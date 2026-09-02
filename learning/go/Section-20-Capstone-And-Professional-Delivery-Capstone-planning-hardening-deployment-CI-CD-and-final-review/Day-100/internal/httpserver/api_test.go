package httpserver_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-100/internal/auth"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-100/internal/config"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-100/internal/httpserver"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-100/internal/service"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-100/internal/store"
)

// harness is the whole service, wired the way main wires it, against a real
// SQLite file. These are integration tests: they exercise routing, auth,
// validation, persistence and the domain rules together, which is where the
// interesting bugs live.
type harness struct {
	server *httptest.Server
	store  *store.Store
	key    string
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	ctx := t.Context()

	dataStore, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if err := dataStore.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	if _, err := store.Migrate(ctx, dataStore.DB()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	generated, err := auth.Generate()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	if err := dataStore.CreateAPIKey(ctx, store.APIKey{
		ID: generated.ID, Owner: "ada", Hash: generated.Hash, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create key: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"

	svc := service.New(dataStore, logger)

	server := httpserver.New(cfg, logger)
	server.Mount(httpserver.NewAPI(svc, "http://short.test", logger), dataStore)
	server.MarkReady()

	httpServer := httptest.NewServer(server.Handler())

	t.Cleanup(httpServer.Close)

	return &harness{server: httpServer, store: dataStore, key: generated.Plaintext}
}

func (h *harness) do(t *testing.T, method, path string, body any, authenticated bool) *http.Response {
	t.Helper()

	var reader io.Reader

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		reader = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(t.Context(), method, h.server.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if authenticated {
		request.Header.Set("Authorization", "Bearer "+h.key)
	}

	// The redirect must be observed, not followed.
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	t.Cleanup(func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
	})

	return response
}

func decodeLink(t *testing.T, response *http.Response) map[string]any {
	t.Helper()

	var body map[string]any

	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	return body
}

// The core journey, end to end: create, follow, count.
func TestCreateFollowAndCount(t *testing.T) {
	h := newHarness(t)

	response := h.do(t, http.MethodPost, "/api/links",
		map[string]any{"target": "https://go.dev", "code": "golang"}, true)

	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", response.StatusCode)
	}

	created := decodeLink(t, response)

	if created["short_url"] != "http://short.test/golang" {
		t.Errorf("short_url = %v", created["short_url"])
	}

	redirect := h.do(t, http.MethodGet, "/golang", nil, false)

	if redirect.StatusCode != http.StatusFound {
		t.Fatalf("redirect status = %d, want 302", redirect.StatusCode)
	}

	if location := redirect.Header.Get("Location"); location != "https://go.dev" {
		t.Errorf("Location = %q, want https://go.dev", location)
	}

	// The click is recorded after the response, so poll rather than assume.
	deadline := time.Now().Add(3 * time.Second)

	for {
		count, err := h.store.ClickCount(t.Context(), "golang")
		if err != nil {
			t.Fatalf("ClickCount: %v", err)
		}

		if count == 1 {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("the click was never recorded (count %d)", count)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// The redirect must NOT require a key: a short link that needs a bearer token
// is not a short link.
func TestRedirectNeedsNoCredential(t *testing.T) {
	h := newHarness(t)

	h.do(t, http.MethodPost, "/api/links", map[string]any{"target": "https://go.dev", "code": "public"}, true)

	if response := h.do(t, http.MethodGet, "/public", nil, false); response.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want 302 without a key", response.StatusCode)
	}
}

func TestAPIRoutesRequireACredential(t *testing.T) {
	h := newHarness(t)

	cases := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPost, "/api/links", map[string]any{"target": "https://go.dev"}},
		{http.MethodGet, "/api/links", nil},
		{http.MethodGet, "/api/links/golang", nil},
		{http.MethodDelete, "/api/links/golang", nil},
	}

	for _, testCase := range cases {
		t.Run(testCase.method+" "+testCase.path, func(t *testing.T) {
			response := h.do(t, testCase.method, testCase.path, testCase.body, false)

			if response.StatusCode != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", response.StatusCode)
			}

			// A 401 without WWW-Authenticate is a 403 with the wrong number.
			if response.Header.Get("WWW-Authenticate") == "" {
				t.Error("no WWW-Authenticate header on a 401")
			}
		})
	}
}

// Gone tells a crawler to forget the URL; Not Found invites it back tomorrow.
func TestDeactivatedLinkIsGoneNotMissing(t *testing.T) {
	h := newHarness(t)

	h.do(t, http.MethodPost, "/api/links", map[string]any{"target": "https://go.dev", "code": "gone01"}, true)

	if response := h.do(t, http.MethodDelete, "/api/links/gone01", nil, true); response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", response.StatusCode)
	}

	response := h.do(t, http.MethodGet, "/gone01", nil, false)

	if response.StatusCode != http.StatusGone {
		t.Errorf("status = %d, want 410", response.StatusCode)
	}

	if missing := h.do(t, http.MethodGet, "/nosuchcode", nil, false); missing.StatusCode != http.StatusNotFound {
		t.Errorf("unknown code status = %d, want 404", missing.StatusCode)
	}
}

func TestExpiredLinkIsGone(t *testing.T) {
	h := newHarness(t)

	// One second in the future, so the create succeeds and the follow does not.
	expires := time.Now().UTC().Add(time.Second)

	h.do(t, http.MethodPost, "/api/links", map[string]any{
		"target": "https://go.dev", "code": "expiry", "expires_at": expires.Format(time.RFC3339),
	}, true)

	time.Sleep(1100 * time.Millisecond)

	if response := h.do(t, http.MethodGet, "/expiry", nil, false); response.StatusCode != http.StatusGone {
		t.Errorf("status = %d, want 410 for an expired link", response.StatusCode)
	}
}

func TestValidationErrors(t *testing.T) {
	h := newHarness(t)

	cases := map[string]any{
		"javascript target": map[string]any{"target": "javascript:alert(1)"},
		"relative target":   map[string]any{"target": "/local"},
		"reserved code":     map[string]any{"target": "https://go.dev", "code": "api"},
		"invalid code":      map[string]any{"target": "https://go.dev", "code": "has space"},
		"bad expiry":        map[string]any{"target": "https://go.dev", "expires_at": "next tuesday"},
		"unknown field":     map[string]any{"target": "https://go.dev", "colour": "blue"},
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			response := h.do(t, http.MethodPost, "/api/links", body, true)

			if response.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", response.StatusCode)
			}
		})
	}
}

func TestDuplicateCodeIsAConflict(t *testing.T) {
	h := newHarness(t)

	h.do(t, http.MethodPost, "/api/links", map[string]any{"target": "https://go.dev", "code": "taken1"}, true)

	response := h.do(t, http.MethodPost, "/api/links",
		map[string]any{"target": "https://example.com", "code": "taken1"}, true)

	if response.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", response.StatusCode)
	}
}

// An empty list must be [] and not null: a client ranging over null throws.
func TestEmptyListIsAnArray(t *testing.T) {
	h := newHarness(t)

	response := h.do(t, http.MethodGet, "/api/links", nil, true)

	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if string(bytes.TrimSpace(payload)) != "[]" {
		t.Errorf("body = %s, want []", payload)
	}
}

func TestProbesAndRoot(t *testing.T) {
	h := newHarness(t)

	for path, want := range map[string]int{
		"/healthz": http.StatusOK,
		"/readyz":  http.StatusOK,
		"/":        http.StatusOK,
	} {
		if response := h.do(t, http.MethodGet, path, nil, false); response.StatusCode != want {
			t.Errorf("%s = %d, want %d", path, response.StatusCode, want)
		}
	}
}

// Every error body carries the request id, which is what turns "it failed at
// 14:32" into one grep.
func TestErrorsCarryTheRequestID(t *testing.T) {
	h := newHarness(t)

	response := h.do(t, http.MethodGet, "/nosuchcode", nil, false)

	var body struct {
		Error     string `json:"error"`
		RequestID string `json:"request_id"`
	}

	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.Error != "not_found" {
		t.Errorf("error = %q, want not_found", body.Error)
	}

	if body.RequestID == "" {
		t.Error("no request id in the error body")
	}

	if response.Header.Get(httpserver.RequestIDHeader) != body.RequestID {
		t.Error("the header and the body disagree about the request id")
	}
}
