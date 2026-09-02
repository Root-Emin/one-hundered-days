// Package httpapi is the transport layer for the todo service.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-70/internal/todo"
)

type Handler struct {
	service *todo.Service
}

func New(service *todo.Service) *Handler {
	return &Handler{service: service}
}

type contextKey string

const ownerContextKey contextKey = "owner"

func ownerFrom(ctx context.Context) string {
	owner, _ := ctx.Value(ownerContextKey).(string)

	return owner
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.Handle("POST /tasks", h.authenticate(http.HandlerFunc(h.create)))
	mux.Handle("GET /tasks", h.authenticate(http.HandlerFunc(h.list)))
	mux.Handle("GET /tasks/overdue", h.authenticate(http.HandlerFunc(h.overdue)))
	mux.Handle("GET /tasks/{id}", h.authenticate(http.HandlerFunc(h.get)))
	mux.Handle("POST /tasks/{id}/complete", h.authenticate(http.HandlerFunc(h.complete)))
	mux.Handle("DELETE /tasks/{id}", h.authenticate(http.HandlerFunc(h.delete)))

	return mux
}

func (h *Handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")

		if !found || !strings.EqualFold(scheme, "bearer") {
			unauthorized(w)
			return
		}

		owner, err := h.service.Authenticate(token)
		if err != nil {
			unauthorized(w)
			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ownerContextKey, owner)))
	})
}

type taskResponse struct {
	ID        int64  `json:"id"`
	Owner     string `json:"owner"`
	Title     string `json:"title"`
	Done      bool   `json:"done"`
	DueAt     string `json:"due_at,omitempty"`
	CreatedAt string `json:"created_at"`
}

func toResponse(task todo.Task) taskResponse {
	response := taskResponse{
		ID:        task.ID,
		Owner:     task.Owner,
		Title:     task.Title,
		Done:      task.Done,
		CreatedAt: task.CreatedAt.Format(time.RFC3339),
	}

	if !task.DueAt.IsZero() {
		response.DueAt = task.DueAt.Format(time.RFC3339)
	}

	return response
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title string `json:"title"`
		DueAt string `json:"due_at"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	var dueAt time.Time

	if input.DueAt != "" {
		parsed, err := time.Parse(time.RFC3339, input.DueAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "due_at must be RFC3339")
			return
		}

		dueAt = parsed
	}

	task, err := h.service.Create(r.Context(), ownerFrom(r.Context()), input.Title, dueAt)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toResponse(task))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tasks, err := h.service.List(r.Context(), ownerFrom(r.Context()),
		r.URL.Query().Get("include_done") == "true")
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"tasks": responses(tasks), "count": len(tasks)})
}

func (h *Handler) overdue(w http.ResponseWriter, r *http.Request) {
	tasks, err := h.service.Overdue(r.Context(), ownerFrom(r.Context()))
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"tasks": responses(tasks), "count": len(tasks)})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	task, err := h.service.Get(r.Context(), ownerFrom(r.Context()), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toResponse(task))
}

func (h *Handler) complete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	task, err := h.service.Complete(r.Context(), ownerFrom(r.Context()), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toResponse(task))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if err := h.service.Delete(r.Context(), ownerFrom(r.Context()), id); err != nil {
		respondError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func responses(tasks []todo.Task) []taskResponse {
	result := make([]taskResponse, 0, len(tasks))

	for _, task := range tasks {
		result = append(result, toResponse(task))
	}

	return result
}

func respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, todo.ErrUnauthenticated):
		unauthorized(w)

	case errors.Is(err, todo.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")

	case errors.Is(err, todo.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")

	case errors.Is(err, todo.ErrValidation):
		writeError(w, http.StatusUnprocessableEntity,
			strings.TrimPrefix(err.Error(), "invalid task: "))

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
