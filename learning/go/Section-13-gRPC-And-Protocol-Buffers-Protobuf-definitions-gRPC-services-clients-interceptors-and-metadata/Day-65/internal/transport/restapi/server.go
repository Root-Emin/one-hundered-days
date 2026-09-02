// Package restapi is the HTTP transport over the same notes service.
package restapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-65/internal/notes"
)

type Authenticator func(token string) (string, bool)

type contextKey string

const userContextKey contextKey = "user_id"

func userFrom(ctx context.Context) string {
	user, _ := ctx.Value(userContextKey).(string)

	return user
}

type Handler struct {
	service      *notes.Service
	authenticate Authenticator
}

// NewHandler takes the same Authenticator the gRPC server takes: one identity
// source, two transports.
func NewHandler(service *notes.Service, authenticate Authenticator) *Handler {
	return &Handler{service: service, authenticate: authenticate}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.Handle("POST /notes", h.authMiddleware(http.HandlerFunc(h.create)))
	mux.Handle("GET /notes", h.authMiddleware(http.HandlerFunc(h.list)))
	mux.Handle("GET /notes/{id}", h.authMiddleware(http.HandlerFunc(h.get)))
	mux.Handle("POST /notes/{id}/archive", h.authMiddleware(http.HandlerFunc(h.archive)))
	mux.Handle("DELETE /notes/{id}", h.authMiddleware(http.HandlerFunc(h.delete)))

	return h.logging(mux)
}

func (h *Handler) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")

		if !found || !strings.EqualFold(scheme, "bearer") {
			unauthorized(w)
			return
		}

		userID, valid := h.authenticate(strings.TrimSpace(token))
		if !valid {
			unauthorized(w)
			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, userID)))
	})
}

func (h *Handler) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		next.ServeHTTP(w, r)

		log.Printf("http  method=%s path=%s duration=%s",
			r.Method, r.URL.Path, time.Since(start).Round(time.Microsecond))
	})
}

type noteResponse struct {
	ID        int64  `json:"id"`
	OwnerID   string `json:"owner_id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Archived  bool   `json:"archived"`
	CreatedAt string `json:"created_at"`
}

func toResponse(note notes.Note) noteResponse {
	return noteResponse{
		ID:        note.ID,
		OwnerID:   note.OwnerID,
		Title:     note.Title,
		Body:      note.Body,
		Archived:  note.Archived,
		CreatedAt: note.CreatedAt.Format(time.RFC3339),
	}
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	note, err := h.service.Create(r.Context(), userFrom(r.Context()), input.Title, input.Body)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toResponse(note))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	note, err := h.service.Get(r.Context(), userFrom(r.Context()), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toResponse(note))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	includeArchived := r.URL.Query().Get("include_archived") == "true"

	found, total, err := h.service.List(r.Context(), userFrom(r.Context()), int32(pageSize), includeArchived)
	if err != nil {
		respondError(w, err)
		return
	}

	responses := make([]noteResponse, 0, len(found))

	for _, note := range found {
		responses = append(responses, toResponse(note))
	}

	writeJSON(w, http.StatusOK, map[string]any{"notes": responses, "total_size": total})
}

func (h *Handler) archive(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	note, err := h.service.Archive(r.Context(), userFrom(r.Context()), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toResponse(note))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	deleted, err := h.service.Delete(r.Context(), userFrom(r.Context()), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"deleted": deleted})
}

// respondError mirrors grpcapi.toStatus. The parity test in the cmd package
// asserts that the two agree on every case.
func respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, notes.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")

	case errors.Is(err, notes.ErrForbidden):
		writeError(w, http.StatusForbidden, "not your note")

	case errors.Is(err, notes.ErrValidation):
		writeError(w, http.StatusUnprocessableEntity, err.Error())

	case errors.Is(err, notes.ErrUnauthenticated):
		unauthorized(w)

	default:
		log.Printf("internal error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}

	return id, true
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

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
	writeError(w, http.StatusUnauthorized, "authentication required")
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
