// Package httpapi is the transport layer: it turns HTTP requests into service
// calls and service results into HTTP responses.
//
// It is the outermost layer. It imports the service and the domain; nothing
// imports it except cmd/api. Everything it does is parse, delegate, render -
// there is no business rule in this package.
package httpapi

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-56/internal/domain"
	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-56/internal/service"
)

type Handler struct {
	tasks *service.TaskService
}

func NewHandler(tasks *service.TaskService) *Handler {
	return &Handler{tasks: tasks}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /tasks", h.list)
	mux.HandleFunc("POST /tasks", h.create)
	mux.HandleFunc("GET /tasks/overdue", h.overdue)
	mux.HandleFunc("GET /tasks/{id}", h.get)
	mux.HandleFunc("POST /tasks/{id}/status", h.advance)
	mux.HandleFunc("DELETE /tasks/{id}", h.delete)

	return logging(mux)
}

//
// DTOs: the wire format, owned by this package
//

type taskResponse struct {
	ID        int64  `json:"id"`
	Reference string `json:"reference"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Assignee  string `json:"assignee,omitempty"`
	DueAt     string `json:"due_at,omitempty"`
	CreatedAt string `json:"created_at"`
}

// toResponse keeps the JSON contract separate from the domain type, so
// renaming a domain field does not break every client.
func toResponse(task domain.Task) taskResponse {
	response := taskResponse{
		ID:        task.ID,
		Reference: task.Reference,
		Title:     task.Title,
		Status:    string(task.Status),
		Assignee:  task.Assignee,
		CreatedAt: task.CreatedAt.Format(time.RFC3339),
	}

	if !task.DueAt.IsZero() {
		response.DueAt = task.DueAt.Format(time.RFC3339)
	}

	return response
}

func toResponses(tasks []domain.Task) []taskResponse {
	responses := make([]taskResponse, 0, len(tasks))

	for _, task := range tasks {
		responses = append(responses, toResponse(task))
	}

	return responses
}

//
// HANDLERS: parse, call, render
//

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Reference string `json:"reference"`
		Title     string `json:"title"`
		Assignee  string `json:"assignee"`
		DueAt     string `json:"due_at"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	var dueAt time.Time

	if input.DueAt != "" {
		parsed, err := time.Parse(time.RFC3339, input.DueAt)
		if err != nil {
			// A parse failure is a transport concern, so it is handled here
			// rather than being pushed into the service.
			writeError(w, http.StatusBadRequest, "due_at must be RFC3339")
			return
		}

		dueAt = parsed
	}

	task, err := h.tasks.CreateTask(r.Context(), input.Reference, input.Title, input.Assignee, dueAt)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toResponse(task))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	status := domain.Status(r.URL.Query().Get("status"))

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	tasks, err := h.tasks.List(r.Context(), status, limit)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"tasks": toResponses(tasks), "count": len(tasks)})
}

func (h *Handler) overdue(w http.ResponseWriter, r *http.Request) {
	tasks, err := h.tasks.Overdue(r.Context(), 100)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"tasks": toResponses(tasks), "count": len(tasks)})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	task, err := h.tasks.Task(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toResponse(task))
}

func (h *Handler) advance(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var input struct {
		Status string `json:"status"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	task, err := h.tasks.Advance(r.Context(), id, domain.Status(input.Status))
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

	if err := h.tasks.Delete(r.Context(), id); err != nil {
		respondError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

//
// TRANSLATION
//

// respondError is the one place that knows how a domain error becomes a
// status code. The service returns meaning; this maps meaning to protocol.
func respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")

	case errors.Is(err, domain.ErrConflict):
		writeError(w, http.StatusConflict, "already exists")

	case errors.Is(err, domain.ErrValidation):
		writeError(w, http.StatusUnprocessableEntity,
			strings.TrimPrefix(err.Error(), "invalid task: "))

	default:
		log.Printf("internal error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
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

		log.Printf("method=%s path=%s duration=%s",
			r.Method, r.URL.Path, time.Since(start).Round(time.Microsecond))
	})
}
