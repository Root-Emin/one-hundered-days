// Package httpapi serves the catalog over HTTP.
//
// The routes here are mirrored by api/openapi.yaml, and internal/contract
// checks that the two agree. That check is the point of the package: a spec
// nobody verifies drifts from the implementation within a month, and a drifted
// spec is worse than no spec, because clients trust it.
//
// # Route table
//
// The Routes method is the single source of truth for what exists:
//
//	GET    /products          list products
//	POST   /products          add a product
//	GET    /products/{sku}    fetch one product
//	DELETE /products/{sku}    remove a product
//	POST   /products/{sku}/reservations   reserve stock
//	GET    /healthz           liveness
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-92/internal/catalog"
)

// API is the HTTP handler set for a catalog store.
type API struct {
	store  *catalog.Store
	logger *slog.Logger
}

// New returns an API backed by store. A nil logger is replaced by the default.
func New(store *catalog.Store, logger *slog.Logger) *API {
	if logger == nil {
		logger = slog.Default()
	}

	return &API{store: store, logger: logger}
}

// Route describes one endpoint, for the contract check and for documentation.
//
// Declaring the routes as data - rather than only as calls to mux.HandleFunc -
// is what lets a test compare them with the OpenAPI document. A route table
// that only exists as code can be read by humans and by nothing else.
type Route struct {
	// Method is the HTTP verb.
	Method string
	// Pattern is the Go 1.22 ServeMux pattern, e.g. "/products/{sku}".
	Pattern string
	// Summary is the one-line description; it must match the spec's summary.
	Summary string
	// Statuses are every status code this handler can return.
	Statuses []int

	handler http.HandlerFunc
}

// Routes returns the route table.
func (a *API) Routes() []Route {
	return []Route{
		{
			Method:   http.MethodGet,
			Pattern:  "/products",
			Summary:  "List products",
			Statuses: []int{http.StatusOK},
			handler:  a.listProducts,
		},
		{
			Method:   http.MethodPost,
			Pattern:  "/products",
			Summary:  "Add a product",
			Statuses: []int{http.StatusCreated, http.StatusBadRequest, http.StatusConflict},
			handler:  a.addProduct,
		},
		{
			Method:   http.MethodGet,
			Pattern:  "/products/{sku}",
			Summary:  "Fetch one product",
			Statuses: []int{http.StatusOK, http.StatusNotFound},
			handler:  a.getProduct,
		},
		{
			Method:   http.MethodDelete,
			Pattern:  "/products/{sku}",
			Summary:  "Remove a product",
			Statuses: []int{http.StatusNoContent, http.StatusNotFound},
			handler:  a.deleteProduct,
		},
		{
			Method:   http.MethodPost,
			Pattern:  "/products/{sku}/reservations",
			Summary:  "Reserve stock",
			Statuses: []int{http.StatusOK, http.StatusBadRequest, http.StatusNotFound, http.StatusConflict},
			handler:  a.reserveStock,
		},
		{
			Method:   http.MethodGet,
			Pattern:  "/healthz",
			Summary:  "Liveness probe",
			Statuses: []int{http.StatusOK},
			handler:  a.health,
		},
	}
}

// Handler builds a ServeMux from the route table.
func (a *API) Handler() *http.ServeMux {
	mux := http.NewServeMux()

	for _, route := range a.Routes() {
		mux.HandleFunc(route.Method+" "+route.Pattern, route.handler)
	}

	return mux
}

// ErrorResponse is the body returned with every non-2xx status.
//
// One shape for every error means a client writes one decoder. Different
// shapes per endpoint means the client writes five, and gets one wrong.
type ErrorResponse struct {
	// Error is a stable, machine-readable code: not_found, conflict,
	// invalid_request. Clients switch on this.
	Error string `json:"error"`
	// Message is for humans and may change between releases.
	Message string `json:"message"`
}

func (a *API) listProducts(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.store.List())
}

func (a *API) addProduct(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SKU       string `json:"sku"`
		Name      string `json:"name"`
		PriceCent int64  `json:"price_cent"`
		Stock     int    `json:"stock"`
	}

	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())

		return
	}

	product, err := a.store.Add(catalog.Product{
		SKU: body.SKU, Name: body.Name, PriceCent: body.PriceCent, Stock: body.Stock,
	})
	if err != nil {
		a.writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusCreated, product)
}

func (a *API) getProduct(w http.ResponseWriter, r *http.Request) {
	product, err := a.store.Get(r.PathValue("sku"))
	if err != nil {
		a.writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, product)
}

func (a *API) deleteProduct(w http.ResponseWriter, r *http.Request) {
	if err := a.store.Delete(r.PathValue("sku")); err != nil {
		a.writeStoreError(w, err)

		return
	}

	// 204: the resource is gone and there is nothing to say about it.
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) reserveStock(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Quantity int `json:"quantity"`
	}

	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())

		return
	}

	product, err := a.store.Reserve(r.PathValue("sku"), body.Quantity)
	if err != nil {
		a.writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, product)
}

func (a *API) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// writeStoreError maps domain errors onto status codes in one place.
//
// Scattering this mapping across handlers is how the same failure ends up as a
// 400 on one endpoint and a 500 on another - and the client cannot tell which
// errors are worth retrying.
func (a *API) writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())

	case errors.Is(err, catalog.ErrDuplicateSKU):
		writeError(w, http.StatusConflict, "conflict", err.Error())

	case errors.Is(err, catalog.ErrInsufficientStock):
		// 409, not 400: the request was well formed, the state said no.
		writeError(w, http.StatusConflict, "conflict", err.Error())

	case errors.Is(err, catalog.ErrInvalidProduct):
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())

	default:
		a.logger.Error("unhandled store error", slog.String("error", err.Error()))

		writeError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

func decode(r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}

	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// The status line is already written; there is nothing left to say.
		_ = err
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorResponse{Error: code, Message: message})
}
