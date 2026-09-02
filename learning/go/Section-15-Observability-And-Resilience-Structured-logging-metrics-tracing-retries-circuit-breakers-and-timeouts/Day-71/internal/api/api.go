// Package api shows the logging middleware and the level guidance in practice.
package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-71/internal/logging"
)

var (
	ErrNotFound   = errors.New("order not found")
	ErrValidation = errors.New("invalid order")
)

type Order struct {
	ID       int64
	Customer string
	Email    string
	Cents    int64
}

type Store struct {
	mu     sync.RWMutex
	orders map[int64]Order
	nextID int64

	// failNext makes the demo produce a real error path on request.
	failNext bool
}

func NewStore() *Store {
	return &Store{orders: make(map[int64]Order), nextID: 1}
}

func (s *Store) SetFailNext(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.failNext = fail
}

func (s *Store) Create(order Order) (Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.failNext {
		s.failNext = false

		return Order{}, errors.New("connection reset by peer")
	}

	order.ID = s.nextID
	s.orders[order.ID] = order
	s.nextID++

	return order, nil
}

func (s *Store) ByID(id int64) (Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	order, found := s.orders[id]
	if !found {
		return Order{}, ErrNotFound
	}

	return order, nil
}

type API struct {
	logger *slog.Logger
	store  *Store
}

func New(logger *slog.Logger, store *Store) *API {
	return &API{logger: logger, store: store}
}

func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("POST /orders", a.createOrder)
	mux.HandleFunc("GET /orders/{id}", a.getOrder)
	mux.HandleFunc("POST /debug/fail-next", a.failNext)

	return a.requestLogger(mux)
}

// requestLogger is the middleware every service needs: one line per request,
// with the fields that make a log searchable.
func (a *API) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Honour an upstream correlation id, or mint one. Either way every
		// line for this request carries the same value.
		requestID := r.Header.Get("X-Request-ID")

		if requestID == "" {
			requestID = newRequestID()
		}

		w.Header().Set("X-Request-ID", requestID)

		ctx := logging.WithRequestID(r.Context(), a.logger, requestID)

		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(recorder, r.WithContext(ctx))

		logger := logging.FromContext(ctx).With(
			slog.String(logging.FieldMethod, r.Method),
			slog.String(logging.FieldPath, r.URL.Path),
			slog.Int(logging.FieldStatus, recorder.status),
			logging.Duration(time.Since(start)),
		)

		// The level follows the outcome, not the code path:
		//   5xx -> error   something is broken on our side
		//   4xx -> warn    the caller did something wrong; interesting in bulk
		//   else -> info   the normal case
		switch {
		case recorder.status >= 500:
			logger.ErrorContext(ctx, "request failed")

		case recorder.status >= 400:
			logger.WarnContext(ctx, "request rejected")

		default:
			logger.InfoContext(ctx, "request completed")
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	// Debug: useful while chasing a problem, noise at any other time. A health
	// check logged at info level is how a log bill doubles.
	logging.FromContext(r.Context()).DebugContext(r.Context(), "health check")

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) createOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)

	var input struct {
		Customer string `json:"customer"`
		Email    string `json:"email"`
		Cents    int64  `json:"cents"`
		Password string `json:"password"` // accepted, never logged
	}

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		// The decode error is the caller's problem: warn, with the reason.
		logger.WarnContext(ctx, "invalid request body", slog.String(logging.FieldError, err.Error()))
		writeError(w, http.StatusBadRequest, "invalid JSON body")

		return
	}

	if strings.TrimSpace(input.Customer) == "" || input.Cents <= 0 {
		logger.WarnContext(ctx, "order rejected",
			slog.String("reason", "customer and a positive amount are required"))
		writeError(w, http.StatusUnprocessableEntity, "customer and cents are required")

		return
	}

	order, err := a.store.Create(Order{
		Customer: input.Customer,
		Email:    input.Email,
		Cents:    input.Cents,
	})
	if err != nil {
		// Our fault: error level, with the cause. The client gets a generic
		// message; the log gets the detail.
		logger.ErrorContext(ctx, "could not store order",
			slog.String(logging.FieldError, err.Error()),
			slog.String(logging.FieldComponent, "store"),
		)
		writeError(w, http.StatusInternalServerError, "internal error")

		return
	}

	// Note what is logged and what is not: the email goes through the masking
	// rule in the handler config, and the password is not passed at all.
	logger.InfoContext(ctx, "order created",
		slog.Int64("order_id", order.ID),
		slog.String("customer", order.Customer),
		slog.String("email", order.Email),
		slog.Int64("amount_cents", order.Cents),
	)

	writeJSON(w, http.StatusCreated, map[string]any{"id": order.ID})
}

func (a *API) getOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)

	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	order, err := a.store.ByID(id)
	if err != nil {
		// Not found is expected traffic, not a failure: warn at most, and
		// only because a spike of them is worth noticing.
		logger.WarnContext(ctx, "order not found", slog.Int64("order_id", id))
		writeError(w, http.StatusNotFound, "not found")

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":       order.ID,
		"customer": order.Customer,
		"cents":    order.Cents,
	})
}

func (a *API) failNext(w http.ResponseWriter, r *http.Request) {
	a.store.SetFailNext(true)

	logging.FromContext(r.Context()).WarnContext(r.Context(), "failure injection armed",
		slog.String(logging.FieldComponent, "debug"))

	writeJSON(w, http.StatusOK, map[string]string{"armed": "the next order creation will fail"})
}

func parseID(value string) (int64, error) {
	var id int64

	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, ErrValidation
		}

		id = id*10 + int64(char-'0')
	}

	if id <= 0 {
		return 0, ErrValidation
	}

	return id, nil
}

func newRequestID() string {
	raw := make([]byte, 8)

	if _, err := rand.Read(raw); err != nil {
		return "unknown"
	}

	return base64.RawURLEncoding.EncodeToString(raw)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("encode response", slog.String(logging.FieldError, err.Error()))
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
