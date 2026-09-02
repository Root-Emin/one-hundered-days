package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

/*
Auth flow tests: success, wrong password, unknown user, missing token,
malformed token, expired session, and logout.

Every one of these is a regression that would be embarrassing in production,
which is exactly why they are written down.
*/

func newTestAPI(t *testing.T, ttl time.Duration) *httptest.Server {
	t.Helper()

	db, err := OpenDB(t.Context(), ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	// MinCost keeps the suite fast; production uses DefaultCost.
	api, err := NewAPI(NewStore(db), BcryptHasher{Cost: bcrypt.MinCost}, ttl)
	if err != nil {
		t.Fatalf("build api: %v", err)
	}

	server := httptest.NewServer(api.Routes())

	t.Cleanup(server.Close)

	return server
}

func request(t *testing.T, server *httptest.Server, method, path string, body any, token string) (int, []byte) {
	t.Helper()

	payload := bytes.NewReader(nil)

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}

		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, payload)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := server.Client().Do(req)
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

func registerAndLogin(t *testing.T, server *httptest.Server, email, password string) string {
	t.Helper()

	status, body := request(t, server, http.MethodPost, "/auth/register", map[string]string{
		"email":        email,
		"display_name": "Test User",
		"password":     password,
	}, "")

	if status != http.StatusCreated {
		t.Fatalf("register status = %d (%s)", status, body)
	}

	status, body = request(t, server, http.MethodPost, "/auth/login", map[string]string{
		"email":    email,
		"password": password,
	}, "")

	if status != http.StatusOK {
		t.Fatalf("login status = %d (%s)", status, body)
	}

	var response struct {
		Token     string `json:"token"`
		TokenType string `json:"token_type"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode login response: %v", err)
	}

	if response.TokenType != "Bearer" {
		t.Fatalf("token_type = %q, want Bearer", response.TokenType)
	}

	return response.Token
}

func TestRegisterLoginAndProtectedRoute(t *testing.T) {
	t.Parallel()

	server := newTestAPI(t, time.Hour)
	token := registerAndLogin(t, server, "ada@example.com", "correct-horse-7")

	status, body := request(t, server, http.MethodGet, "/me", nil, token)

	if status != http.StatusOK {
		t.Fatalf("me status = %d (%s)", status, body)
	}

	var user publicUserResponse

	if err := json.Unmarshal(body, &user); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if user.Email != "ada@example.com" {
		t.Fatalf("email = %q", user.Email)
	}

	// The response must not carry anything password-shaped.
	if bytes.Contains(bytes.ToLower(body), []byte("password")) ||
		bytes.Contains(body, []byte("$2a$")) {
		t.Fatalf("response leaks credential material: %s", body)
	}
}

func TestRegistrationRules(t *testing.T) {
	t.Parallel()

	server := newTestAPI(t, time.Hour)

	tests := []struct {
		name string
		body map[string]string
		want int
	}{
		{
			"valid",
			map[string]string{"email": "new@example.com", "display_name": "New", "password": "correct-horse-7"},
			http.StatusCreated,
		},
		{
			"duplicate email",
			map[string]string{"email": "new@example.com", "display_name": "Copy", "password": "correct-horse-7"},
			http.StatusConflict,
		},
		{
			"invalid email",
			map[string]string{"email": "nope", "display_name": "X", "password": "correct-horse-7"},
			http.StatusUnprocessableEntity,
		},
		{
			"short password",
			map[string]string{"email": "short@example.com", "display_name": "X", "password": "abc12"},
			http.StatusUnprocessableEntity,
		},
		{
			"no digits",
			map[string]string{"email": "letters@example.com", "display_name": "X", "password": "onlylettershere"},
			http.StatusUnprocessableEntity,
		},
		{
			"well-known password",
			map[string]string{"email": "weak@example.com", "display_name": "X", "password": "mypassword123"},
			http.StatusUnprocessableEntity,
		},
		{
			"missing display name",
			map[string]string{"email": "noname@example.com", "display_name": " ", "password": "correct-horse-7"},
			http.StatusUnprocessableEntity,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, body := request(t, server, http.MethodPost, "/auth/register", test.body, "")

			if status != test.want {
				t.Fatalf("status = %d, want %d (%s)", status, test.want, body)
			}
		})
	}
}

func TestLoginFailures(t *testing.T) {
	t.Parallel()

	server := newTestAPI(t, time.Hour)
	registerAndLogin(t, server, "grace@example.com", "correct-horse-7")

	tests := []struct {
		name  string
		email string
		pass  string
	}{
		{"wrong password", "grace@example.com", "wrong-horse-7"},
		{"unknown user", "nobody@example.com", "correct-horse-7"},
		{"empty password", "grace@example.com", ""},
	}

	var messages []string

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, body := request(t, server, http.MethodPost, "/auth/login",
				map[string]string{"email": test.email, "password": test.pass}, "")

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
		})
	}

	// All three failures must be indistinguishable: a different message for
	// "unknown user" is a user-enumeration oracle.
	for _, message := range messages {
		if message != messages[0] {
			t.Fatalf("login failures return different messages: %q vs %q", message, messages[0])
		}
	}
}

func TestProtectedRouteRejectsBadTokens(t *testing.T) {
	t.Parallel()

	server := newTestAPI(t, time.Hour)
	valid := registerAndLogin(t, server, "protected@example.com", "correct-horse-7")

	tests := []struct {
		name   string
		header string
	}{
		{"no header", ""},
		{"garbage token", "Bearer not-a-real-token"},
		{"empty bearer", "Bearer "},
		{"wrong scheme", "Basic " + valid},
		{"raw token without scheme", valid},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/me", nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}

			if test.header != "" {
				req.Header.Set("Authorization", test.header)
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

			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", resp.StatusCode)
			}

			if challenge := resp.Header.Get("WWW-Authenticate"); !strings.Contains(challenge, "Bearer") {
				t.Fatalf("WWW-Authenticate = %q, want a Bearer challenge", challenge)
			}
		})
	}
}

func TestExpiredSessionIsRejected(t *testing.T) {
	t.Parallel()

	// A session that is already over by the time it is issued.
	server := newTestAPI(t, -time.Minute)

	status, body := request(t, server, http.MethodPost, "/auth/register", map[string]string{
		"email": "expired@example.com", "display_name": "Exp", "password": "correct-horse-7",
	}, "")

	if status != http.StatusCreated {
		t.Fatalf("register = %d (%s)", status, body)
	}

	status, body = request(t, server, http.MethodPost, "/auth/login", map[string]string{
		"email": "expired@example.com", "password": "correct-horse-7",
	}, "")

	if status != http.StatusOK {
		t.Fatalf("login = %d (%s)", status, body)
	}

	var response struct {
		Token string `json:"token"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if status, body := request(t, server, http.MethodGet, "/me", nil, response.Token); status != http.StatusUnauthorized {
		t.Fatalf("expired token status = %d, want 401 (%s)", status, body)
	}
}

func TestLogoutRevokesTheToken(t *testing.T) {
	t.Parallel()

	server := newTestAPI(t, time.Hour)
	token := registerAndLogin(t, server, "logout@example.com", "correct-horse-7")

	if status, body := request(t, server, http.MethodPost, "/auth/logout", nil, token); status != http.StatusNoContent {
		t.Fatalf("logout = %d (%s)", status, body)
	}

	if status, _ := request(t, server, http.MethodGet, "/me", nil, token); status != http.StatusUnauthorized {
		t.Fatalf("token still works after logout: status = %d", status)
	}
}

func TestLogoutEverywhere(t *testing.T) {
	t.Parallel()

	server := newTestAPI(t, time.Hour)

	first := registerAndLogin(t, server, "many@example.com", "correct-horse-7")

	// A second login for the same user: two live sessions.
	_, body := request(t, server, http.MethodPost, "/auth/login", map[string]string{
		"email": "many@example.com", "password": "correct-horse-7",
	}, "")

	var response struct {
		Token string `json:"token"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode: %v", err)
	}

	second := response.Token

	if status, _ := request(t, server, http.MethodPost, "/auth/logout-everywhere", nil, first); status != http.StatusNoContent {
		t.Fatalf("logout everywhere failed: %d", status)
	}

	for name, token := range map[string]string{"first": first, "second": second} {
		if status, _ := request(t, server, http.MethodGet, "/me", nil, token); status != http.StatusUnauthorized {
			t.Fatalf("%s session still valid: status = %d", name, status)
		}
	}
}

//
// UNIT TESTS FOR THE HASHERS
//

func TestHashersRoundTrip(t *testing.T) {
	t.Parallel()

	hashers := map[string]Hasher{
		"bcrypt":   BcryptHasher{Cost: bcrypt.MinCost},
		"argon2id": Argon2Hasher{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32, SaltLen: 16},
	}

	for name, hasher := range hashers {
		t.Run(name, func(t *testing.T) {
			const password = "correct-horse-battery-7"

			hash, err := hasher.Hash(password)
			if err != nil {
				t.Fatalf("hash: %v", err)
			}

			if strings.Contains(hash, password) {
				t.Fatal("the hash contains the plaintext password")
			}

			if err := hasher.Verify(password, hash); err != nil {
				t.Fatalf("verify correct password: %v", err)
			}

			if err := hasher.Verify("wrong-password-1", hash); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("verify wrong password err = %v, want ErrInvalidCredentials", err)
			}

			// Same password, different salt: the stored values must differ.
			second, err := hasher.Hash(password)
			if err != nil {
				t.Fatalf("hash again: %v", err)
			}

			if hash == second {
				t.Fatal("two hashes of the same password are identical: the salt is not random")
			}
		})
	}
}

func TestArgon2RejectsTamperedHash(t *testing.T) {
	t.Parallel()

	hasher := Argon2Hasher{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32, SaltLen: 16}

	hash, err := hasher.Hash("correct-horse-7")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	for _, tampered := range []string{
		"not-a-hash",
		"$argon2i$v=19$m=8192,t=1,p=1$c2FsdA$ZGlnZXN0", // wrong variant
		strings.Replace(hash, "$argon2id$", "$argon2d$", 1),
		hash[:len(hash)-4],
	} {
		if err := hasher.Verify("correct-horse-7", tampered); err == nil {
			t.Fatalf("tampered hash accepted: %s", tampered)
		}
	}
}

func TestSessionTokensAreStoredHashed(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	store := NewStore(db)

	user, err := store.CreateUser(context.Background(), "hash@example.com", "Hashed", "irrelevant")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	token, _, err := store.CreateSession(context.Background(), user.ID, time.Hour)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	var stored string

	if err := db.QueryRowContext(context.Background(),
		`SELECT token_hash FROM sessions LIMIT 1;`).Scan(&stored); err != nil {
		t.Fatalf("read session row: %v", err)
	}

	if stored == token {
		t.Fatal("the raw session token is stored in the database")
	}

	if stored != hashToken(token) {
		t.Fatal("stored value is not the token hash")
	}

	// The hash still authenticates the original token.
	if _, err := store.SessionByToken(context.Background(), token); err != nil {
		t.Fatalf("session lookup: %v", err)
	}
}
