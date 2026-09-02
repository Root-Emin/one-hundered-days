// Package httpapi is the transport layer: parse, delegate, render.
package httpapi

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-60/internal/domain"
	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-60/internal/service"
)

type Handler struct {
	library *service.Library
}

func New(library *service.Library) *Handler {
	return &Handler{library: library}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /books", h.add)
	mux.HandleFunc("GET /books", h.list)
	mux.HandleFunc("GET /books/{id}", h.get)
	mux.HandleFunc("POST /books/{id}/start", h.start)
	mux.HandleFunc("POST /books/{id}/progress", h.progress)
	mux.HandleFunc("POST /books/{id}/abandon", h.abandon)
	mux.HandleFunc("DELETE /books/{id}", h.remove)
	mux.HandleFunc("GET /stats", h.stats)

	return mux
}

type bookResponse struct {
	ID          int64  `json:"id"`
	ISBN        string `json:"isbn"`
	Title       string `json:"title"`
	Author      string `json:"author"`
	Pages       int    `json:"pages"`
	Status      string `json:"status"`
	Progress    int    `json:"progress"`
	PercentRead int    `json:"percent_read"`
	AddedAt     string `json:"added_at"`
	FinishedAt  string `json:"finished_at,omitempty"`
}

func toResponse(book domain.Book) bookResponse {
	response := bookResponse{
		ID:          book.ID,
		ISBN:        book.ISBN.String(),
		Title:       book.Title,
		Author:      book.Author,
		Pages:       book.Pages,
		Status:      string(book.Status),
		Progress:    book.Progress,
		PercentRead: book.PercentRead(), // asked of the domain, not recomputed
		AddedAt:     book.AddedAt.Format(time.RFC3339),
	}

	if !book.FinishedAt.IsZero() {
		response.FinishedAt = book.FinishedAt.Format(time.RFC3339)
	}

	return response
}

func (h *Handler) add(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ISBN   string `json:"isbn"`
		Title  string `json:"title"`
		Author string `json:"author"`
		Pages  int    `json:"pages"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	book, err := h.library.AddBook(r.Context(), input.ISBN, input.Title, input.Author, input.Pages)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toResponse(book))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	books, err := h.library.List(r.Context(), domain.Status(r.URL.Query().Get("status")), limit, offset)
	if err != nil {
		respondError(w, err)
		return
	}

	responses := make([]bookResponse, 0, len(books))

	for _, book := range books {
		responses = append(responses, toResponse(book))
	}

	writeJSON(w, http.StatusOK, map[string]any{"books": responses, "count": len(responses)})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	book, err := h.library.Book(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toResponse(book))
}

func (h *Handler) start(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	book, err := h.library.Start(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toResponse(book))
}

func (h *Handler) progress(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var input struct {
		Page int64 `json:"page"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	book, err := h.library.RecordProgress(r.Context(), id, input.Page)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toResponse(book))
}

func (h *Handler) abandon(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	book, err := h.library.Abandon(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toResponse(book))
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if err := h.library.Remove(r.Context(), id); err != nil {
		respondError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) stats(w http.ResponseWriter, r *http.Request) {
	stats, percent, err := h.library.Progress(r.Context())
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total":        stats.Total,
		"reading":      stats.Reading,
		"finished":     stats.Finished,
		"pages_read":   stats.PagesRead,
		"pages_total":  stats.PagesTotal,
		"percent_read": percent,
	})
}

//
// TRANSLATION
//

func respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")

	case errors.Is(err, domain.ErrConflict):
		writeError(w, http.StatusConflict, clean(err, "conflict: "))

	case errors.Is(err, domain.ErrValidation):
		writeError(w, http.StatusUnprocessableEntity, clean(err, "validation failed: "))

	default:
		log.Printf("internal error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

func clean(err error, prefix string) string {
	if _, after, found := strings.Cut(err.Error(), prefix); found {
		return after
	}

	return err.Error()
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
