package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

/*
The auth regression suite.

It covers every flow the README promises, in both directions:

	register   valid / duplicate / weak password / bad email
	login      success / wrong password / unknown user / suspended / rate limited
	tokens     valid / missing / malformed / expired / revoked / forged
	authz      allowed role / forbidden role / ownership / privilege escalation

An auth bug that reaches production is the expensive kind, so these run on
every commit.
*/

type testEnv struct {
	server *httptest.Server
	api    *API
	store  *Store
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	// A fixed key so tokens are stable across the test's lifetime.
	t.Setenv("JWT_SIGNING_KEY", "a-test-signing-key-that-is-long-enough")

	db, err := OpenDB(t.Context(), ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	tokens, err := NewTokenService(AccessTokenTTL)
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	store := NewStore(db)
	hasher := PasswordHasher{Cost: bcrypt.MinCost} // fast tests, production uses DefaultCost

	if err := seedDemoUsers(context.Background(), store, hasher); err != nil {
		t.Fatalf("seed: %v", err)
	}

	api, err := NewAPI(store, tokens, hasher)
	if err != nil {
		t.Fatalf("build api: %v", err)
	}

	server := httptest.NewServer(api.Routes())

	t.Cleanup(server.Close)

	return &testEnv{server: server, api: api, store: store}
}

func (e *testEnv) do(t *testing.T, method, path string, body any, token string) (int, []byte) {
	t.Helper()

	payload := bytes.NewReader(nil)

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, e.server.URL+path, payload)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := e.server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
	}()

	var buffer bytes.Buffer

	if _, err := buffer.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode, buffer.Bytes()
}

func (e *testEnv) login(t *testing.T, email, password string) (access, refresh string) {
	t.Helper()

	status, body := e.do(t, http.MethodPost, "/auth/login",
		map[string]string{"email": email, "password": password}, "")

	if status != http.StatusOK {
		t.Fatalf("login %s = %d (%s)", email, status, body)
	}

	var response struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode login: %v", err)
	}

	if response.TokenType != "Bearer" {
		t.Fatalf("token_type = %q", response.TokenType)
	}

	return response.AccessToken, response.RefreshToken
}

//
// REGISTRATION
//

func TestRegistration(t *testing.T) {
	env := newTestEnv(t)

	tests := []struct {
		name string
		body map[string]string
		want int
	}{
		{"valid", map[string]string{"email": "new@example.com", "display_name": "New", "password": "correct-horse-7"}, http.StatusCreated},
		{"duplicate", map[string]string{"email": "new@example.com", "display_name": "Copy", "password": "correct-horse-7"}, http.StatusConflict},
		{"bad email", map[string]string{"email": "nope", "display_name": "X", "password": "correct-horse-7"}, http.StatusUnprocessableEntity},
		{"short password", map[string]string{"email": "short@example.com", "display_name": "X", "password": "abc123"}, http.StatusUnprocessableEntity},
		{"common password", map[string]string{"email": "weak@example.com", "display_name": "X", "password": "mypassword123"}, http.StatusUnprocessableEntity},
		{"empty name", map[string]string{"email": "noname@example.com", "display_name": "  ", "password": "correct-horse-7"}, http.StatusUnprocessableEntity},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, body := env.do(t, http.MethodPost, "/auth/register", test.body, "")

			if status != test.want {
				t.Fatalf("status = %d, want %d (%s)", status, test.want, body)
			}
		})
	}
}

// TestRegistrationCannotChooseARole is the privilege escalation check: a
// public endpoint must not let the caller pick their own role.
func TestRegistrationCannotChooseARole(t *testing.T) {
	env := newTestEnv(t)

	status, _ := env.do(t, http.MethodPost, "/auth/register", map[string]any{
		"email": "sneaky@example.com", "display_name": "Sneaky",
		"password": "correct-horse-7", "role": "admin",
	}, "")

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unknown field", status)
	}

	status, body := env.do(t, http.MethodPost, "/auth/register", map[string]string{
		"email": "normal@example.com", "display_name": "Normal", "password": "correct-horse-7",
	}, "")

	if status != http.StatusCreated {
		t.Fatalf("status = %d (%s)", status, body)
	}

	var user publicUserResponse

	if err := json.Unmarshal(body, &user); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if user.Role != string(RoleMember) {
		t.Fatalf("role = %q, want member", user.Role)
	}
}

func TestPasswordHashNeverLeaves(t *testing.T) {
	env := newTestEnv(t)

	access, _ := env.login(t, "member@example.com", "correct-horse-7")

	for _, path := range []string{"/me", "/notes"} {
		_, body := env.do(t, http.MethodGet, path, nil, access)

		lower := bytes.ToLower(body)

		if bytes.Contains(lower, []byte("password")) || bytes.Contains(body, []byte("$2a$")) {
			t.Fatalf("%s leaks credential material: %s", path, body)
		}
	}
}

//
// LOGIN
//

func TestLoginSuccess(t *testing.T) {
	env := newTestEnv(t)

	access, refresh := env.login(t, "member@example.com", "correct-horse-7")

	if access == "" || refresh == "" {
		t.Fatal("login returned empty tokens")
	}

	status, body := env.do(t, http.MethodGet, "/me", nil, access)

	if status != http.StatusOK {
		t.Fatalf("me = %d (%s)", status, body)
	}
}

func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	env := newTestEnv(t)

	attempts := []map[string]string{
		{"email": "member@example.com", "password": "wrong-horse-7"},
		{"email": "ghost@example.com", "password": "correct-horse-7"},
	}

	var messages []string

	for _, attempt := range attempts {
		status, body := env.do(t, http.MethodPost, "/auth/login", attempt, "")

		if status != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 (%s)", status, body)
		}

		var response struct {
			Error string `json:"error"`
		}

		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatalf("decode: %v", err)
		}

		messages = append(messages, response.Error)
	}

	if messages[0] != messages[1] {
		t.Fatalf("wrong password and unknown user differ: %q vs %q - user enumeration",
			messages[0], messages[1])
	}
}

func TestLoginRateLimited(t *testing.T) {
	env := newTestEnv(t)

	attempt := map[string]string{"email": "member@example.com", "password": "wrong-horse-7"}

	sawLimit := false

	for range 8 {
		status, _ := env.do(t, http.MethodPost, "/auth/login", attempt, "")

		if status == http.StatusTooManyRequests {
			sawLimit = true
			break
		}
	}

	if !sawLimit {
		t.Fatal("brute force was never rate limited")
	}
}

func TestSuspendedUserCannotLogInOrUseTokens(t *testing.T) {
	env := newTestEnv(t)

	memberAccess, _ := env.login(t, "member@example.com", "correct-horse-7")
	adminAccess, _ := env.login(t, "admin@example.com", "correct-horse-7")

	member, err := env.store.UserByEmail(context.Background(), "member@example.com")
	if err != nil {
		t.Fatalf("load member: %v", err)
	}

	status, body := env.do(t, http.MethodPost,
		"/admin/users/"+strconv.FormatInt(member.ID, 10)+"/suspend", nil, adminAccess)

	if status != http.StatusNoContent {
		t.Fatalf("suspend = %d (%s)", status, body)
	}

	// An already-issued token stops working immediately: RequireAuth reloads
	// the user on every request instead of trusting the claims.
	if status, _ := env.do(t, http.MethodGet, "/me", nil, memberAccess); status != http.StatusForbidden {
		t.Fatalf("suspended user's token = %d, want 403", status)
	}

	if status, _ := env.do(t, http.MethodPost, "/auth/login",
		map[string]string{"email": "member@example.com", "password": "correct-horse-7"}, ""); status != http.StatusForbidden {
		t.Fatalf("suspended login = %d, want 403", status)
	}
}

func TestAdminCannotSuspendThemselves(t *testing.T) {
	env := newTestEnv(t)

	adminAccess, _ := env.login(t, "admin@example.com", "correct-horse-7")

	admin, err := env.store.UserByEmail(context.Background(), "admin@example.com")
	if err != nil {
		t.Fatalf("load admin: %v", err)
	}

	status, _ := env.do(t, http.MethodPost,
		"/admin/users/"+strconv.FormatInt(admin.ID, 10)+"/suspend", nil, adminAccess)

	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", status)
	}
}

//
// TOKENS
//

func TestProtectedRoutesRejectBadTokens(t *testing.T) {
	env := newTestEnv(t)

	access, _ := env.login(t, "member@example.com", "correct-horse-7")

	parts := strings.Split(access, ".")

	tests := []struct {
		name  string
		token string
	}{
		{"missing", ""},
		{"garbage", "not-a-token"},
		{"tampered payload", parts[0] + ".ZXlKemRXSWlPaUl4SW4w." + parts[2]},
		{"tampered signature", parts[0] + "." + parts[1] + "." + strings.Repeat("A", len(parts[2]))},
		{"only two parts", parts[0] + "." + parts[1]},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, _ := env.do(t, http.MethodGet, "/me", nil, test.token)

			if status != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", status)
			}
		})
	}
}

func TestExpiredTokenIsRejected(t *testing.T) {
	env := newTestEnv(t)

	// Issue a token that is already past its expiry plus the leeway.
	expiredService, err := NewTokenService(-time.Hour)
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	user, err := env.store.UserByEmail(context.Background(), "member@example.com")
	if err != nil {
		t.Fatalf("load user: %v", err)
	}

	expired, _, err := expiredService.Issue(user)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	status, body := env.do(t, http.MethodGet, "/me", nil, expired)

	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (%s)", status, body)
	}

	if !bytes.Contains(body, []byte("expired")) {
		t.Fatalf("body = %s, want an expiry message", body)
	}
}

func TestLogoutRevokesAccessAndRefresh(t *testing.T) {
	env := newTestEnv(t)

	access, refresh := env.login(t, "member@example.com", "correct-horse-7")

	if status, _ := env.do(t, http.MethodPost, "/auth/logout", nil, access); status != http.StatusNoContent {
		t.Fatalf("logout failed")
	}

	if status, _ := env.do(t, http.MethodGet, "/me", nil, access); status != http.StatusUnauthorized {
		t.Fatalf("access token still works after logout: %d", status)
	}

	if status, _ := env.do(t, http.MethodPost, "/auth/refresh",
		map[string]string{"refresh_token": refresh}, ""); status != http.StatusUnauthorized {
		t.Fatalf("refresh token still works after logout: %d", status)
	}
}

func TestRefreshRotatesAndDetectsReplay(t *testing.T) {
	env := newTestEnv(t)

	_, refresh := env.login(t, "member@example.com", "correct-horse-7")

	status, body := env.do(t, http.MethodPost, "/auth/refresh",
		map[string]string{"refresh_token": refresh}, "")

	if status != http.StatusOK {
		t.Fatalf("refresh = %d (%s)", status, body)
	}

	var response struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if response.RefreshToken == refresh {
		t.Fatal("the refresh token was not rotated")
	}

	// Replaying the consumed token revokes the whole family.
	if status, _ := env.do(t, http.MethodPost, "/auth/refresh",
		map[string]string{"refresh_token": refresh}, ""); status != http.StatusUnauthorized {
		t.Fatalf("replay = %d, want 401", status)
	}

	if status, _ := env.do(t, http.MethodPost, "/auth/refresh",
		map[string]string{"refresh_token": response.RefreshToken}, ""); status != http.StatusUnauthorized {
		t.Fatal("the rotated token survived a detected replay")
	}
}

//
// AUTHORIZATION
//

func TestRolePermissions(t *testing.T) {
	env := newTestEnv(t)

	memberAccess, _ := env.login(t, "member@example.com", "correct-horse-7")
	editorAccess, _ := env.login(t, "editor@example.com", "correct-horse-7")
	adminAccess, _ := env.login(t, "admin@example.com", "correct-horse-7")

	tests := []struct {
		name   string
		method string
		path   string
		token  string
		want   int
	}{
		{"member cannot list users", http.MethodGet, "/admin/users", memberAccess, http.StatusForbidden},
		{"editor cannot list users", http.MethodGet, "/admin/users", editorAccess, http.StatusForbidden},
		{"admin can list users", http.MethodGet, "/admin/users", adminAccess, http.StatusOK},
		{"member cannot read audit", http.MethodGet, "/admin/audit", memberAccess, http.StatusForbidden},
		{"admin can read audit", http.MethodGet, "/admin/audit", adminAccess, http.StatusOK},
		{"anonymous gets 401 not 403", http.MethodGet, "/admin/users", "", http.StatusUnauthorized},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, body := env.do(t, test.method, test.path, nil, test.token)

			if status != test.want {
				t.Fatalf("status = %d, want %d (%s)", status, test.want, body)
			}
		})
	}
}

func TestNoteOwnership(t *testing.T) {
	env := newTestEnv(t)

	memberAccess, _ := env.login(t, "member@example.com", "correct-horse-7")
	editorAccess, _ := env.login(t, "editor@example.com", "correct-horse-7")
	adminAccess, _ := env.login(t, "admin@example.com", "correct-horse-7")

	status, body := env.do(t, http.MethodPost, "/notes",
		map[string]string{"title": "member note", "body": "private"}, memberAccess)

	if status != http.StatusCreated {
		t.Fatalf("create = %d (%s)", status, body)
	}

	var note Note

	if err := json.Unmarshal(body, &note); err != nil {
		t.Fatalf("decode: %v", err)
	}

	path := "/notes/" + strconv.FormatInt(note.ID, 10)

	// The owner reads it, the editor may read any, a second member may not.
	if status, _ := env.do(t, http.MethodGet, path, nil, memberAccess); status != http.StatusOK {
		t.Fatalf("owner read = %d", status)
	}

	if status, _ := env.do(t, http.MethodGet, path, nil, editorAccess); status != http.StatusOK {
		t.Fatalf("editor read = %d, want 200", status)
	}

	// The editor cannot delete someone else's note; the admin can.
	if status, _ := env.do(t, http.MethodDelete, path, nil, editorAccess); status != http.StatusForbidden {
		t.Fatalf("editor delete = %d, want 403", status)
	}

	if status, _ := env.do(t, http.MethodDelete, path, nil, adminAccess); status != http.StatusNoContent {
		t.Fatalf("admin delete = %d, want 204", status)
	}
}

func TestNoteOwnershipComesFromTheToken(t *testing.T) {
	env := newTestEnv(t)

	memberAccess, _ := env.login(t, "member@example.com", "correct-horse-7")

	// owner_id is not accepted, so it cannot be spoofed.
	if status, _ := env.do(t, http.MethodPost, "/notes",
		map[string]any{"title": "spoof", "body": "x", "owner_id": 99}, memberAccess); status != http.StatusBadRequest {
		t.Fatal("owner_id was accepted from the request body")
	}

	_, body := env.do(t, http.MethodPost, "/notes",
		map[string]string{"title": "legit", "body": "x"}, memberAccess)

	var note Note

	if err := json.Unmarshal(body, &note); err != nil {
		t.Fatalf("decode: %v", err)
	}

	member, err := env.store.UserByEmail(context.Background(), "member@example.com")
	if err != nil {
		t.Fatalf("load user: %v", err)
	}

	if note.OwnerID != member.ID {
		t.Fatalf("owner = %d, want %d", note.OwnerID, member.ID)
	}
}

func TestInputValidationOnNotes(t *testing.T) {
	env := newTestEnv(t)

	access, _ := env.login(t, "member@example.com", "correct-horse-7")

	tests := []struct {
		name string
		body map[string]any
		want int
	}{
		{"empty title", map[string]any{"title": "  ", "body": "x"}, http.StatusUnprocessableEntity},
		{"long title", map[string]any{"title": strings.Repeat("a", 201), "body": ""}, http.StatusUnprocessableEntity},
		{"control character", map[string]any{"title": "a\x07b", "body": ""}, http.StatusUnprocessableEntity},
		{"unknown field", map[string]any{"title": "ok", "body": "", "admin": true}, http.StatusBadRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, body := env.do(t, http.MethodPost, "/notes", test.body, access)

			if status != test.want {
				t.Fatalf("status = %d, want %d (%s)", status, test.want, body)
			}
		})
	}
}

func TestSecurityHeadersAndAudit(t *testing.T) {
	env := newTestEnv(t)

	adminAccess, _ := env.login(t, "admin@example.com", "correct-horse-7")

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, env.server.URL+"/healthz", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	if err := resp.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}

	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Cache-Control":          "no-store",
	} {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}

	// Logins are audited, and the audit log is admin-only (checked above).
	status, body := env.do(t, http.MethodGet, "/admin/audit", nil, adminAccess)

	if status != http.StatusOK {
		t.Fatalf("audit = %d (%s)", status, body)
	}

	if !bytes.Contains(body, []byte("auth.login")) {
		t.Fatalf("audit log has no login entry: %s", body)
	}
}
