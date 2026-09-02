package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

/*
Day 54 - Auth & Security: Security Best Practices

Tasks covered:

 1. Every input validated at the boundary, even from authenticated callers
    (validation.go: allowlists, length limits, path traversal, SSRF)
 2. HTTPS: hardened TLS config, HSTS and the rest of the security headers,
    plaintext refused for credential-carrying methods (tls.go)
 3. Rate limiting: a global per-client limit plus a much stricter one on
    login (ratelimit.go)
 4. Dependency audit: `go run . audit` runs govulncheck

Files:

	validation.go     input validation helpers
	ratelimit.go      token bucket limiter, keying, middleware
	tls.go            TLS config, security headers, HTTPS enforcement
	main.go           the API that puts them together
	security_test.go  tests for each defence

Run:

	go run .            # plaintext HTTP on :8080 (development)
	go run . tls        # HTTPS on :8443 with a self-signed certificate
	go run . audit      # govulncheck over this module
	go run . checklist  # the pre-production security checklist

Environment variables:

	PORT             HTTP port.                       Default: 8080
	TLS_PORT         HTTPS port.                      Default: 8443
	ALLOW_PLAINTEXT  Skip the HTTPS requirement.      Default: true (dev)
	TRUSTED_PROXY    Trust X-Forwarded-* headers.     Default: false
	RATE_PER_SECOND  Global limit per client.         Default: 5
	RATE_BURST       Burst allowance.                 Default: 10

Try it:

	curl localhost:8080/api/echo -d '{"username":"ada","message":"hello"}'
	for i in $(seq 1 20); do curl -s -o /dev/null -w "%{http_code} " localhost:8080/api/echo -d '{}'; done
	curl -XPOST localhost:8080/api/login -d '{"email":"ada@example.com","password":"wrong"}'

Test:

	go test ./...
*/

var (
	// Deployment switches, read once at startup so the request path does not
	// have to look at the environment.
	trustProxyHeaders bool
	allowPlaintext    bool
)

type API struct {
	global *Limiter
	login  *Limiter
}

func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /api/echo", a.echo)
	mux.HandleFunc("POST /api/login", a.login_)
	mux.HandleFunc("GET /api/files/{name}", a.readFile)
	mux.HandleFunc("POST /api/fetch", a.fetchURL)

	// Order matters: security headers outermost so they are present even on
	// a 429 or a redirect, then HTTPS enforcement, then rate limiting, then
	// the routes.
	var handler http.Handler = mux

	handler = RateLimit(a.global, ClientIPKey)(handler)
	handler = RequireHTTPS(handler)
	handler = SecurityHeaders(!allowPlaintext)(handler)
	handler = requestSizeLimit(1 << 20)(handler)

	return handler
}

// requestSizeLimit caps the body before any handler reads it. Without it, a
// single request can make the process allocate until it dies.
func requestSizeLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

			next.ServeHTTP(w, r)
		})
	}
}

//
// HANDLERS
//

func (a *API) echo(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Message  string `json:"message"`
		Priority string `json:"priority"`
		Retries  int    `json:"retries"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	// Validate everything, in order, and stop at the first failure. The
	// message names the field and the rule - never echoes the bad value back,
	// which would turn an error page into a reflection gadget.
	username, err := ValidateUsername(input.Username)
	if err != nil {
		respondInvalid(w, err)
		return
	}

	message, err := ValidateText("message", input.Message, 1, 500)
	if err != nil {
		respondInvalid(w, err)
		return
	}

	priority := "normal"

	if input.Priority != "" {
		if priority, err = ValidateEnum("priority", input.Priority, "low", "normal", "high"); err != nil {
			respondInvalid(w, err)
			return
		}
	}

	retries, err := ValidateRange("retries", input.Retries, 0, 5)
	if err != nil {
		respondInvalid(w, err)
		return
	}

	// User input in a log line goes through EscapeForLog, so a newline in a
	// username cannot forge a second log entry.
	log.Printf("echo user=%s priority=%s retries=%d", EscapeForLog(username), priority, retries)

	writeJSON(w, http.StatusOK, map[string]any{
		"username": username,
		"message":  message,
		"priority": priority,
		"retries":  retries,
	})
}

// login_ is deliberately behind a second, much stricter limiter: this is the
// endpoint an attacker will try thousands of times.
func (a *API) login_(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	email, err := ValidateEmail(input.Email)
	if err != nil {
		// Same 401 as a wrong password: a 422 here would confirm which emails
		// are even shaped like accounts.
		unauthorized(w)
		return
	}

	allowed, retryAfter := a.login.Allow(LoginKey(email, r))
	if !allowed {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", int(retryAfter.Seconds())+1))

		log.Printf("login rate limited email=%s ip=%s", EscapeForLog(email), ClientIP(r))
		writeError(w, http.StatusTooManyRequests, "too many login attempts")

		return
	}

	// The real credential check lives in Day 51; this day is about what
	// surrounds it.
	if email != "ada@example.com" || input.Password != "correct-horse-7" {
		log.Printf("failed login email=%s ip=%s", EscapeForLog(email), ClientIP(r))
		unauthorized(w)

		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "authenticated"})
}

func (a *API) readFile(w http.ResponseWriter, r *http.Request) {
	// The classic path traversal target: a user-controlled file name.
	resolved, err := SafeJoin("public", r.PathValue("name"))
	if err != nil {
		log.Printf("path traversal attempt name=%s ip=%s",
			EscapeForLog(r.PathValue("name")), ClientIP(r))
		respondInvalid(w, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"resolved": resolved,
		"note":     "the file is not actually read here; the point is that the path stayed inside public/",
	})
}

func (a *API) fetchURL(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL string `json:"url"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	target, err := ValidateOutboundURL(input.URL)
	if err != nil {
		log.Printf("blocked outbound url from ip=%s: %v", ClientIP(r), err)
		respondInvalid(w, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"host":   target.Hostname(),
		"scheme": target.Scheme,
		"note":   "the request is not actually made here; the point is that private ranges were rejected",
	})
}

//
// RESPONSES
//

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	defer func() {
		if err := r.Body.Close(); err != nil {
			log.Printf("close body: %v", err)
		}
	}()

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}

	return true
}

func respondInvalid(w http.ResponseWriter, err error) {
	writeError(w, http.StatusUnprocessableEntity,
		strings.TrimPrefix(err.Error(), "invalid input: "))
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
	writeError(w, http.StatusUnauthorized, "invalid email or password")
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("encode response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

//
// DEPENDENCY AUDIT
//

// runAudit shells out to govulncheck, which cross-references the module graph
// against the Go vulnerability database - and, unlike a plain dependency
// scanner, reports only vulnerabilities in code paths this binary can reach.
func runAudit() error {
	fmt.Println("\nDependency audit")
	fmt.Println(strings.Repeat("-", 72))

	path, err := exec.LookPath("govulncheck")
	if err != nil {
		fmt.Println("govulncheck is not installed. Install it with:")
		fmt.Println()
		fmt.Println("    go install golang.org/x/vuln/cmd/govulncheck@latest")
		fmt.Println()
		fmt.Println("Then run it over the module:")
		fmt.Println()
		fmt.Println("    govulncheck ./...")
		fmt.Println()
		fmt.Println("In CI it is one step, and a non-zero exit fails the build (Day 79).")

		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	command := exec.CommandContext(ctx, path, "./...")
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr

	fmt.Printf("running %s ./...\n\n", path)

	if err := command.Run(); err != nil {
		// A non-zero exit means findings, which is information, not a crash.
		return fmt.Errorf("govulncheck reported findings: %w", err)
	}

	fmt.Println("\nNo known vulnerabilities in reachable code.")

	return nil
}

func printChecklist() {
	fmt.Println("\nPre-production security checklist")
	fmt.Println(strings.Repeat("-", 72))

	items := []struct {
		area string
		item string
	}{
		{"transport", "TLS 1.2+ everywhere, HSTS on, plaintext refused or redirected"},
		{"transport", "certificates renewed automatically, expiry alerted on"},
		{"input", "every field validated against an allowlist at the boundary"},
		{"input", "request body size capped, JSON unknown fields rejected"},
		{"input", "user input escaped before it reaches logs, SQL, shells or HTML"},
		{"abuse", "rate limits on login, registration, password reset and search"},
		{"abuse", "limiter state bounded and evicted, keyed per identity not just per IP"},
		{"secrets", "no secret in source, in an image layer, or in a log line"},
		{"secrets", "signing keys and database passwords rotatable without downtime"},
		{"authz", "every handler checks a permission; the default is deny"},
		{"deps", "govulncheck in CI, dependencies updated on a schedule"},
		{"deps", "go.sum committed, builds reproducible, module proxy pinned"},
		{"errors", "internal errors logged in full, returned as a generic message"},
		{"data", "passwords hashed with bcrypt/argon2, tokens stored as hashes"},
		{"ops", "graceful shutdown, health checks, and an alert on 5xx rate"},
	}

	area := ""

	for _, entry := range items {
		if entry.area != area {
			area = entry.area

			fmt.Printf("\n%s\n", strings.ToUpper(area))
		}

		fmt.Printf("  [ ] %s\n", entry.item)
	}

	fmt.Println("\nNone of these is exotic. Every one of them is a real incident that")
	fmt.Println("happened to somebody who thought the item was obvious.")
}

//
// MAIN
//

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day54: ")

	trustProxyHeaders = envBool("TRUSTED_PROXY", false)
	allowPlaintext = envBool("ALLOW_PLAINTEXT", true)

	command := ""
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	switch command {
	case "audit":
		if err := runAudit(); err != nil {
			log.Fatalf("%v", err)
		}

		return

	case "checklist":
		printChecklist()

		return
	}

	api := &API{
		// Global: generous, meant to stop one client from monopolising the
		// service.
		global: NewLimiter(envFloat("RATE_PER_SECOND", 5), envInt("RATE_BURST", 10), 10*time.Minute),
		// Login: 5 attempts, then one more every 12 seconds. Brute forcing a
		// password at that rate takes longer than the universe has left.
		login: NewLimiter(1.0/12.0, 5, time.Hour),
	}

	stopCleanup := make(chan struct{})
	defer close(stopCleanup)

	go api.global.RunCleanup(time.Minute, stopCleanup)
	go api.login.RunCleanup(time.Minute, stopCleanup)

	useTLS := command == "tls"

	if useTLS {
		allowPlaintext = false
	}

	address := ":" + envOr("PORT", "8080")
	if useTLS {
		address = ":" + envOr("TLS_PORT", "8443")
	}

	server := &http.Server{
		Addr:              address,
		Handler:           api.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		// Even the TLS handshake gets a deadline: an attacker opening
		// connections and never completing them is a cheap resource attack.
		TLSConfig: HardenedTLSConfig(),
	}

	serverErrors := make(chan error, 1)

	go func() {
		if useTLS {
			certificate, err := SelfSignedCertificate("localhost")
			if err != nil {
				serverErrors <- err
				return
			}

			server.TLSConfig.Certificates = []tls.Certificate{certificate}

			log.Printf("listening on https://localhost%s (self-signed, browsers will warn)", address)
			log.Printf("try: curl -k https://localhost%s/healthz -i", address)

			if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
				serverErrors <- err
			}

			return
		}

		log.Printf("listening on http://localhost%s (development, ALLOW_PLAINTEXT=%t)", address, allowPlaintext)

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		log.Fatalf("server error: %v", err)
	case received := <-shutdown:
		log.Printf("shutdown signal: %s", received)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown: %v", err)
	}

	log.Printf("stopped cleanly")
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))

	switch strings.ToLower(value) {
	case "":
		return fallback
	case "1", "true", "yes":
		return true
	case "0", "false", "no":
		return false
	default:
		log.Printf("invalid %s=%q, using %t", key, value, fallback)
		return fallback
	}
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))

	if value == "" {
		return fallback
	}

	var parsed int

	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil || parsed <= 0 {
		log.Printf("invalid %s=%q, using %d", key, value, fallback)
		return fallback
	}

	return parsed
}

func envFloat(key string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(key))

	if value == "" {
		return fallback
	}

	var parsed float64

	if _, err := fmt.Sscanf(value, "%f", &parsed); err != nil || parsed <= 0 {
		log.Printf("invalid %s=%q, using %v", key, value, fallback)
		return fallback
	}

	return parsed
}
