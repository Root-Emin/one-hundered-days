package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

/*
The HTTP boundary: DTOs and handlers.

The domain type never leaves this file as-is. Mapping to a DTO here is what
stops two things from happening:

  - internal data (cost_cents, and with it the company's margin) leaking into
    a public JSON response
  - database column names becoming a public API contract that can never be
    renamed again
*/

type productResponse struct {
	ID        string  `json:"id"`
	SKU       string  `json:"sku"`
	Name      string  `json:"name"`
	Price     string  `json:"price"`
	InStock   bool    `json:"in_stock"`
	Stock     int     `json:"stock"`
	CreatedAt string  `json:"created_at"`
	Margin    float64 `json:"-"` // internal reporting only, never serialized
}

func toProductResponse(product Product) productResponse {
	return productResponse{
		// IDs go out as strings: JSON numbers lose precision above 2^53 and
		// clients should treat identifiers as opaque anyway.
		ID:      strconv.FormatInt(product.ID, 10),
		SKU:     product.SKU,
		Name:    product.Name,
		Price:   formatMoney(product.PriceCent),
		InStock: product.Stock > 0,
		Stock:   product.Stock,
		// cost_cents is deliberately absent.
		CreatedAt: product.CreatedAt.Format(time.RFC3339),
		Margin:    product.MarginPercent(),
	}
}

func toProductResponses(products []Product) []productResponse {
	responses := make([]productResponse, 0, len(products))

	for _, product := range products {
		responses = append(responses, toProductResponse(product))
	}

	return responses
}

type createProductRequest struct {
	SKU       string `json:"sku"`
	Name      string `json:"name"`
	PriceCent int64  `json:"price_cents"`
	CostCents int64  `json:"cost_cents"`
	Stock     int    `json:"stock"`
}

func (r createProductRequest) toDomain() Product {
	return Product{
		SKU:       r.SKU,
		Name:      r.Name,
		PriceCent: r.PriceCent,
		CostCents: r.CostCents,
		Stock:     r.Stock,
	}
}

func formatMoney(cents int64) string {
	return strconv.FormatFloat(float64(cents)/100, 'f', 2, 64)
}

//
// HANDLERS
//

// ProductHandler receives its service through a constructor. Swapping the
// storage engine happens in main.go, not here.
type ProductHandler struct {
	catalog *CatalogService
}

func NewProductHandler(catalog *CatalogService) *ProductHandler {
	return &ProductHandler{catalog: catalog}
}

func (h *ProductHandler) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /products", h.create)
	mux.HandleFunc("GET /products", h.list)
	mux.HandleFunc("GET /products/{id}", h.get)
	mux.HandleFunc("POST /products/{id}/sell", h.sell)
	mux.HandleFunc("DELETE /products/{id}", h.delete)

	return mux
}

func (h *ProductHandler) create(w http.ResponseWriter, r *http.Request) {
	var input createProductRequest

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	product, err := h.catalog.AddProduct(r.Context(), input.toDomain())
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toProductResponse(product))
}

func (h *ProductHandler) list(w http.ResponseWriter, r *http.Request) {
	filter := ProductFilter{
		InStockOnly: r.URL.Query().Get("in_stock") == "true",
		Limit:       atoiOr(r.URL.Query().Get("limit"), 20),
		Offset:      atoiOr(r.URL.Query().Get("offset"), 0),
	}

	products, err := h.catalog.Catalog(r.Context(), filter)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"products": toProductResponses(products),
		"count":    len(products),
	})
}

func (h *ProductHandler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	product, err := h.catalog.Get(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toProductResponse(product))
}

func (h *ProductHandler) sell(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var input struct {
		Quantity int `json:"quantity"`
	}

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	product, err := h.catalog.Sell(r.Context(), id, input.Quantity)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toProductResponse(product))
}

func (h *ProductHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if err := h.catalog.Remove(r.Context(), id); err != nil {
		respondError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

//
// HTTP HELPERS
//

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}

	return id, true
}

func atoiOr(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return parsed
}

// respondError maps domain errors, not driver errors: the handler has no idea
// SQL exists, which is exactly the point of the repository pattern.
func respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")

	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "sku already exists")

	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusUnprocessableEntity, strings.TrimPrefix(err.Error(), "invalid product: "))

	default:
		log.Printf("internal error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
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
