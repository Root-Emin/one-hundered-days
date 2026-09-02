package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

/*
Day 51 - Auth & Security: Authentication Basics

Tasks covered:

 1. Passwords hashed with bcrypt or argon2id, never stored in plaintext
 2. Registration: unique email, password policy, hashed storage
 3. Login: credentials verified, an opaque session token issued
 4. Protected routes: 401 when the token is missing, wrong or expired

Files:

	password.go  hashing, verification, password policy
	store.go     users and sessions (token hashes, not tokens)
	main.go      HTTP API and middleware
	auth_test.go the flows: success, wrong password, no token, expired token

Run:

	go run .                       # HTTP server on :8080
	HASHER=argon2id go run .

Environment variables:

	PORT           HTTP port.                 Default: 8080
	DB_PATH        SQLite path.               Default: :memory:
	HASHER         bcrypt | argon2id.         Default: bcrypt
	SESSION_TTL    Session lifetime.          Default: 24h

Try it:

	curl -XPOST localhost:8080/auth/register \
	  -d '{"email":"ada@example.com","display_name":"Ada","password":"correct-horse-7"}'

	curl -XPOST localhost:8080/auth/login \
	  -d '{"email":"ada@example.com","password":"correct-horse-7"}'

	curl localhost:8080/me -H "Authorization: Bearer <token>"
	curl -XPOST localhost:8080/auth/logout -H "Authorization: Bearer <token>"

Test:

	go test ./...
*/

type API struct {
	store      *Store
	hasher     Hasher
	sessionTTL time.Duration

	// dummyHash is verified against when no user matches, so a login attempt
	// for an unknown email costs the same time as one for a known email. Skip
	// this and the response time becomes a user-enumeration oracle.
	dummyHash string
}

func NewAPI(store *Store, hasher Hasher, sessionTTL time.Duration) (*API, error) {
	dummy, err := hasher.Hash("this password exists only to burn the same CPU time")
	if err != nil {
		return nil, fmt.Errorf("prepare dummy hash: %w", err)
	}

	return &API{
		store:      store,
		hasher:     hasher,
		sessionTTL: sessionTTL,
		dummyHash:  dummy,
	}, nil
}

func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	// Public.
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("POST /auth/register", a.register)
	mux.HandleFunc("POST /auth/login", a.login)

	// Protected: the middleware wraps only these, so the public routes stay
	// reachable and the protected ones cannot be forgotten.
	mux.Handle("GET /me", a.RequireAuth(http.HandlerFunc(a.me)))
	mux.Handle("POST /auth/logout", a.RequireAuth(http.HandlerFunc(a.logout)))
	mux.Handle("POST /auth/logout-everywhere", a.RequireAuth(http.HandlerFunc(a.logoutEverywhere)))

	return logging(mux)
}

//
// CONTEXT
//

type contextKey string

const (
	userContextKey  contextKey = "user"
	tokenContextKey contextKey = "session_token"
)

func userFrom(ctx context.Context) (User, bool) {
	user, ok := ctx.Value(userContextKey).(User)

	return user, ok
}

func tokenFrom(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(tokenContextKey).(string)

	return token, ok
}

//
// MIDDLEWARE
//

// RequireAuth rejects anything without a valid session with 401 - never 500,
// never a redirect, and never a message that says which part was wrong.
func (a *API) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			unauthorized(w, "missing bearer token")
			return
		}

		session, err := a.store.SessionByToken(r.Context(), token)
		if err != nil {
			if errors.Is(err, ErrNoSession) {
				unauthorized(w, "invalid or expired token")
				return
			}

			log.Printf("session lookup failed: %v", err)
			writeError(w, http.StatusInternalServerError, "internal server error")

			return
		}

		user, err := a.store.UserByID(r.Context(), session.UserID)
		if err != nil {
			// The session outlived its user: treat it as no session at all.
			if errors.Is(err, ErrUserNotFound) {
				unauthorized(w, "invalid or expired token")
				return
			}

			log.Printf("user lookup failed: %v", err)
			writeError(w, http.StatusInternalServerError, "internal server error")

			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, user)
		ctx = context.WithValue(ctx, tokenContextKey, token)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// bearerToken parses "Authorization: Bearer <token>" case-insensitively on
// the scheme, as RFC 7235 requires.
func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")

	scheme, value, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "bearer") {
		return "", false
	}

	value = strings.TrimSpace(value)

	return value, value != ""
}

func unauthorized(w http.ResponseWriter, reason string) {
	// WWW-Authenticate tells a well-behaved client what to do next.
	w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
	writeError(w, http.StatusUnauthorized, reason)
}

//
// HANDLERS
//

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) register(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	email := normalizeEmail(input.Email)

	if email == "" || !strings.Contains(email, "@") || len(email) > 254 {
		writeError(w, http.StatusUnprocessableEntity, "a valid email is required")
		return
	}

	if strings.TrimSpace(input.DisplayName) == "" {
		writeError(w, http.StatusUnprocessableEntity, "display_name is required")
		return
	}

	if err := ValidatePassword(input.Password); err != nil {
		// The policy message is safe to return: it describes the rule, not
		// the secret.
		writeError(w, http.StatusUnprocessableEntity, strings.TrimPrefix(
			err.Error(), "password does not meet the policy: "))

		return
	}

	hash, err := a.hasher.Hash(input.Password)
	if err != nil {
		log.Printf("hash password: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	user, err := a.store.CreateUser(r.Context(), email, input.DisplayName, hash)
	if err != nil {
		if errors.Is(err, ErrEmailTaken) {
			// A conscious trade-off: this endpoint admits the email exists,
			// which is hard to avoid for registration. The login endpoint
			// below gives nothing away.
			writeError(w, http.StatusConflict, "email already registered")
			return
		}

		log.Printf("create user: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	log.Printf("registered user id=%d email=%s", user.ID, user.Email)

	writeJSON(w, http.StatusCreated, publicUser(user))
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	user, err := a.store.UserByEmail(r.Context(), input.Email)

	if err != nil && !errors.Is(err, ErrUserNotFound) {
		log.Printf("login lookup: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	// Same work either way: unknown email verifies against the dummy hash.
	storedHash := a.dummyHash
	if err == nil {
		storedHash = user.passwordHash
	}

	verifyErr := a.hasher.Verify(input.Password, storedHash)

	if err != nil || verifyErr != nil {
		// One message for both cases. Anything more specific tells an
		// attacker which half of the guess was right.
		log.Printf("failed login attempt email=%s", normalizeEmail(input.Email))
		unauthorized(w, "invalid email or password")

		return
	}

	token, expiresAt, err := a.store.CreateSession(r.Context(), user.ID, a.sessionTTL)
	if err != nil {
		log.Printf("create session: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	log.Printf("login user id=%d", user.ID)

	// The token appears exactly once, in this response body. It is never
	// logged, and only its hash is stored.
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      token,
		"token_type": "Bearer",
		"expires_at": expiresAt.Format(time.RFC3339),
		"user":       publicUser(user),
	})
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	user, ok := userFrom(r.Context())
	if !ok {
		// Unreachable behind RequireAuth, but a handler must never assume.
		unauthorized(w, "missing session")
		return
	}

	writeJSON(w, http.StatusOK, publicUser(user))
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	token, ok := tokenFrom(r.Context())
	if !ok {
		unauthorized(w, "missing session")
		return
	}

	if err := a.store.DeleteSession(r.Context(), token); err != nil {
		log.Printf("logout: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (a *API) logoutEverywhere(w http.ResponseWriter, r *http.Request) {
	user, ok := userFrom(r.Context())
	if !ok {
		unauthorized(w, "missing session")
		return
	}

	if err := a.store.DeleteUserSessions(r.Context(), user.ID); err != nil {
		log.Printf("logout everywhere: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

//
// DTO
//

type publicUserResponse struct {
	ID          int64  `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

// publicUser is the only shape a user ever leaves the process in. There is no
// json tag on the hash because there is no exported field to tag.
func publicUser(user User) publicUserResponse {
	return publicUserResponse{
		ID:          user.ID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		CreatedAt:   user.CreatedAt.Format(time.RFC3339),
	}
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

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		next.ServeHTTP(w, r)

		// Never log the Authorization header or the request body: both carry
		// credentials.
		log.Printf("method=%s path=%s duration=%s",
			r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

//
// MAIN
//

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day51: ")

	ctx := context.Background()

	db, err := OpenDB(ctx, envOr("DB_PATH", ":memory:"))
	if err != nil {
		log.Fatalf("database unavailable: %v", err)
	}

	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	}()

	var hasher Hasher = NewBcryptHasher()

	if envOr("HASHER", "bcrypt") == "argon2id" {
		hasher = NewArgon2Hasher()
	}

	ttl, err := time.ParseDuration(envOr("SESSION_TTL", "24h"))
	if err != nil || ttl <= 0 {
		log.Printf("invalid SESSION_TTL, using 24h")

		ttl = 24 * time.Hour
	}

	api, err := NewAPI(NewStore(db), hasher, ttl)
	if err != nil {
		log.Fatalf("build api: %v", err)
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
		log.Printf("listening on %s hasher=%s session_ttl=%s", server.Addr, hasher.Name(), ttl)

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	// Expired sessions are deleted periodically. The query that reads a
	// session also filters on expiry, so this is hygiene, not security.
	purgeCtx, stopPurge := context.WithCancel(ctx)
	defer stopPurge()

	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-purgeCtx.Done():
				return

			case <-ticker.C:
				removed, err := api.store.PurgeExpiredSessions(purgeCtx)
				if err != nil {
					log.Printf("purge sessions: %v", err)
					continue
				}

				if removed > 0 {
					log.Printf("purged %d expired session(s)", removed)
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

	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
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
