package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"
)

/*
Thin handlers.

Each one does exactly three things: parse the request, call the service, render
the result. There is no branching on business state, no recomputing of totals,
no validation beyond "is this JSON". Everything a handler could be tempted to
decide has already been decided one layer down.

The one thing this layer does own is translation: domain errors in, HTTP
statuses and JSON out.
*/

type Handler struct {
	orders *OrderService
}

func NewHandler(orders *OrderService) *Handler {
	return &Handler{orders: orders}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /orders", h.create)
	mux.HandleFunc("GET /orders", h.list)
	mux.HandleFunc("GET /orders/{id}", h.get)
	mux.HandleFunc("POST /orders/{id}/lines", h.addLine)
	mux.HandleFunc("POST /orders/{id}/submit", h.submit)
	mux.HandleFunc("POST /orders/{id}/pay", h.pay)
	mux.HandleFunc("POST /orders/{id}/cancel", h.cancel)

	return mux
}

//
// WIRE FORMAT
//

type lineResponse struct {
	SKU       string `json:"sku"`
	Quantity  int    `json:"quantity"`
	UnitPrice string `json:"unit_price"`
	LineTotal string `json:"line_total"`
}

type orderResponse struct {
	ID        int64          `json:"id"`
	Customer  string         `json:"customer_email"`
	State     string         `json:"state"`
	Lines     []lineResponse `json:"lines"`
	Total     string         `json:"total"`
	CreatedAt string         `json:"created_at"`
}

// toResponse asks the entity for its values. Note that Total is not computed
// here: the transport layer must never own a business calculation.
func toResponse(order *Order) orderResponse {
	lines := make([]lineResponse, 0, len(order.Lines()))

	for _, line := range order.Lines() {
		lines = append(lines, lineResponse{
			SKU:       line.SKU.String(),
			Quantity:  line.Quantity.Int(),
			UnitPrice: line.UnitPrice.String(),
			LineTotal: line.Total().String(),
		})
	}

	return orderResponse{
		ID:        order.ID(),
		Customer:  order.Customer().String(),
		State:     string(order.State()),
		Lines:     lines,
		Total:     order.Total().String(),
		CreatedAt: order.CreatedAt().Format(time.RFC3339),
	}
}

//
// HANDLERS
//

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CustomerEmail string `json:"customer_email"`
		Lines         []struct {
			SKU      string `json:"sku"`
			Quantity int    `json:"quantity"`
		} `json:"lines"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	lines := make([]LineRequest, 0, len(input.Lines))

	for _, line := range input.Lines {
		lines = append(lines, LineRequest{SKU: line.SKU, Quantity: line.Quantity})
	}

	order, err := h.orders.CreateOrder(r.Context(), input.CustomerEmail, lines)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toResponse(order))
}

func (h *Handler) addLine(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var input struct {
		SKU      string `json:"sku"`
		Quantity int    `json:"quantity"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	order, err := h.orders.AddLine(r.Context(), id, LineRequest{SKU: input.SKU, Quantity: input.Quantity})
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toResponse(order))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	order, err := h.orders.Order(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toResponse(order))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	orders, err := h.orders.List(r.Context(), OrderState(r.URL.Query().Get("state")))
	if err != nil {
		respondError(w, err)
		return
	}

	responses := make([]orderResponse, 0, len(orders))

	for _, order := range orders {
		responses = append(responses, toResponse(order))
	}

	writeJSON(w, http.StatusOK, map[string]any{"orders": responses, "count": len(responses)})
}

func (h *Handler) submit(w http.ResponseWriter, r *http.Request) {
	h.workflow(w, r, h.orders.Submit)
}

func (h *Handler) pay(w http.ResponseWriter, r *http.Request) {
	h.workflow(w, r, h.orders.Pay)
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	h.workflow(w, r, h.orders.Cancel)
}

// workflow is how thin a state-transition handler can get: parse an id, call
// one service method, render. Three endpoints, one function.
func (h *Handler) workflow(w http.ResponseWriter, r *http.Request, action func(ctx context.Context, id int64) (*Order, error)) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	order, err := action(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toResponse(order))
}

//
// ERROR TRANSLATION
//

type errorResponse struct {
	Error  string        `json:"error"`
	Fields []fieldDetail `json:"fields,omitempty"`
}

type fieldDetail struct {
	Field   string `json:"field"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// respondError is the only place that knows about HTTP status codes. Typed
// errors make it a switch on meaning rather than on error strings, and let a
// validation failure carry per-field detail into the response.
func respondError(w http.ResponseWriter, err error) {
	var validation *ValidationError

	if errors.As(err, &validation) {
		fields := make([]fieldDetail, 0, len(validation.Fields))

		for _, field := range validation.Fields {
			fields = append(fields, fieldDetail{Field: field.Field, Rule: field.Rule, Message: field.Message})
		}

		writeJSON(w, http.StatusUnprocessableEntity, errorResponse{
			Error:  "validation failed",
			Fields: fields,
		})

		return
	}

	var fieldError FieldError

	if errors.As(err, &fieldError) {
		writeJSON(w, http.StatusUnprocessableEntity, errorResponse{
			Error: "validation failed",
			Fields: []fieldDetail{
				{Field: fieldError.Field, Rule: fieldError.Rule, Message: fieldError.Message},
			},
		})

		return
	}

	var notFound NotFoundError

	if errors.As(err, &notFound) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: notFound.Error()})

		return
	}

	var stateError StateError

	if errors.As(err, &stateError) {
		// 409, not 422: the request is fine, the entity is in the wrong state.
		writeJSON(w, http.StatusConflict, errorResponse{Error: stateError.Error()})

		return
	}

	var duplicate DuplicateError

	if errors.As(err, &duplicate) {
		writeJSON(w, http.StatusConflict, errorResponse{Error: duplicate.Error()})

		return
	}

	log.Printf("internal error: %v", err)
	writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
}

//
// PLUMBING
//

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid id"})

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
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body"})

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
