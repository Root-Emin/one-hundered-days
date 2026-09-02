// Package api is the HTTP layer of the bookmarks service.
package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-66/internal/store"
)

type API struct {
	store *store.Store
}

func New(bookmarks *store.Store) *API {
	return &API{store: bookmarks}
}

func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("POST /bookmarks", a.create)
	mux.HandleFunc("GET /bookmarks", a.list)
	mux.HandleFunc("GET /bookmarks/{id}", a.get)
	mux.HandleFunc("DELETE /bookmarks/{id}", a.delete)

	return mux
}

type bookmarkResponse struct {
	ID        int64    `json:"id"`
	Owner     string   `json:"owner"`
	URL       string   `json:"url"`
	Title     string   `json:"title"`
	Tags      []string `json:"tags"`
	CreatedAt string   `json:"created_at"`
}

func toResponse(bookmark store.Bookmark) bookmarkResponse {
	tags := bookmark.Tags

	if tags == nil {
		tags = []string{}
	}

	return bookmarkResponse{
		ID:        bookmark.ID,
		Owner:     bookmark.Owner,
		URL:       bookmark.URL,
		Title:     bookmark.Title,
		Tags:      tags,
		CreatedAt: bookmark.CreatedAt.Format(time.RFC3339),
	}
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	if _, err := a.store.Count(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unreachable")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) create(w http.ResponseWriter, r *http.Request) {
	owner, ok := ownerFrom(w, r)
	if !ok {
		return
	}

	var input struct {
		URL   string   `json:"url"`
		Title string   `json:"title"`
		Tags  []string `json:"tags"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	url := strings.TrimSpace(input.URL)
	title := strings.TrimSpace(input.Title)

	switch {
	case url == "":
		writeError(w, http.StatusUnprocessableEntity, "url is required")
		return

	case !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://"):
		writeError(w, http.StatusUnprocessableEntity, "url must start with http:// or https://")
		return

	case title == "":
		writeError(w, http.StatusUnprocessableEntity, "title is required")
		return
	}

	for _, tag := range input.Tags {
		if strings.ContainsAny(tag, ",") {
			writeError(w, http.StatusUnprocessableEntity, "tags may not contain commas")
			return
		}
	}

	bookmark, err := a.store.Create(r.Context(), store.Bookmark{
		Owner: owner, URL: url, Title: title, Tags: input.Tags,
	})
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toResponse(bookmark))
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	owner, ok := ownerFrom(w, r)
	if !ok {
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	if limit <= 0 || limit > 100 {
		limit = 20
	}

	bookmarks, err := a.store.ListByOwner(r.Context(), owner, r.URL.Query().Get("tag"), limit)
	if err != nil {
		respondError(w, err)
		return
	}

	responses := make([]bookmarkResponse, 0, len(bookmarks))

	for _, bookmark := range bookmarks {
		responses = append(responses, toResponse(bookmark))
	}

	writeJSON(w, http.StatusOK, map[string]any{"bookmarks": responses, "count": len(responses)})
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	owner, ok := ownerFrom(w, r)
	if !ok {
		return
	}

	id, ok := pathID(w, r)
	if !ok {
		return
	}

	bookmark, err := a.store.ByID(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}

	// Ownership is checked here rather than in the query, so the wiring bug
	// this catches (a handler that forgets the check) is visible in the
	// integration test rather than in production.
	if bookmark.Owner != owner {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	writeJSON(w, http.StatusOK, toResponse(bookmark))
}

func (a *API) delete(w http.ResponseWriter, r *http.Request) {
	owner, ok := ownerFrom(w, r)
	if !ok {
		return
	}

	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if err := a.store.Delete(r.Context(), owner, id); err != nil {
		respondError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func ownerFrom(w http.ResponseWriter, r *http.Request) (string, bool) {
	owner := strings.TrimSpace(r.Header.Get("X-Owner"))

	if owner == "" {
		writeError(w, http.StatusUnauthorized, "X-Owner header is required")
		return "", false
	}

	return owner, true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}

	return id, true
}

func respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")

	case errors.Is(err, store.ErrDuplicate):
		writeError(w, http.StatusConflict, "url already saved")

	default:
		log.Printf("internal error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	defer func() {
		if err := r.Body.Close(); err != nil {
			log.Printf("close body: %v", err)
		}
	}()

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
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
