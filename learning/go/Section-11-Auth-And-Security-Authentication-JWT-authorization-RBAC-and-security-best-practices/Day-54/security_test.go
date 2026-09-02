package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

//
// VALIDATION
//

func TestValidateEmail(t *testing.T) {
	t.Parallel()

	valid := []string{"ada@example.com", "ADA@Example.COM", " user.name+tag@sub.example.co ", "a@b.io"}

	for _, input := range valid {
		if _, err := ValidateEmail(input); err != nil {
			t.Errorf("ValidateEmail(%q) = %v, want ok", input, err)
		}
	}

	invalid := []string{
		"", "nope", "@example.com", "user@", "user@example",
		"user name@example.com", "user@exa mple.com",
		strings.Repeat("a", 250) + "@example.com",
	}

	for _, input := range invalid {
		if _, err := ValidateEmail(input); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("ValidateEmail(%q) accepted the value", input)
		}
	}

	// Normalisation is part of the contract.
	normalized, err := ValidateEmail(" ADA@Example.com ")
	if err != nil || normalized != "ada@example.com" {
		t.Fatalf("normalised = %q (err=%v)", normalized, err)
	}
}

func TestValidateUsername(t *testing.T) {
	t.Parallel()

	if _, err := ValidateUsername("ada_lovelace-1"); err != nil {
		t.Fatalf("valid username rejected: %v", err)
	}

	// Case is normalised rather than rejected: "Ada" and "ada" are the same
	// account, and treating them as two is how impersonation starts.
	normalized, err := ValidateUsername("  Ada  ")
	if err != nil || normalized != "ada" {
		t.Fatalf("normalised = %q (err=%v), want \"ada\"", normalized, err)
	}

	invalid := []string{"ab", "has space", "with!bang", strings.Repeat("a", 33), "admin", "root", "ADMIN"}

	for _, input := range invalid {
		if _, err := ValidateUsername(input); err == nil {
			t.Errorf("ValidateUsername(%q) was accepted", input)
		}
	}
}

func TestValidateText(t *testing.T) {
	t.Parallel()

	if _, err := ValidateText("message", "hello\nworld\t!", 1, 100); err != nil {
		t.Fatalf("newline and tab rejected: %v", err)
	}

	// Length is counted in runes, so a multi-byte string is not unfairly cut.
	if _, err := ValidateText("message", strings.Repeat("ü", 100), 1, 100); err != nil {
		t.Fatalf("100 runes rejected: %v", err)
	}

	invalid := map[string]string{
		"too short":        "",
		"too long":         strings.Repeat("a", 101),
		"control char":     "bell\x07here",
		"escape sequence":  "\x1b[31mred",
		"invalid utf-8":    string([]byte{0xff, 0xfe}),
		"null byte inside": "abc\x00def",
	}

	for name, input := range invalid {
		if _, err := ValidateText("message", input, 1, 100); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestSafeJoinBlocksTraversal(t *testing.T) {
	t.Parallel()

	allowed := map[string]string{
		"report.pdf":       "/public/report.pdf",
		"nested/file.txt":  "/public/nested/file.txt",
		"./also-fine.json": "/public/also-fine.json",
	}

	for input, want := range allowed {
		got, err := SafeJoin("public", input)
		if err != nil {
			t.Errorf("SafeJoin(%q) = %v, want %q", input, err, want)
			continue
		}

		if got != want {
			t.Errorf("SafeJoin(%q) = %q, want %q", input, got, want)
		}
	}

	blocked := []string{
		"../secrets.env",
		"../../etc/passwd",
		"nested/../../escape",
		"/etc/passwd",
		"",
		"file\x00.txt",
	}

	for _, input := range blocked {
		if resolved, err := SafeJoin("public", input); err == nil {
			t.Errorf("SafeJoin(%q) escaped to %q", input, resolved)
		}
	}
}

func TestValidateOutboundURLBlocksPrivateTargets(t *testing.T) {
	t.Parallel()

	blocked := []string{
		"http://127.0.0.1:8080/admin",
		"http://localhost/admin",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/",
		"http://10.0.0.5/internal",
		"http://192.168.1.1/router",
		"file:///etc/passwd",
		"ftp://example.com/",
		"not a url at all",
		"https://",
	}

	for _, input := range blocked {
		if _, err := ValidateOutboundURL(input); err == nil {
			t.Errorf("ValidateOutboundURL(%q) was allowed", input)
		}
	}
}

func TestEscapeForLog(t *testing.T) {
	t.Parallel()

	// A forged log line is the attack this prevents.
	escaped := EscapeForLog("ada\n2026-01-01 admin logged in")

	if strings.Contains(escaped, "\n") {
		t.Fatalf("newline survived escaping: %q", escaped)
	}

	if !strings.Contains(escaped, "\\n") {
		t.Fatalf("newline was dropped instead of escaped: %q", escaped)
	}

	if escaped := EscapeForLog("\x1b[31mred"); strings.Contains(escaped, "\x1b") {
		t.Fatalf("ANSI escape survived: %q", escaped)
	}

	if len(EscapeForLog(strings.Repeat("a", 1000))) > 210 {
		t.Fatal("long input was not truncated")
	}
}

//
// RATE LIMITING
//

func TestLimiterAllowsBurstThenBlocks(t *testing.T) {
	t.Parallel()

	// One request per second, burst of 3.
	limiter := NewLimiter(1, 3, time.Minute)

	for i := range 3 {
		if allowed, _ := limiter.Allow("client"); !allowed {
			t.Fatalf("request %d in the burst was blocked", i+1)
		}
	}

	allowed, retryAfter := limiter.Allow("client")

	if allowed {
		t.Fatal("the fourth request was allowed: the burst is not enforced")
	}

	if retryAfter <= 0 {
		t.Fatal("no retry-after hint was returned")
	}
}

func TestLimiterIsPerKey(t *testing.T) {
	t.Parallel()

	limiter := NewLimiter(1, 1, time.Minute)

	if allowed, _ := limiter.Allow("first"); !allowed {
		t.Fatal("first client blocked")
	}

	// A different key must have its own bucket.
	if allowed, _ := limiter.Allow("second"); !allowed {
		t.Fatal("one client's traffic exhausted another client's budget")
	}

	if allowed, _ := limiter.Allow("first"); allowed {
		t.Fatal("first client was allowed a second request")
	}
}

func TestLimiterRejectionDoesNotConsumeFutureCapacity(t *testing.T) {
	t.Parallel()

	limiter := NewLimiter(100, 1, time.Minute)

	if allowed, _ := limiter.Allow("client"); !allowed {
		t.Fatal("first request blocked")
	}

	// Hammer it while it is empty.
	for range 20 {
		if allowed, _ := limiter.Allow("client"); allowed {
			t.Fatal("a request was allowed before the bucket refilled")
		}
	}

	// At 100/s the bucket refills in 10ms; the rejected attempts must not
	// have pushed that further out.
	time.Sleep(30 * time.Millisecond)

	if allowed, _ := limiter.Allow("client"); !allowed {
		t.Fatal("bucket did not refill: rejected requests consumed capacity")
	}
}

func TestLimiterCleanupEvictsIdleBuckets(t *testing.T) {
	t.Parallel()

	limiter := NewLimiter(10, 10, time.Millisecond)

	for _, key := range []string{"a", "b", "c"} {
		limiter.Allow(key)
	}

	if limiter.Size() != 3 {
		t.Fatalf("size = %d, want 3", limiter.Size())
	}

	time.Sleep(5 * time.Millisecond)

	if removed := limiter.Cleanup(); removed != 3 {
		t.Fatalf("cleanup removed %d, want 3", removed)
	}

	if limiter.Size() != 0 {
		t.Fatalf("size after cleanup = %d, want 0 - the map is a memory leak", limiter.Size())
	}
}

//
// HTTP
//

func newTestServer(t *testing.T, perSecond float64, burst int) *httptest.Server {
	t.Helper()

	allowPlaintext = true
	trustProxyHeaders = false

	api := &API{
		global: NewLimiter(perSecond, burst, time.Minute),
		login:  NewLimiter(1.0/12.0, 3, time.Hour),
	}

	server := httptest.NewServer(api.Routes())

	t.Cleanup(server.Close)

	return server
}

func post(t *testing.T, server *httptest.Server, path string, body any) (*http.Response, []byte) {
	t.Helper()

	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	var buffer bytes.Buffer

	if _, err := buffer.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}

	if err := resp.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}

	return resp, buffer.Bytes()
}

func TestSecurityHeadersArePresent(t *testing.T) {
	server := newTestServer(t, 100, 100)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/healthz", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
	}()

	required := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
		"Cache-Control":           "no-store",
	}

	for header, want := range required {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}

	// HSTS must NOT be sent over plaintext.
	if resp.Header.Get("Strict-Transport-Security") != "" {
		t.Error("HSTS sent over a plaintext connection")
	}
}

func TestValidationRejectsBadInput(t *testing.T) {
	server := newTestServer(t, 100, 100)

	tests := []struct {
		name string
		body map[string]any
		want int
	}{
		{"valid", map[string]any{"username": "ada", "message": "hello"}, http.StatusOK},
		{"reserved username", map[string]any{"username": "admin", "message": "hi"}, http.StatusUnprocessableEntity},
		{"empty message", map[string]any{"username": "ada", "message": ""}, http.StatusUnprocessableEntity},
		{"control character", map[string]any{"username": "ada", "message": "a\x07b"}, http.StatusUnprocessableEntity},
		{"bad priority", map[string]any{"username": "ada", "message": "hi", "priority": "urgent"}, http.StatusUnprocessableEntity},
		{"retries out of range", map[string]any{"username": "ada", "message": "hi", "retries": 99}, http.StatusUnprocessableEntity},
		{"unknown field", map[string]any{"username": "ada", "message": "hi", "is_admin": true}, http.StatusBadRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resp, body := post(t, server, "/api/echo", test.body)

			if resp.StatusCode != test.want {
				t.Fatalf("status = %d, want %d (%s)", resp.StatusCode, test.want, body)
			}
		})
	}
}

func TestRateLimitReturns429WithRetryAfter(t *testing.T) {
	server := newTestServer(t, 1, 2)

	valid := map[string]any{"username": "ada", "message": "hello"}

	for i := range 2 {
		if resp, body := post(t, server, "/api/echo", valid); resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d = %d (%s)", i+1, resp.StatusCode, body)
		}
	}

	resp, _ := post(t, server, "/api/echo", valid)

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}

	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("429 without a Retry-After header")
	}

	// Security headers survive the rejection: the middleware order matters.
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("security headers missing on a rate-limited response")
	}
}

func TestLoginLimiterIsStricter(t *testing.T) {
	server := newTestServer(t, 100, 100) // global limit out of the way

	attempt := map[string]any{"email": "ada@example.com", "password": "wrong"}

	statuses := make([]int, 0, 6)

	for range 6 {
		resp, _ := post(t, server, "/api/login", attempt)
		statuses = append(statuses, resp.StatusCode)
	}

	if statuses[0] != http.StatusUnauthorized {
		t.Fatalf("first attempt = %d, want 401", statuses[0])
	}

	if statuses[len(statuses)-1] != http.StatusTooManyRequests {
		t.Fatalf("brute force was never rate limited: %v", statuses)
	}
}

func TestPathTraversalIsRejectedByTheAPI(t *testing.T) {
	server := newTestServer(t, 100, 100)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		server.URL+"/api/files/..%2F..%2Fetc%2Fpasswd", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
	}()

	if resp.StatusCode == http.StatusOK {
		t.Fatal("a traversal path was accepted")
	}
}

//
// TLS
//

func TestHardenedTLSConfig(t *testing.T) {
	t.Parallel()

	config := HardenedTLSConfig()

	if config.MinVersion < tls.VersionTLS12 {
		t.Fatalf("MinVersion = %x, want at least TLS 1.2", config.MinVersion)
	}

	// No CBC, no RC4, no non-forward-secret suites.
	for _, suite := range config.CipherSuites {
		name := tls.CipherSuiteName(suite)

		if !strings.Contains(name, "ECDHE") {
			t.Errorf("cipher suite %s is not forward secret", name)
		}

		if strings.Contains(name, "CBC") || strings.Contains(name, "RC4") || strings.Contains(name, "3DES") {
			t.Errorf("weak cipher suite enabled: %s", name)
		}
	}
}

func TestSelfSignedCertificateIsUsable(t *testing.T) {
	t.Parallel()

	certificate, err := SelfSignedCertificate("localhost")
	if err != nil {
		t.Fatalf("generate certificate: %v", err)
	}

	if len(certificate.Certificate) == 0 || certificate.PrivateKey == nil {
		t.Fatal("certificate is incomplete")
	}
}

func TestHTTPSIsRequiredWhenPlaintextIsDisabled(t *testing.T) {
	// Not parallel: it flips package-level deployment switches.
	allowPlaintext = false
	trustProxyHeaders = false

	t.Cleanup(func() { allowPlaintext = true })

	api := &API{global: NewLimiter(100, 100, time.Minute), login: NewLimiter(100, 100, time.Minute)}
	server := httptest.NewServer(api.Routes())

	t.Cleanup(server.Close)

	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	// GET is redirected to https.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/healthz", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	if err := resp.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}

	if resp.StatusCode != http.StatusPermanentRedirect {
		t.Fatalf("GET status = %d, want 308 redirect to https", resp.StatusCode)
	}

	if !strings.HasPrefix(resp.Header.Get("Location"), "https://") {
		t.Fatalf("Location = %q, want an https URL", resp.Header.Get("Location"))
	}

	// POST is refused outright rather than redirected: a redirect would make
	// the client re-send the credentials it already leaked.
	req, err = http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/api/login",
		strings.NewReader(`{"email":"ada@example.com","password":"correct-horse-7"}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	if err := resp.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}

	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("POST status = %d, want 426", resp.StatusCode)
	}
}

func TestUntrustedForwardedHeaderIsIgnored(t *testing.T) {
	// An attacker sets X-Forwarded-For to get a fresh rate limit bucket per
	// request. With TRUSTED_PROXY off, the header must be ignored.
	trustProxyHeaders = false

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.5:44321"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")

	if ip := ClientIP(req); ip != "203.0.113.5" {
		t.Fatalf("ClientIP = %q, want the socket address", ip)
	}

	trustProxyHeaders = true

	t.Cleanup(func() { trustProxyHeaders = false })

	if ip := ClientIP(req); ip != "1.2.3.4" {
		t.Fatalf("with a trusted proxy ClientIP = %q, want 1.2.3.4", ip)
	}
}
