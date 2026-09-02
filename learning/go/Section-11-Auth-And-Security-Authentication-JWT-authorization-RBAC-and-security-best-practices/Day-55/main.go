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
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"
)

/*
Day 55 - Auth & Security: Practice

Section 11 capstone. The notes MVP, secured end to end. See README.md for the
client-facing auth documentation and the threat review.

Tasks covered:

 1. Registration, login, JWT access tokens, rotating refresh tokens and role
    checks on every protected route
 2. Tests for success, wrong password, missing token, expired token, and
    forbidden roles (auth_test.go)
 3. Client documentation: README.md, "Getting and sending a token"
 4. Honest threat review: README.md, "What is not solved"

Files:

	store.go       users, notes, refresh tokens, audit log
	auth.go        hashing, JWT, roles, validation, rate limiting
	main.go        HTTP API and middleware
	auth_test.go   the full auth and authorization test suite
	README.md      client docs + threat model

Run:

	go run .                 # :8080, in-memory database, seeded demo users
	DB_PATH=data/day55.db go run .

Environment variables:

	PORT                HTTP port.                    Default: 8080
	DB_PATH             SQLite path.                  Default: :memory:
	JWT_SIGNING_KEY     Signing secret (>= 32 chars). Required when ENV=production
	JWT_SIGNING_KEY_ID  Key id for rotation.          Default: v1
	ENV                 production tightens defaults
	SEED_DEMO_USERS     Seed demo accounts.           Default: true when DB_PATH=:memory:

Test:

	go test ./...
	go test -race -count=1 ./...
*/

type API struct {
	store  *Store
	tokens *TokenService
	hasher PasswordHasher

	globalLimiter *Limiter
	loginLimiter  *Limiter

	dummyHash string // constant-time cost for unknown accounts
}

func NewAPI(store *Store, tokens *TokenService, hasher PasswordHasher) (*API, error) {
	dummy, err := hasher.Hash("a password that exists only to spend the same CPU time")
	if err != nil {
		return nil, fmt.Errorf("prepare dummy hash: %w", err)
	}

	return &API{
		store:         store,
		tokens:        tokens,
		hasher:        hasher,
		globalLimiter: NewLimiter(20, 40, 10*time.Minute),
		loginLimiter:  NewLimiter(1.0/6.0, 5, time.Hour),
		dummyHash:     dummy,
	}, nil
}

func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("POST /auth/register", a.register)
	mux.HandleFunc("POST /auth/login", a.login)
	mux.HandleFunc("POST /auth/refresh", a.refresh)

	mux.Handle("POST /auth/logout", a.RequireAuth(http.HandlerFunc(a.logout)))
	mux.Handle("GET /me", a.RequireAuth(http.HandlerFunc(a.me)))

	mux.Handle("GET /notes", a.RequireAuth(http.HandlerFunc(a.listNotes)))
	mux.Handle("POST /notes", a.RequireAuth(a.RequirePermission(PermNoteCreate, http.HandlerFunc(a.createNote))))
	mux.Handle("GET /notes/{id}", a.RequireAuth(http.HandlerFunc(a.getNote)))
	mux.Handle("DELETE /notes/{id}", a.RequireAuth(http.HandlerFunc(a.deleteNote)))

	mux.Handle("GET /admin/users", a.RequireAuth(a.RequirePermission(PermUserList, http.HandlerFunc(a.listUsers))))
	mux.Handle("POST /admin/users/{id}/suspend", a.RequireAuth(a.RequirePermission(PermUserSuspend, http.HandlerFunc(a.suspendUser))))
	mux.Handle("GET /admin/audit", a.RequireAuth(a.RequirePermission(PermAuditRead, http.HandlerFunc(a.auditLog))))

	// Outer to inner: headers, body cap, rate limit, routes.
	var handler http.Handler = mux

	handler = a.rateLimit(handler)
	handler = bodyLimit(1<<20, handler)
	handler = securityHeaders(handler)

	return handler
}

//
// MIDDLEWARE
//

type contextKey string

const (
	userContextKey   contextKey = "user"
	claimsContextKey contextKey = "claims"
)

func userFrom(ctx context.Context) (User, bool) {
	user, ok := ctx.Value(userContextKey).(User)

	return user, ok
}

func claimsFrom(ctx context.Context) (Claims, bool) {
	claims, ok := ctx.Value(claimsContextKey).(Claims)

	return claims, ok
}

// RequireAuth: 401 for anything without a valid, non-revoked token whose user
// still exists and is not suspended.
func (a *API) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, raw, found := strings.Cut(r.Header.Get("Authorization"), " ")

		if !found || !strings.EqualFold(scheme, "bearer") || strings.TrimSpace(raw) == "" {
			unauthorized(w, "missing bearer token")
			return
		}

		claims, err := a.tokens.Verify(strings.TrimSpace(raw))
		if err != nil {
			if errors.Is(err, ErrExpiredToken) {
				unauthorized(w, "token expired")
				return
			}

			unauthorized(w, "invalid token")

			return
		}

		userID, err := strconv.ParseInt(claims.Subject, 10, 64)
		if err != nil {
			unauthorized(w, "invalid token")
			return
		}

		// The token is only a claim about the user; the database is the truth.
		// Without this lookup a suspended user keeps working for the rest of
		// the token's lifetime.
		user, err := a.store.UserByID(r.Context(), userID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				unauthorized(w, "invalid token")
				return
			}

			log.Printf("user lookup failed: %v", err)
			writeError(w, http.StatusInternalServerError, "internal server error")

			return
		}

		if user.Suspended {
			writeError(w, http.StatusForbidden, "account suspended")
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, user)
		ctx = context.WithValue(ctx, claimsContextKey, claims)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequirePermission: 403 for an authenticated caller whose role lacks it.
func (a *API) RequirePermission(permission Permission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := userFrom(r.Context())
		if !ok {
			unauthorized(w, "missing token")
			return
		}

		if !user.Role.Can(permission) {
			log.Printf("denied user=%d role=%s permission=%s path=%s",
				user.ID, user.Role, permission, r.URL.Path)
			writeError(w, http.StatusForbidden, "forbidden")

			return
		}

		next.ServeHTTP(w, r)
	})
}

func (a *API) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed, retryAfter := a.globalLimiter.Allow(ClientIP(r))
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, "too many requests")

			return
		}

		next.ServeHTTP(w, r)
	})
}

func bodyLimit(maxBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()

		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("Cache-Control", "no-store")

		if r.TLS != nil {
			header.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		next.ServeHTTP(w, r)
	})
}

//
// AUTH HANDLERS
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

	email, err := ValidateEmail(input.Email)
	if err != nil {
		respondInvalid(w, err)
		return
	}

	displayName, err := ValidateText("display_name", input.DisplayName, 1, 80)
	if err != nil {
		respondInvalid(w, err)
		return
	}

	if err := ValidatePassword(input.Password); err != nil {
		respondInvalid(w, err)
		return
	}

	hash, err := a.hasher.Hash(input.Password)
	if err != nil {
		log.Printf("hash password: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	// New accounts always get the lowest role. Role assignment is an admin
	// action, never a field in a public request body.
	user, err := a.store.CreateUser(r.Context(), email, displayName, hash, RoleMember)
	if err != nil {
		if errors.Is(err, ErrEmailTaken) {
			writeError(w, http.StatusConflict, "email already registered")
			return
		}

		log.Printf("create user: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	a.store.Audit(r.Context(), &user.ID, "user.register", SanitizeForLog(email), ClientIP(r))

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

	email := strings.ToLower(strings.TrimSpace(input.Email))

	// Per-account and per-IP: brute force is expensive from any angle.
	allowed, retryAfter := a.loginLimiter.Allow("login:" + email + "|" + ClientIP(r))
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		a.store.Audit(r.Context(), nil, "auth.rate_limited", SanitizeForLog(email), ClientIP(r))
		writeError(w, http.StatusTooManyRequests, "too many login attempts")

		return
	}

	user, lookupErr := a.store.UserByEmail(r.Context(), email)

	if lookupErr != nil && !errors.Is(lookupErr, ErrNotFound) {
		log.Printf("login lookup: %v", lookupErr)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	// Unknown accounts still pay for a hash comparison, so response time does
	// not reveal which emails exist.
	hash := a.dummyHash
	if lookupErr == nil {
		hash = user.PasswordHash
	}

	verifyErr := a.hasher.Verify(input.Password, hash)

	if lookupErr != nil || verifyErr != nil {
		a.store.Audit(r.Context(), nil, "auth.login_failed", SanitizeForLog(email), ClientIP(r))
		unauthorized(w, "invalid email or password")

		return
	}

	if user.Suspended {
		a.store.Audit(r.Context(), &user.ID, "auth.login_suspended", "", ClientIP(r))
		writeError(w, http.StatusForbidden, "account suspended")

		return
	}

	a.issueTokens(w, r, user, "auth.login")
}

func (a *API) refresh(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RefreshToken string `json:"refresh_token"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	userID, err := a.store.RedeemRefreshToken(r.Context(), input.RefreshToken)
	if err != nil {
		// A replayed token has already revoked the family inside the store.
		a.store.Audit(r.Context(), nil, "auth.refresh_rejected", SanitizeForLog(err.Error()), ClientIP(r))
		unauthorized(w, "invalid refresh token")

		return
	}

	user, err := a.store.UserByID(r.Context(), userID)
	if err != nil || user.Suspended {
		unauthorized(w, "invalid refresh token")
		return
	}

	a.issueTokens(w, r, user, "auth.refresh")
}

// issueTokens mints a new access/refresh pair. Refresh tokens rotate on every
// use, so a stolen one is only good until the real client refreshes.
func (a *API) issueTokens(w http.ResponseWriter, r *http.Request, user User, action string) {
	access, _, err := a.tokens.Issue(user)
	if err != nil {
		log.Printf("issue access token: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	refresh, err := RandomToken()
	if err != nil {
		log.Printf("generate refresh token: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	if err := a.store.StoreRefreshToken(r.Context(), refresh, user.ID, RefreshTokenTTL); err != nil {
		log.Printf("store refresh token: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	a.store.Audit(r.Context(), &user.ID, action, "", ClientIP(r))

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"token_type":    "Bearer",
		"expires_in":    int(AccessTokenTTL.Seconds()),
		"user":          publicUser(user),
	})
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	user, _ := userFrom(r.Context())
	claims, _ := claimsFrom(r.Context())

	// Both halves: deny-list the access token until it expires, and drop
	// every refresh token so the session cannot be resurrected.
	a.tokens.Revoke(claims)

	if err := a.store.RevokeUserTokens(r.Context(), user.ID); err != nil {
		log.Printf("revoke refresh tokens: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	a.store.Audit(r.Context(), &user.ID, "auth.logout", "", ClientIP(r))

	w.WriteHeader(http.StatusNoContent)
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	user, _ := userFrom(r.Context())

	permissions := make([]string, 0)

	for _, permission := range rolePermissions[user.Role] {
		permissions = append(permissions, string(permission))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"user":        publicUser(user),
		"permissions": permissions,
	})
}

//
// NOTES
//

func (a *API) listNotes(w http.ResponseWriter, r *http.Request) {
	user, _ := userFrom(r.Context())

	notes, err := a.store.ListNotes(r.Context(), user.ID, 50)
	if err != nil {
		log.Printf("list notes: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"notes": notes, "count": len(notes)})
}

func (a *API) createNote(w http.ResponseWriter, r *http.Request) {
	user, _ := userFrom(r.Context())

	var input struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	title, err := ValidateText("title", input.Title, 1, 200)
	if err != nil {
		respondInvalid(w, err)
		return
	}

	body, err := ValidateText("body", input.Body, 0, 10_000)
	if err != nil {
		respondInvalid(w, err)
		return
	}

	// Ownership comes from the token, never from the request.
	note, err := a.store.CreateNote(r.Context(), user.ID, title, body)
	if err != nil {
		log.Printf("create note: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	writeJSON(w, http.StatusCreated, note)
}

func (a *API) getNote(w http.ResponseWriter, r *http.Request) {
	user, _ := userFrom(r.Context())

	note, ok := a.loadNote(w, r)
	if !ok {
		return
	}

	if err := AuthorizeNote(user, "read", note); err != nil {
		log.Printf("denied: %v", err)
		writeError(w, http.StatusForbidden, "forbidden")

		return
	}

	writeJSON(w, http.StatusOK, note)
}

func (a *API) deleteNote(w http.ResponseWriter, r *http.Request) {
	user, _ := userFrom(r.Context())

	note, ok := a.loadNote(w, r)
	if !ok {
		return
	}

	if err := AuthorizeNote(user, "delete", note); err != nil {
		log.Printf("denied: %v", err)
		writeError(w, http.StatusForbidden, "forbidden")

		return
	}

	if err := a.store.DeleteNote(r.Context(), note.ID); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}

		log.Printf("delete note: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	a.store.Audit(r.Context(), &user.ID, "note.delete", strconv.FormatInt(note.ID, 10), ClientIP(r))

	w.WriteHeader(http.StatusNoContent)
}

func (a *API) loadNote(w http.ResponseWriter, r *http.Request) (Note, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return Note{}, false
	}

	note, err := a.store.NoteByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return Note{}, false
		}

		log.Printf("load note: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return Note{}, false
	}

	return note, true
}

//
// ADMIN
//

func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.store.ListUsers(r.Context(), 100)
	if err != nil {
		log.Printf("list users: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	responses := make([]publicUserResponse, 0, len(users))

	for _, user := range users {
		responses = append(responses, publicUser(user))
	}

	writeJSON(w, http.StatusOK, map[string]any{"users": responses})
}

func (a *API) suspendUser(w http.ResponseWriter, r *http.Request) {
	actor, _ := userFrom(r.Context())

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	if id == actor.ID {
		// Locking the last admin out of the system is an outage, not a
		// security win.
		writeError(w, http.StatusUnprocessableEntity, "an admin cannot suspend themselves")
		return
	}

	if err := a.store.SetSuspended(r.Context(), id, true); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}

		log.Printf("suspend user: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	a.store.Audit(r.Context(), &actor.ID, "user.suspend", strconv.FormatInt(id, 10), ClientIP(r))

	w.WriteHeader(http.StatusNoContent)
}

func (a *API) auditLog(w http.ResponseWriter, r *http.Request) {
	entries, err := a.store.AuditTail(r.Context(), 50)
	if err != nil {
		log.Printf("read audit log: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

//
// DTO AND HELPERS
//

type publicUserResponse struct {
	ID          int64  `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Suspended   bool   `json:"suspended"`
	CreatedAt   string `json:"created_at"`
}

// publicUser drops PasswordHash. The User struct is never marshalled directly
// anywhere in this program - that is what keeps the hash out of responses.
func publicUser(user User) publicUserResponse {
	return publicUserResponse{
		ID:          user.ID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		Role:        string(user.Role),
		Suspended:   user.Suspended,
		CreatedAt:   user.CreatedAt.Format(time.RFC3339),
	}
}

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
	writeError(w, http.StatusUnprocessableEntity, strings.TrimPrefix(err.Error(), "invalid input: "))
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
// SEED AND MAIN
//

func seedDemoUsers(ctx context.Context, store *Store, hasher PasswordHasher) error {
	demo := []struct {
		email    string
		name     string
		password string
		role     Role
	}{
		{"member@example.com", "Member", "correct-horse-7", RoleMember},
		{"editor@example.com", "Editor", "correct-horse-7", RoleEditor},
		{"admin@example.com", "Admin", "correct-horse-7", RoleAdmin},
	}

	for _, item := range demo {
		hash, err := hasher.Hash(item.password)
		if err != nil {
			return err
		}

		if _, err := store.CreateUser(ctx, item.email, item.name, hash, item.role); err != nil {
			if errors.Is(err, ErrEmailTaken) {
				continue
			}

			return err
		}
	}

	log.Printf("seeded demo users (password: correct-horse-7) - development only")

	return nil
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day55: ")

	ctx := context.Background()

	dbPath := envOr("DB_PATH", ":memory:")

	db, err := OpenDB(ctx, dbPath)
	if err != nil {
		log.Fatalf("database unavailable: %v", err)
	}

	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	}()

	tokens, err := NewTokenService(AccessTokenTTL)
	if err != nil {
		log.Fatalf("token service: %v", err)
	}

	if os.Getenv("JWT_SIGNING_KEY") == "" {
		log.Printf("WARNING: JWT_SIGNING_KEY is unset; using a throwaway development key")
	}

	store := NewStore(db)
	hasher := PasswordHasher{Cost: bcrypt.DefaultCost}

	if envBool("SEED_DEMO_USERS", dbPath == ":memory:") {
		if err := seedDemoUsers(ctx, store, hasher); err != nil {
			log.Fatalf("seed: %v", err)
		}
	}

	api, err := NewAPI(store, tokens, hasher)
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
		log.Printf("listening on %s db=%s", server.Addr, dbPath)

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	// Housekeeping: bounded denylist, bounded limiter maps.
	maintenanceCtx, stopMaintenance := context.WithCancel(ctx)
	defer stopMaintenance()

	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-maintenanceCtx.Done():
				return

			case <-ticker.C:
				api.tokens.PurgeRevoked()
				api.globalLimiter.Cleanup()
				api.loginLimiter.Cleanup()
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

func envBool(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "":
		return fallback
	case "1", "true", "yes":
		return true
	case "0", "false", "no":
		return false
	default:
		return fallback
	}
}
