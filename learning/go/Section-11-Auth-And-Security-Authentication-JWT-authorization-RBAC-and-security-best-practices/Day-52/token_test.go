package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testKeyring(t *testing.T) *Keyring {
	t.Helper()

	return &Keyring{
		activeKID: "test",
		keys:      map[string][]byte{"test": []byte("a-test-signing-key-of-sufficient-length")},
	}
}

func testService(t *testing.T) *TokenService {
	t.Helper()

	return NewTokenService(testKeyring(t))
}

func TestIssueAndVerify(t *testing.T) {
	t.Parallel()

	service := testService(t)

	token, issued, err := service.Issue(42, "ada@example.com", []string{"member", "admin"}, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	claims, err := service.Verify(token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	if claims.Subject != "42" || claims.Email != "ada@example.com" {
		t.Fatalf("claims = %+v", claims)
	}

	if !claims.HasRole("admin") || claims.HasRole("root") {
		t.Fatalf("roles = %v", claims.Roles)
	}

	if claims.ID != issued.ID {
		t.Fatalf("jti = %q, want %q", claims.ID, issued.ID)
	}

	if claims.Issuer != issuer || len(claims.Audience) == 0 || claims.Audience[0] != audience {
		t.Fatalf("issuer/audience = %q / %v", claims.Issuer, claims.Audience)
	}
}

func TestVerifyRejects(t *testing.T) {
	t.Parallel()

	service := testService(t)

	valid, _, err := service.Issue(1, "ada@example.com", []string{"member"}, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	parts := strings.Split(valid, ".")

	claimsJSON := func(body string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(body))
	}

	future := time.Now().Add(time.Hour).Unix()

	tests := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"not a jwt", "hello.world"},
		{"tampered payload", parts[0] + "." + claimsJSON(`{"sub":"1","roles":["admin"]}`) + "." + parts[2]},
		{
			"alg none",
			base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT","kid":"test"}`)) +
				"." + claimsJSON(`{"iss":"`+issuer+`","aud":["`+audience+`"],"sub":"1"}`) + ".",
		},
		{"missing signature", parts[0] + "." + parts[1] + "."},
		{"truncated signature", parts[0] + "." + parts[1] + "." + parts[2][:10]},
		{
			"wrong issuer",
			signWith(t, "test", []byte("a-test-signing-key-of-sufficient-length"), Claims{
				RegisteredClaims: jwt.RegisteredClaims{
					Issuer:    "someone-else",
					Subject:   "1",
					Audience:  jwt.ClaimStrings{audience},
					ExpiresAt: jwt.NewNumericDate(time.Unix(future, 0)),
				},
			}),
		},
		{
			"wrong audience",
			signWith(t, "test", []byte("a-test-signing-key-of-sufficient-length"), Claims{
				RegisteredClaims: jwt.RegisteredClaims{
					Issuer:    issuer,
					Subject:   "1",
					Audience:  jwt.ClaimStrings{"another-api"},
					ExpiresAt: jwt.NewNumericDate(time.Unix(future, 0)),
				},
			}),
		},
		{
			"no expiry",
			signWith(t, "test", []byte("a-test-signing-key-of-sufficient-length"), Claims{
				RegisteredClaims: jwt.RegisteredClaims{
					Issuer:   issuer,
					Subject:  "1",
					Audience: jwt.ClaimStrings{audience},
				},
			}),
		},
		{
			"unknown key id",
			signWith(t, "rogue", []byte("a-test-signing-key-of-sufficient-length"), Claims{
				RegisteredClaims: jwt.RegisteredClaims{
					Issuer:    issuer,
					Subject:   "1",
					Audience:  jwt.ClaimStrings{audience},
					ExpiresAt: jwt.NewNumericDate(time.Unix(future, 0)),
				},
			}),
		},
		{
			"foreign signing key",
			signWith(t, "test", []byte("an-attacker-controlled-secret-key-here!"), Claims{
				RegisteredClaims: jwt.RegisteredClaims{
					Issuer:    issuer,
					Subject:   "1",
					Audience:  jwt.ClaimStrings{audience},
					ExpiresAt: jwt.NewNumericDate(time.Unix(future, 0)),
				},
			}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Verify(test.token); err == nil {
				t.Fatal("token was accepted")
			}
		})
	}
}

func signWith(t *testing.T, kid string, key []byte, claims Claims) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token.Header["kid"] = kid

	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	return signed
}

func TestExpiredTokenIsReportedAsExpired(t *testing.T) {
	t.Parallel()

	service := testService(t)

	// Well past the 30s leeway.
	token, _, err := service.Issue(1, "ada@example.com", nil, -5*time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if _, err := service.Verify(token); !errors.Is(err, ErrExpiredToken) {
		t.Fatalf("err = %v, want ErrExpiredToken", err)
	}
}

func TestRevocation(t *testing.T) {
	t.Parallel()

	service := testService(t)

	token, claims, err := service.Issue(1, "ada@example.com", nil, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if _, err := service.Verify(token); err != nil {
		t.Fatalf("verify before revoke: %v", err)
	}

	service.Revoke(claims)

	if _, err := service.Verify(token); !errors.Is(err, ErrRevoked) {
		t.Fatalf("err = %v, want ErrRevoked", err)
	}
}

func TestPurgeRevokedDropsExpiredEntries(t *testing.T) {
	t.Parallel()

	service := testService(t)

	_, expiredClaims, err := service.Issue(1, "ada@example.com", nil, -time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	_, liveClaims, err := service.Issue(1, "ada@example.com", nil, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	service.Revoke(expiredClaims)
	service.Revoke(liveClaims)

	if removed := service.PurgeRevoked(); removed != 1 {
		t.Fatalf("purged %d entries, want 1", removed)
	}

	if !service.IsRevoked(liveClaims.ID) {
		t.Fatal("purge dropped a still-valid revocation")
	}
}

// TestKeyRotation is the reason tokens carry a kid: after rotating, freshly
// issued tokens use the new key while tokens signed with the old one keep
// working until they expire.
func TestKeyRotation(t *testing.T) {
	t.Parallel()

	oldKeyring := &Keyring{
		activeKID: "v1",
		keys:      map[string][]byte{"v1": []byte("the-old-signing-key-long-enough-here")},
	}

	oldService := NewTokenService(oldKeyring)

	oldToken, _, err := oldService.Issue(1, "ada@example.com", nil, time.Hour)
	if err != nil {
		t.Fatalf("issue with old key: %v", err)
	}

	// After rotation: v2 is active, v1 still accepted.
	rotated := NewTokenService(&Keyring{
		activeKID: "v2",
		keys: map[string][]byte{
			"v2": []byte("the-new-signing-key-long-enough-here"),
			"v1": []byte("the-old-signing-key-long-enough-here"),
		},
	})

	if _, err := rotated.Verify(oldToken); err != nil {
		t.Fatalf("old token rejected during rotation: %v", err)
	}

	newToken, _, err := rotated.Issue(1, "ada@example.com", nil, time.Hour)
	if err != nil {
		t.Fatalf("issue with new key: %v", err)
	}

	// Once v1 is retired, only new tokens survive.
	retired := NewTokenService(&Keyring{
		activeKID: "v2",
		keys:      map[string][]byte{"v2": []byte("the-new-signing-key-long-enough-here")},
	})

	if _, err := retired.Verify(newToken); err != nil {
		t.Fatalf("new token rejected after retiring v1: %v", err)
	}

	if _, err := retired.Verify(oldToken); err == nil {
		t.Fatal("token signed with the retired key is still accepted")
	}
}

//
// REFRESH TOKENS
//

func TestRefreshRotation(t *testing.T) {
	t.Parallel()

	store := NewRefreshStore()

	token, err := store.Issue(7, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	userID, err := store.Redeem(token)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}

	if userID != 7 {
		t.Fatalf("user id = %d, want 7", userID)
	}

	// Replay must fail, and must take every session of that user with it.
	if _, err := store.Redeem(token); !errors.Is(err, ErrRevoked) {
		t.Fatalf("replay err = %v, want ErrRevoked", err)
	}
}

func TestRefreshReplayRevokesTheFamily(t *testing.T) {
	t.Parallel()

	store := NewRefreshStore()

	first, err := store.Issue(9, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if _, err := store.Redeem(first); err != nil {
		t.Fatalf("redeem: %v", err)
	}

	rotated, err := store.Issue(9, time.Hour)
	if err != nil {
		t.Fatalf("issue rotated: %v", err)
	}

	// The attacker replays the stolen (already used) token...
	if _, err := store.Redeem(first); !errors.Is(err, ErrRevoked) {
		t.Fatalf("replay err = %v, want ErrRevoked", err)
	}

	// ...which also invalidates the legitimate user's current token.
	if _, err := store.Redeem(rotated); err == nil {
		t.Fatal("the rotated token survived a detected replay")
	}
}

func TestExpiredRefreshToken(t *testing.T) {
	t.Parallel()

	store := NewRefreshStore()

	token, err := store.Issue(3, -time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if _, err := store.Redeem(token); !errors.Is(err, ErrExpiredToken) {
		t.Fatalf("err = %v, want ErrExpiredToken", err)
	}
}

//
// HTTP LEVEL
//

func newTestServer(t *testing.T) (*httptest.Server, *API) {
	t.Helper()

	users, err := NewUserStore()
	if err != nil {
		t.Fatalf("seed users: %v", err)
	}

	api := &API{
		users:    users,
		tokens:   testService(t),
		refresh:  NewRefreshStore(),
		accessTL: time.Hour,
	}

	server := httptest.NewServer(api.Routes())

	t.Cleanup(server.Close)

	return server, api
}

func post(t *testing.T, server *httptest.Server, path string, body any, token string) (int, map[string]any) {
	t.Helper()

	payload := bytes.NewReader(nil)

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+path, payload)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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

	decoded := map[string]any{}

	if resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}

	return resp.StatusCode, decoded
}

func TestLoginRefreshLogoutFlow(t *testing.T) {
	t.Parallel()

	server, _ := newTestServer(t)

	status, body := post(t, server, "/auth/login",
		map[string]string{"email": "ada@example.com", "password": "correct-horse-7"}, "")

	if status != http.StatusOK {
		t.Fatalf("login = %d (%v)", status, body)
	}

	access, _ := body["access_token"].(string)
	refresh, _ := body["refresh_token"].(string)

	if access == "" || refresh == "" {
		t.Fatalf("missing tokens in %v", body)
	}

	// Protected route works.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/me", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+access)

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("me: %v", err)
	}

	if err := resp.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me = %d", resp.StatusCode)
	}

	// Refresh returns a new pair.
	status, body = post(t, server, "/auth/refresh", map[string]string{"refresh_token": refresh}, "")

	if status != http.StatusOK {
		t.Fatalf("refresh = %d (%v)", status, body)
	}

	if body["refresh_token"] == refresh {
		t.Fatal("refresh token was not rotated")
	}

	// The consumed refresh token must not work again.
	if status, _ := post(t, server, "/auth/refresh", map[string]string{"refresh_token": refresh}, ""); status != http.StatusUnauthorized {
		t.Fatalf("replayed refresh = %d, want 401", status)
	}

	// Logout revokes the access token immediately.
	if status, _ := post(t, server, "/auth/logout", nil, access); status != http.StatusNoContent {
		t.Fatalf("logout = %d", status)
	}

	req, err = http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/me", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+access)

	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("me after logout: %v", err)
	}

	if err := resp.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me after logout = %d, want 401", resp.StatusCode)
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	t.Parallel()

	server, _ := newTestServer(t)

	tests := []map[string]string{
		{"email": "ada@example.com", "password": "wrong-horse-7"},
		{"email": "nobody@example.com", "password": "correct-horse-7"},
		{"email": "", "password": ""},
	}

	for _, body := range tests {
		if status, _ := post(t, server, "/auth/login", body, ""); status != http.StatusUnauthorized {
			t.Fatalf("login %v = %d, want 401", body, status)
		}
	}
}

func TestKeyringRequiresLongSecret(t *testing.T) {
	t.Setenv("JWT_SIGNING_KEY", "too-short")

	if _, err := LoadKeyring(); err == nil {
		t.Fatal("a short signing key was accepted")
	}
}

func TestKeyringParsesPreviousKeys(t *testing.T) {
	t.Setenv("JWT_SIGNING_KEY", "the-new-signing-key-long-enough-here!")
	t.Setenv("JWT_SIGNING_KEY_ID", "v2")
	t.Setenv("JWT_PREVIOUS_KEYS", "v1:the-old-signing-key-long-enough-here")

	keyring, err := LoadKeyring()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if keyring.activeKID != "v2" || len(keyring.keys) != 2 {
		t.Fatalf("keyring = %+v", keyring)
	}
}
