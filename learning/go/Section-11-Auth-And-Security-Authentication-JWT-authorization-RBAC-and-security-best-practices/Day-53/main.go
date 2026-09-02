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
	"sync"
	"syscall"
	"time"
)

/*
Day 53 - Auth & Security: Authorization and RBAC

Tasks covered:

 1. Roles modelled in the domain (guest, member, editor, admin) - policy.go
 2. Permission checked before every sensitive or destructive operation
 3. 403 Forbidden for an authenticated caller without permission, 401 only
    when there is no valid identity
 4. Policy centralized: one table, one AuthorizeDocument function, plus
    RequirePermission / RequireRole middleware

Files:

	policy.go       roles, permissions, principals, the authorization rules
	main.go         API, middleware, in-memory documents
	policy_test.go  the allow/deny matrix for every role and action

Run:

	go run .          # HTTP server on :8080
	go run . matrix   # print the permission matrix
	go run . demo     # run every role against every action, no server needed

Authentication here is a simple demo token (see tokens map below) so the day
stays focused on authorization. Day 52 has the real JWT version.

Try it:

	curl localhost:8080/documents -H "Authorization: Bearer member-token"
	curl -XDELETE localhost:8080/documents/1 -H "Authorization: Bearer member-token"   # 403
	curl -XDELETE localhost:8080/documents/1 -H "Authorization: Bearer admin-token"    # 204
	curl -XDELETE localhost:8080/documents/1                                            # 401

Test:

	go test ./...
*/

//
// DEMO IDENTITIES
//

var principals = map[string]Principal{
	"guest-token":  {UserID: 1, Email: "guest@example.com", Roles: []Role{RoleGuest}},
	"member-token": {UserID: 2, Email: "member@example.com", Roles: []Role{RoleMember}},
	"other-token":  {UserID: 3, Email: "other@example.com", Roles: []Role{RoleMember}},
	"editor-token": {UserID: 4, Email: "editor@example.com", Roles: []Role{RoleEditor}},
	"admin-token":  {UserID: 5, Email: "admin@example.com", Roles: []Role{RoleAdmin}},
}

//
// STORE
//

type DocumentStore struct {
	mu        sync.RWMutex
	documents map[int64]Document
	nextID    int64
}

func NewDocumentStore() *DocumentStore {
	store := &DocumentStore{documents: make(map[int64]Document), nextID: 1}

	seed := []Document{
		{OwnerID: 2, Title: "Member's published note", Body: "visible to everyone", Published: true},
		{OwnerID: 2, Title: "Member's draft", Body: "owner only", Published: false},
		{OwnerID: 3, Title: "Another member's draft", Body: "not yours", Published: false},
	}

	for _, document := range seed {
		document.ID = store.nextID
		store.documents[document.ID] = document
		store.nextID++
	}

	return store
}

func (s *DocumentStore) ByID(id int64) (Document, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	document, found := s.documents[id]

	return document, found
}

func (s *DocumentStore) List() []Document {
	s.mu.RLock()
	defer s.mu.RUnlock()

	documents := make([]Document, 0, len(s.documents))

	for _, document := range s.documents {
		documents = append(documents, document)
	}

	sortDocuments(documents)

	return documents
}

func (s *DocumentStore) Create(ownerID int64, title, body string) Document {
	s.mu.Lock()
	defer s.mu.Unlock()

	document := Document{ID: s.nextID, OwnerID: ownerID, Title: title, Body: body}

	s.documents[document.ID] = document
	s.nextID++

	return document
}

func (s *DocumentStore) Update(id int64, title, body string) (Document, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	document, found := s.documents[id]
	if !found {
		return Document{}, false
	}

	document.Title = title
	document.Body = body
	s.documents[id] = document

	return document, true
}

func (s *DocumentStore) Publish(id int64) (Document, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	document, found := s.documents[id]
	if !found {
		return Document{}, false
	}

	document.Published = true
	s.documents[id] = document

	return document, true
}

func (s *DocumentStore) Delete(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, found := s.documents[id]; !found {
		return false
	}

	delete(s.documents, id)

	return true
}

func sortDocuments(documents []Document) {
	for i := 1; i < len(documents); i++ {
		for j := i; j > 0 && documents[j].ID < documents[j-1].ID; j-- {
			documents[j], documents[j-1] = documents[j-1], documents[j]
		}
	}
}

//
// API
//

type API struct {
	documents *DocumentStore
}

type contextKey string

const principalContextKey contextKey = "principal"

func principalFrom(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey).(Principal)

	return principal, ok
}

func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Every route below requires an identity. Authorization then happens
	// either in middleware (coarse, role or permission based) or in the
	// handler through AuthorizeDocument (fine, resource aware).
	mux.Handle("GET /me", Authenticate(http.HandlerFunc(a.me)))
	mux.Handle("GET /documents", Authenticate(http.HandlerFunc(a.listDocuments)))
	mux.Handle("GET /documents/{id}", Authenticate(http.HandlerFunc(a.getDocument)))
	mux.Handle("POST /documents", Authenticate(RequirePermission(PermDocumentCreate)(http.HandlerFunc(a.createDocument))))
	mux.Handle("PUT /documents/{id}", Authenticate(http.HandlerFunc(a.updateDocument)))
	mux.Handle("POST /documents/{id}/publish", Authenticate(RequirePermission(PermDocumentPublish)(http.HandlerFunc(a.publishDocument))))
	mux.Handle("DELETE /documents/{id}", Authenticate(http.HandlerFunc(a.deleteDocument)))

	// Admin-only surface, gated by role rather than by resource.
	mux.Handle("GET /admin/users", Authenticate(RequireRole(RoleAdmin)(http.HandlerFunc(a.listUsers))))
	mux.Handle("GET /admin/audit", Authenticate(RequirePermission(PermAuditRead)(http.HandlerFunc(a.auditLog))))

	return mux
}

//
// MIDDLEWARE
//

// Authenticate resolves the bearer token into a Principal. Failure here is
// always 401: there is no identity to forbid.
func Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")

		if !found || !strings.EqualFold(scheme, "bearer") {
			respondUnauthorized(w)
			return
		}

		principal, known := principals[strings.TrimSpace(token)]
		if !known {
			respondUnauthorized(w)
			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalContextKey, principal)))
	})
}

// RequirePermission is the coarse gate: it answers "may this role reach this
// endpoint at all?" before any handler code runs.
func RequirePermission(permission Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := principalFrom(r.Context())
			if !ok {
				respondUnauthorized(w)
				return
			}

			if !principal.Can(permission) {
				log.Printf("denied: %s lacks %s for %s %s", principal, permission, r.Method, r.URL.Path)
				respondForbidden(w)

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireRole exists for the rare check that is genuinely about the role
// itself (an admin console), not about a capability.
func RequireRole(role Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := principalFrom(r.Context())
			if !ok {
				respondUnauthorized(w)
				return
			}

			if !principal.HasRole(role) {
				log.Printf("denied: %s is not %s for %s %s", principal, role, r.Method, r.URL.Path)
				respondForbidden(w)

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

//
// HANDLERS
//

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())

	permissions := make([]string, 0)

	for _, permission := range principal.Permissions() {
		permissions = append(permissions, string(permission))
	}

	roles := make([]string, 0, len(principal.Roles))

	for _, role := range principal.Roles {
		roles = append(roles, string(role))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":     principal.UserID,
		"email":       principal.Email,
		"roles":       roles,
		"permissions": permissions,
	})
}

// listDocuments filters by the same policy that guards a single read, so the
// list endpoint cannot become a way to see drafts you may not open directly.
func (a *API) listDocuments(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())

	visible := make([]Document, 0)

	for _, document := range a.documents.List() {
		if err := AuthorizeDocument(principal, ActionRead, &document); err == nil {
			visible = append(visible, document)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"documents": visible, "count": len(visible)})
}

func (a *API) getDocument(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())

	document, ok := a.loadDocument(w, r)
	if !ok {
		return
	}

	if err := AuthorizeDocument(principal, ActionRead, &document); err != nil {
		log.Printf("denied: %v", err)
		respondForbidden(w)

		return
	}

	writeJSON(w, http.StatusOK, document)
}

func (a *API) createDocument(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())

	var input struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	if strings.TrimSpace(input.Title) == "" {
		writeError(w, http.StatusUnprocessableEntity, "title is required")
		return
	}

	// The owner comes from the authenticated principal, never from the body.
	// Accepting an owner_id field would let anyone create documents as
	// someone else - a classic broken-access-control bug.
	document := a.documents.Create(principal.UserID, input.Title, input.Body)

	writeJSON(w, http.StatusCreated, document)
}

func (a *API) updateDocument(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())

	document, ok := a.loadDocument(w, r)
	if !ok {
		return
	}

	// Resource-aware: members may edit their own, editors may edit any.
	if err := AuthorizeDocument(principal, ActionUpdate, &document); err != nil {
		log.Printf("denied: %v", err)
		respondForbidden(w)

		return
	}

	var input struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	updated, found := a.documents.Update(document.ID, input.Title, input.Body)
	if !found {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

func (a *API) publishDocument(w http.ResponseWriter, r *http.Request) {
	document, ok := a.loadDocument(w, r)
	if !ok {
		return
	}

	published, found := a.documents.Publish(document.ID)
	if !found {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	writeJSON(w, http.StatusOK, published)
}

func (a *API) deleteDocument(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())

	document, ok := a.loadDocument(w, r)
	if !ok {
		return
	}

	// Destructive: authorize before doing anything, and do not let ownership
	// substitute for the delete permission.
	if err := AuthorizeDocument(principal, ActionDelete, &document); err != nil {
		log.Printf("denied: %v", err)
		respondForbidden(w)

		return
	}

	if !a.documents.Delete(document.ID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	log.Printf("audit: %s deleted document %d", principal, document.ID)

	w.WriteHeader(http.StatusNoContent)
}

func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	users := make([]map[string]any, 0, len(principals))

	for _, principal := range principals {
		roles := make([]string, 0, len(principal.Roles))

		for _, role := range principal.Roles {
			roles = append(roles, string(role))
		}

		users = append(users, map[string]any{
			"user_id": principal.UserID,
			"email":   principal.Email,
			"roles":   roles,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (a *API) auditLog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": []string{"audit entries would be listed here"},
	})
}

func (a *API) loadDocument(w http.ResponseWriter, r *http.Request) (Document, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return Document{}, false
	}

	document, found := a.documents.ByID(id)
	if !found {
		writeError(w, http.StatusNotFound, "not found")
		return Document{}, false
	}

	return document, true
}

//
// RESPONSES
//

func respondUnauthorized(w http.ResponseWriter) {
	// 401: we do not know who you are.
	w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
	writeError(w, http.StatusUnauthorized, "authentication required")
}

func respondForbidden(w http.ResponseWriter) {
	// 403: we know exactly who you are, and the answer is still no. The
	// reason went to the log, not to the client.
	writeError(w, http.StatusForbidden, "forbidden")
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
// DEMO
//

func runDemo() {
	fmt.Println("\nEvery role against every document action")
	fmt.Println(strings.Repeat("-", 78))

	documents := map[string]*Document{
		"own draft":     {ID: 1, OwnerID: 2, Published: false},
		"others' draft": {ID: 2, OwnerID: 3, Published: false},
		"published":     {ID: 3, OwnerID: 3, Published: true},
	}

	order := []string{"own draft", "others' draft", "published"}

	actions := []Action{ActionRead, ActionUpdate, ActionPublish, ActionDelete}

	fmt.Printf("%-10s %-16s", "ROLE", "DOCUMENT")

	for _, action := range actions {
		fmt.Printf("%-10s", action)
	}

	fmt.Println()

	for _, role := range KnownRoles() {
		// Every demo principal is user 2, so "own draft" really is theirs.
		principal := Principal{UserID: 2, Email: string(role) + "@example.com", Roles: []Role{role}}

		for _, label := range order {
			fmt.Printf("%-10s %-16s", role, label)

			for _, action := range actions {
				mark := "allow"

				if err := AuthorizeDocument(principal, action, documents[label]); err != nil {
					mark = "DENY"
				}

				fmt.Printf("%-10s", mark)
			}

			fmt.Println()
		}
	}

	fmt.Println("\nRead the DENY column for 'others' draft': that is the ownership rule")
	fmt.Println("doing work that roles alone cannot express.")
}

//
// MAIN
//

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day53: ")

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "matrix":
			fmt.Println()
			fmt.Print(PermissionMatrix())

			return

		case "demo":
			runDemo()

			return
		}
	}

	api := &API{documents: NewDocumentStore()}

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
		log.Printf("listening on %s", server.Addr)
		log.Printf("demo tokens: guest-token member-token other-token editor-token admin-token")

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
