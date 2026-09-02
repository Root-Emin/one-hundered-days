package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

/*
Day 52 - Auth & Security: JWT and Sessions

Tasks covered:

 1. Issue JWTs signed with a server secret and a short expiration
 2. Validate signatures, algorithm, issuer, audience and expiry on every
    protected request
 3. Load signing keys from the environment, with a key id for rotation
 4. Compare stateless JWTs with server-side sessions (printed by `go run . compare`)

Files:

	token.go       claims, keyring, issuing, verification, refresh rotation
	main.go        HTTP API, middleware, the trade-off summary
	token_test.go  expiry, tampering, alg=none, wrong issuer, rotation, replay

Run:

	go run .            # HTTP server on :8080
	go run . compare    # JWT vs session trade-offs
	go run . attack     # what a forged token looks like, and why it fails

Environment variables:

	PORT                 HTTP port.                       Default: 8080
	JWT_SIGNING_KEY      Signing secret, >= 32 chars.     Required in production
	JWT_SIGNING_KEY_ID   Key id in the token header.      Default: v1
	JWT_PREVIOUS_KEYS    "kid:secret,..." still accepted during rotation
	ENV                  production disables the generated dev key

Try it:

	curl -XPOST localhost:8080/auth/login -d '{"email":"ada@example.com","password":"correct-horse-7"}'
	curl localhost:8080/me -H "Authorization: Bearer <access_token>"
	curl -XPOST localhost:8080/auth/refresh -d '{"refresh_token":"<refresh_token>"}'

Test:

	go test ./...
*/

//
// USERS (in memory: Day 51 covered the database side)
//

type User struct {
	ID           int64
	Email        string
	Roles        []string
	passwordHash string
}

type UserStore struct {
	mu    sync.RWMutex
	users map[string]User
}

func NewUserStore() (*UserStore, error) {
	store := &UserStore{users: make(map[string]User)}

	seed := []struct {
		id       int64
		email    string
		password string
		roles    []string
	}{
		{1, "ada@example.com", "correct-horse-7", []string{"member"}},
		{2, "admin@example.com", "admin-horse-battery-9", []string{"member", "admin"}},
	}

	for _, item := range seed {
		hash, err := bcrypt.GenerateFromPassword([]byte(item.password), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("seed user %s: %w", item.email, err)
		}

		store.users[item.email] = User{
			ID:           item.id,
			Email:        item.email,
			Roles:        item.roles,
			passwordHash: string(hash),
		}
	}

	return store, nil
}

func (s *UserStore) Authenticate(email, password string) (User, error) {
	s.mu.RLock()
	user, found := s.users[strings.ToLower(strings.TrimSpace(email))]
	s.mu.RUnlock()

	if !found {
		return User{}, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.passwordHash), []byte(password)); err != nil {
		return User{}, ErrInvalidCredentials
	}

	return user, nil
}

func (s *UserStore) ByID(id int64) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, user := range s.users {
		if user.ID == id {
			return user, nil
		}
	}

	return User{}, ErrInvalidCredentials
}

var ErrInvalidCredentials = errors.New("invalid email or password")

//
// API
//

type API struct {
	users    *UserStore
	tokens   *TokenService
	refresh  *RefreshStore
	accessTL time.Duration
}

func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /auth/login", a.login)
	mux.HandleFunc("POST /auth/refresh", a.refreshToken)

	mux.Handle("GET /me", a.RequireAuth(http.HandlerFunc(a.me)))
	mux.Handle("POST /auth/logout", a.RequireAuth(http.HandlerFunc(a.logout)))

	return mux
}

type contextKey string

const claimsContextKey contextKey = "claims"

func claimsFrom(ctx context.Context) (Claims, bool) {
	claims, ok := ctx.Value(claimsContextKey).(Claims)

	return claims, ok
}

// RequireAuth verifies the bearer token. Everything about validity is decided
// inside TokenService.Verify, so no handler can accidentally skip a check.
func (a *API) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, raw, found := strings.Cut(r.Header.Get("Authorization"), " ")

		if !found || !strings.EqualFold(scheme, "bearer") || strings.TrimSpace(raw) == "" {
			unauthorized(w, "missing bearer token")
			return
		}

		claims, err := a.tokens.Verify(strings.TrimSpace(raw))
		if err != nil {
			// The reason is logged, not returned: an attacker does not need
			// to know whether the signature or the expiry failed.
			log.Printf("token rejected: %v", err)

			switch {
			case errors.Is(err, ErrExpiredToken):
				unauthorized(w, "token expired")
			default:
				unauthorized(w, "invalid token")
			}

			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsContextKey, claims)))
	})
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	user, err := a.users.Authenticate(input.Email, input.Password)
	if err != nil {
		unauthorized(w, "invalid email or password")
		return
	}

	access, _, err := a.tokens.Issue(user.ID, user.Email, user.Roles, a.accessTL)
	if err != nil {
		log.Printf("issue access token: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	refresh, err := a.refresh.Issue(user.ID, RefreshTokenTTL)
	if err != nil {
		log.Printf("issue refresh token: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"token_type":    "Bearer",
		"expires_in":    int(a.accessTL.Seconds()),
	})
}

// refreshToken trades a refresh token for a new pair. The old refresh token
// is consumed: reuse means it leaked, and Redeem revokes the whole family.
func (a *API) refreshToken(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RefreshToken string `json:"refresh_token"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	userID, err := a.refresh.Redeem(input.RefreshToken)
	if err != nil {
		log.Printf("refresh rejected: %v", err)
		unauthorized(w, "invalid refresh token")

		return
	}

	user, err := a.users.ByID(userID)
	if err != nil {
		unauthorized(w, "invalid refresh token")
		return
	}

	access, _, err := a.tokens.Issue(user.ID, user.Email, user.Roles, a.accessTL)
	if err != nil {
		log.Printf("issue access token: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	rotated, err := a.refresh.Issue(user.ID, RefreshTokenTTL)
	if err != nil {
		log.Printf("rotate refresh token: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"refresh_token": rotated,
		"token_type":    "Bearer",
		"expires_in":    int(a.accessTL.Seconds()),
	})
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		unauthorized(w, "missing token")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":    claims.Subject,
		"email":      claims.Email,
		"roles":      claims.Roles,
		"expires_at": claims.ExpiresAt.Time.Format(time.RFC3339),
		"token_id":   claims.ID,
	})
}

// logout adds the access token's jti to the denylist and drops every refresh
// token of the user. This is the honest answer to JWT revocation: it needs
// state, and the only question is how much.
func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		unauthorized(w, "missing token")
		return
	}

	a.tokens.Revoke(claims)

	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err == nil {
		a.refresh.RevokeAll(userID)
	}

	w.WriteHeader(http.StatusNoContent)
}

//
// HTTP HELPERS
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

func unauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
	writeError(w, http.StatusUnauthorized, message)
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
// COMPARISON AND ATTACK DEMOS
//

func printComparison() {
	fmt.Println("\nStateless JWT vs server-side session")
	fmt.Println(strings.Repeat("-", 92))

	rows := []struct {
		aspect  string
		jwt     string
		session string
	}{
		{"Where state lives", "in the token, signed", "in the database, keyed by an opaque id"},
		{"Verification cost", "one HMAC, no I/O", "one lookup per request"},
		{"Revocation", "needs a denylist or short TTL", "delete the row, effective immediately"},
		{"Logout everywhere", "denylist every jti, or rotate the key", "delete the user's rows"},
		{"Token size", "hundreds of bytes, grows with claims", "~32 bytes"},
		{"Claims freshness", "stale until expiry (role changes lag)", "read fresh on every request"},
		{"Cross-service use", "any service with the key can verify", "needs shared session storage"},
		{"Leak impact", "valid until it expires", "valid until the row is deleted"},
	}

	fmt.Printf("%-22s %-38s %s\n", "ASPECT", "JWT", "SESSION")

	for _, row := range rows {
		fmt.Printf("%-22s %-38s %s\n", row.aspect, row.jwt, row.session)
	}

	fmt.Println("\nRule of thumb:")
	fmt.Println("  one product, one backend, browser clients   -> sessions (simpler, revocable)")
	fmt.Println("  many services, mobile clients, no shared DB -> short JWT + stored refresh token")
	fmt.Println("\nThis service uses the hybrid: a 15 minute stateless access token, a")
	fmt.Println("stored and rotating refresh token, and a jti denylist for early logout.")
}

// demonstrateAttacks shows why the validation options in token.go are not
// optional decoration.
func demonstrateAttacks(service *TokenService) error {
	fmt.Println("\nForged tokens and why they fail")
	fmt.Println(strings.Repeat("-", 92))

	valid, claims, err := service.Issue(1, "ada@example.com", []string{"member"}, time.Minute)
	if err != nil {
		return err
	}

	fmt.Printf("  valid token accepted, subject=%s roles=%v\n", claims.Subject, claims.Roles)

	// 1. alg=none: the classic. The signature is dropped entirely.
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT","kid":"v1"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(
		`{"iss":%q,"aud":[%q],"sub":"2","email":"admin@example.com","roles":["admin"],"exp":%d}`,
		issuer, audience, time.Now().Add(time.Hour).Unix())))

	if _, err := service.Verify(header + "." + payload + "."); err == nil {
		return errors.New("alg=none token was accepted")
	} else {
		fmt.Printf("  alg=none rejected: %v\n", trim(err))
	}

	// 2. Tampered payload, original signature.
	parts := strings.Split(valid, ".")

	tamperedPayload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(
		`{"iss":%q,"aud":[%q],"sub":"1","email":"ada@example.com","roles":["admin"],"exp":%d}`,
		issuer, audience, time.Now().Add(time.Hour).Unix())))

	if _, err := service.Verify(parts[0] + "." + tamperedPayload + "." + parts[2]); err == nil {
		return errors.New("tampered token was accepted")
	} else {
		fmt.Printf("  role escalation rejected: %v\n", trim(err))
	}

	// 3. Correct shape, signed with a different secret.
	foreign := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		Email: "attacker@example.com",
		Roles: []string{"admin"},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   "999",
			Audience:  jwt.ClaimStrings{audience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			ID:        "forged",
		},
	})
	foreign.Header["kid"] = "v1"

	forged, err := foreign.SignedString([]byte("an-attacker-controlled-secret-key"))
	if err != nil {
		return err
	}

	if _, err := service.Verify(forged); err == nil {
		return errors.New("token signed with a foreign key was accepted")
	} else {
		fmt.Printf("  wrong signing key rejected: %v\n", trim(err))
	}

	// 4. Expired.
	expired, _, err := service.Issue(1, "ada@example.com", []string{"member"}, -time.Minute)
	if err != nil {
		return err
	}

	if _, err := service.Verify(expired); !errors.Is(err, ErrExpiredToken) {
		return fmt.Errorf("expired token: got %v, want ErrExpiredToken", err)
	} else {
		fmt.Printf("  expired token rejected: %v\n", trim(err))
	}

	// 5. Revoked before expiry.
	service.Revoke(claims)

	if _, err := service.Verify(valid); !errors.Is(err, ErrRevoked) {
		return fmt.Errorf("revoked token: got %v, want ErrRevoked", err)
	} else {
		fmt.Printf("  revoked token rejected: %v\n", err)
	}

	fmt.Println("\n  Note what made each of these fail: the algorithm allowlist, the")
	fmt.Println("  signature check, the issuer/audience match, the expiry, and the")
	fmt.Println("  denylist. Remove any one of them and one of the five gets through.")

	return nil
}

func trim(err error) string {
	message := err.Error()

	if len(message) > 68 {
		return message[:68] + "..."
	}

	return message
}

//
// MAIN
//

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day52: ")

	keyring, err := LoadKeyring()
	if err != nil {
		log.Fatalf("load signing keys: %v", err)
	}

	if os.Getenv("JWT_SIGNING_KEY") == "" {
		if strings.EqualFold(os.Getenv("ENV"), "production") {
			// A generated key means every restart invalidates every token and
			// every replica disagrees. Never in production.
			log.Fatalf("JWT_SIGNING_KEY is required when ENV=production")
		}

		log.Printf("WARNING: JWT_SIGNING_KEY is not set, generated a throwaway development key")
	}

	tokens := NewTokenService(keyring)

	command := ""
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	switch command {
	case "compare":
		printComparison()
		return

	case "attack":
		if err := demonstrateAttacks(tokens); err != nil {
			log.Fatalf("attack demo: %v", err)
		}

		return
	}

	users, err := NewUserStore()
	if err != nil {
		log.Fatalf("seed users: %v", err)
	}

	api := &API{
		users:    users,
		tokens:   tokens,
		refresh:  NewRefreshStore(),
		accessTL: AccessTokenTTL,
	}

	server := &http.Server{
		Addr:              ":" + envOr("PORT", "8080"),
		Handler:           api.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)

	go func() {
		log.Printf("listening on %s access_ttl=%s kid=%s",
			server.Addr, AccessTokenTTL, keyring.activeKID)

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	// Keep the denylist bounded.
	purgeCtx, stopPurge := context.WithCancel(context.Background())
	defer stopPurge()

	go func() {
		ticker := time.NewTicker(AccessTokenTTL)
		defer ticker.Stop()

		for {
			select {
			case <-purgeCtx.Done():
				return
			case <-ticker.C:
				if removed := tokens.PurgeRevoked(); removed > 0 {
					log.Printf("purged %d expired denylist entries", removed)
				}
			}
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
