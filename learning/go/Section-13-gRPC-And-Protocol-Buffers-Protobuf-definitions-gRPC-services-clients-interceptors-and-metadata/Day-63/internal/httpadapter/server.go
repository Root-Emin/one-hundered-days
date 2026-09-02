// Package httpadapter exposes the same inventory service over HTTP/JSON.
//
// Compare this file with grpcadapter/server.go: the two are the same shape,
// call the same methods, and differ only in how they express "not found".
// That symmetry is what "transport is not business logic" looks like in code.
package httpadapter

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-63/internal/inventory"
)

type Handler struct {
	service *inventory.Service
}

func NewHandler(service *inventory.Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /items", h.list)
	mux.HandleFunc("POST /items", h.create)
	mux.HandleFunc("GET /items/{sku}", h.get)
	mux.HandleFunc("POST /items/{sku}/adjust", h.adjust)
	mux.HandleFunc("DELETE /items/{sku}", h.delete)

	return mux
}

type itemResponse struct {
	SKU       string `json:"sku"`
	Name      string `json:"name"`
	Quantity  int32  `json:"quantity"`
	Location  string `json:"location,omitempty"`
	UpdatedAt string `json:"updated_at"`
}

func toResponse(item inventory.Item) itemResponse {
	return itemResponse{
		SKU:       item.SKU,
		Name:      item.Name,
		Quantity:  item.Quantity,
		Location:  item.Location,
		UpdatedAt: item.UpdatedAt.Format(time.RFC3339),
	}
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.Get(r.Context(), r.PathValue("sku"))
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toResponse(item))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))

	items, next, total, err := h.service.List(r.Context(),
		r.URL.Query().Get("location"), int32(pageSize), r.URL.Query().Get("page_token"))
	if err != nil {
		respondError(w, err)
		return
	}

	responses := make([]itemResponse, 0, len(items))

	for _, item := range items {
		responses = append(responses, toResponse(item))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items":           responses,
		"next_page_token": next,
		"total_size":      total,
	})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SKU       string `json:"sku"`
		Name      string `json:"name"`
		Quantity  int32  `json:"quantity"`
		Location  string `json:"location"`
		RequestID string `json:"request_id"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	item, err := h.service.Create(r.Context(), inventory.Item{
		SKU: input.SKU, Name: input.Name, Quantity: input.Quantity, Location: input.Location,
	}, input.RequestID)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toResponse(item))
}

func (h *Handler) adjust(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Delta     int32  `json:"delta"`
		Reason    string `json:"reason"`
		RequestID string `json:"request_id"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	item, previous, err := h.service.Adjust(r.Context(),
		r.PathValue("sku"), input.Delta, input.Reason, input.RequestID)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"item":              toResponse(item),
		"previous_quantity": previous,
	})
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	deleted, err := h.service.Delete(r.Context(), r.PathValue("sku"))
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"deleted": deleted})
}

// respondError maps the same domain errors the gRPC adapter maps, to the HTTP
// equivalents.
func respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, inventory.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")

	case errors.Is(err, inventory.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "already exists")

	case errors.Is(err, inventory.ErrValidation):
		writeError(w, http.StatusUnprocessableEntity, err.Error())

	case errors.Is(err, inventory.ErrInsufficient):
		writeError(w, http.StatusConflict, err.Error())

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
