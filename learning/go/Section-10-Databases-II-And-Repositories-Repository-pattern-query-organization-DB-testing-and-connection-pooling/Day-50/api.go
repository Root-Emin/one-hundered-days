package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

/*
HTTP layer: routing, DTO mapping and error translation.

Grep this file for "SELECT" or "sql." and you will find nothing but the health
check's Ping. That is the review criterion from today's lesson: handlers
contain no raw SQL.
*/

type API struct {
	shop *ShopService
	db   *sql.DB // health check only
}

func NewAPI(shop *ShopService, db *sql.DB) *API {
	return &API{shop: shop, db: db}
}

func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("POST /customers", a.createCustomer)
	mux.HandleFunc("GET /customers/{id}", a.getCustomer)
	mux.HandleFunc("GET /customers/{id}/orders", a.listOrders)
	mux.HandleFunc("POST /products", a.createProduct)
	mux.HandleFunc("GET /products", a.listProducts)
	mux.HandleFunc("POST /orders", a.placeOrder)
	mux.HandleFunc("GET /orders/{id}", a.getOrder)
	mux.HandleFunc("POST /orders/{id}/cancel", a.cancelOrder)
	mux.HandleFunc("POST /orders/{id}/ship", a.shipOrder)

	return logging(recovery(mux))
}

//
// DTOs
//

type customerResponse struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

type productResponse struct {
	ID      string `json:"id"`
	SKU     string `json:"sku"`
	Name    string `json:"name"`
	Price   string `json:"price"`
	Stock   int    `json:"stock"`
	InStock bool   `json:"in_stock"`
}

type orderItemResponse struct {
	ProductID string `json:"product_id"`
	Quantity  int    `json:"quantity"`
	UnitPrice string `json:"unit_price"`
	LineTotal string `json:"line_total"`
}

type orderResponse struct {
	ID         string              `json:"id"`
	CustomerID string              `json:"customer_id"`
	Status     string              `json:"status"`
	Total      string              `json:"total"`
	CreatedAt  string              `json:"created_at"`
	Items      []orderItemResponse `json:"items"`
}

func toCustomerResponse(customer Customer) customerResponse {
	return customerResponse{
		ID:        strconv.FormatInt(customer.ID, 10),
		Email:     customer.Email,
		Name:      customer.Name,
		CreatedAt: customer.CreatedAt.Format(time.RFC3339),
	}
}

func toProductResponse(product Product) productResponse {
	return productResponse{
		ID:      strconv.FormatInt(product.ID, 10),
		SKU:     product.SKU,
		Name:    product.Name,
		Price:   money(product.PriceCents),
		Stock:   product.Stock,
		InStock: product.Stock > 0,
	}
}

func toOrderResponse(order Order) orderResponse {
	items := make([]orderItemResponse, 0, len(order.Items))

	for _, item := range order.Items {
		items = append(items, orderItemResponse{
			ProductID: strconv.FormatInt(item.ProductID, 10),
			Quantity:  item.Quantity,
			UnitPrice: money(item.UnitCents),
			LineTotal: money(item.LineTotal()),
		})
	}

	return orderResponse{
		ID:         strconv.FormatInt(order.ID, 10),
		CustomerID: strconv.FormatInt(order.CustomerID, 10),
		Status:     string(order.Status),
		Total:      money(order.TotalCents),
		CreatedAt:  order.CreatedAt.Format(time.RFC3339),
		Items:      items,
	}
}

func money(cents int64) string {
	return strconv.FormatFloat(float64(cents)/100, 'f', 2, 64)
}

//
// HANDLERS
//

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	if err := a.db.PingContext(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unreachable")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) createCustomer(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	customer, err := a.shop.RegisterCustomer(r.Context(), input.Email, input.Name)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toCustomerResponse(customer))
}

func (a *API) getCustomer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	customer, err := a.shop.Customer(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toCustomerResponse(customer))
}

func (a *API) createProduct(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SKU        string `json:"sku"`
		Name       string `json:"name"`
		PriceCents int64  `json:"price_cents"`
		Stock      int    `json:"stock"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	product, err := a.shop.AddProduct(r.Context(), Product{
		SKU:        input.SKU,
		Name:       input.Name,
		PriceCents: input.PriceCents,
		Stock:      input.Stock,
	})
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toProductResponse(product))
}

func (a *API) listProducts(w http.ResponseWriter, r *http.Request) {
	products, err := a.shop.Products(r.Context(),
		queryInt(r, "limit", 20), queryInt(r, "offset", 0))
	if err != nil {
		respondError(w, err)
		return
	}

	responses := make([]productResponse, 0, len(products))

	for _, product := range products {
		responses = append(responses, toProductResponse(product))
	}

	writeJSON(w, http.StatusOK, map[string]any{"products": responses, "count": len(responses)})
}

func (a *API) placeOrder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CustomerID int64 `json:"customer_id"`
		Lines      []struct {
			ProductID int64 `json:"product_id"`
			Quantity  int   `json:"quantity"`
		} `json:"lines"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	lines := make([]OrderLine, 0, len(input.Lines))

	for _, line := range input.Lines {
		lines = append(lines, OrderLine{ProductID: line.ProductID, Quantity: line.Quantity})
	}

	order, err := a.shop.PlaceOrder(r.Context(), input.CustomerID, lines)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toOrderResponse(order))
}

func (a *API) getOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	order, err := a.shop.Order(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOrderResponse(order))
}

func (a *API) listOrders(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	orders, err := a.shop.Orders(r.Context(), id, queryInt(r, "limit", 20), queryInt(r, "offset", 0))
	if err != nil {
		respondError(w, err)
		return
	}

	responses := make([]orderResponse, 0, len(orders))

	for _, order := range orders {
		responses = append(responses, toOrderResponse(order))
	}

	writeJSON(w, http.StatusOK, map[string]any{"orders": responses, "count": len(responses)})
}

func (a *API) cancelOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	order, err := a.shop.CancelOrder(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOrderResponse(order))
}

func (a *API) shipOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	order, err := a.shop.ShipOrder(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOrderResponse(order))
}

//
// HELPERS
//

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}

	return id, true
}

func queryInt(r *http.Request, key string, fallback int) int {
	parsed, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return fallback
	}

	return parsed
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	defer func() {
		if err := r.Body.Close(); err != nil {
			log.Printf("close request body: %v", err)
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

// respondError is the single translation point between domain errors and
// status codes. Internal errors are logged in full and reported as a generic
// message: a SQL error text in an HTTP response is an information leak.
func respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")

	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "already exists")

	case errors.Is(err, ErrOutOfStock):
		writeError(w, http.StatusConflict, "insufficient stock")

	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusUnprocessableEntity, cleanValidationMessage(err))

	default:
		log.Printf("internal error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

func cleanValidationMessage(err error) string {
	message := err.Error()

	if _, after, found := strings.Cut(message, "validation failed: "); found {
		return after
	}

	return message
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
// MIDDLEWARE
//

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(recorder, r)

		log.Printf("method=%s path=%s status=%d duration=%s",
			r.Method, r.URL.Path, recorder.status, time.Since(start).Round(time.Microsecond))
	})
}

func recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("panic: %v", recovered)
				writeError(w, http.StatusInternalServerError, "internal server error")
			}
		}()

		next.ServeHTTP(w, r)
	})
}
