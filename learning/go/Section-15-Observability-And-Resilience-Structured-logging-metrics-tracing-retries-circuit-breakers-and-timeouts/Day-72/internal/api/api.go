// Package api is a small service instrumented with the metrics package.
package api

import (
	"encoding/json"
	"errors"
	"log"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-72/internal/metrics"
)

type Order struct {
	ID       int64
	Customer string
	Cents    int64
}

type API struct {
	metrics *metrics.Metrics

	mu     sync.RWMutex
	orders map[int64]Order
	nextID int64

	// failureRate drives the demo: a fraction of database calls fail, so the
	// error counter and the latency histogram have something to show.
	failureRate float64
	slowRate    float64
}

func New(recorder *metrics.Metrics) *API {
	return &API{
		metrics: recorder,
		orders:  make(map[int64]Order),
		nextID:  1,
	}
}

func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	// The metrics endpoint is NOT wrapped in the middleware: a scrape is not
	// user traffic, and counting it would pollute the request rate.
	mux.Handle("GET /metrics", a.metrics.Handler())

	instrumented := http.NewServeMux()

	instrumented.HandleFunc("GET /healthz", a.health)
	instrumented.HandleFunc("POST /orders", a.createOrder)
	instrumented.HandleFunc("GET /orders/{id}", a.getOrder)
	instrumented.HandleFunc("GET /orders", a.listOrders)
	instrumented.HandleFunc("POST /debug/chaos", a.chaos)

	mux.Handle("/", a.metrics.Middleware(instrumented))

	return mux
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) createOrder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Customer string `json:"customer"`
		Cents    int64  `json:"cents"`
	}

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		a.metrics.OrdersTotal.WithLabelValues("rejected").Inc()
		writeError(w, http.StatusBadRequest, "invalid JSON body")

		return
	}

	if strings.TrimSpace(input.Customer) == "" || input.Cents <= 0 {
		a.metrics.OrdersTotal.WithLabelValues("rejected").Inc()
		writeError(w, http.StatusUnprocessableEntity, "customer and a positive amount are required")

		return
	}

	var order Order

	// Every database call goes through ObserveDatabase, so the timing and the
	// error classification stay consistent across the codebase.
	err := a.metrics.ObserveDatabase("insert_order", func() error {
		return a.simulateWork(func() {
			a.mu.Lock()
			defer a.mu.Unlock()

			order = Order{ID: a.nextID, Customer: input.Customer, Cents: input.Cents}
			a.orders[order.ID] = order
			a.nextID++
		})
	})
	if err != nil {
		a.metrics.OrdersTotal.WithLabelValues("failed").Inc()
		log.Printf("create order: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")

		return
	}

	a.metrics.OrdersTotal.WithLabelValues("created").Inc()
	a.metrics.OrderValue.Observe(float64(order.Cents))
	a.metrics.QueueDepth.Set(float64(len(a.orders)))

	writeJSON(w, http.StatusCreated, map[string]any{"id": order.ID})
}

func (a *API) getOrder(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	var (
		order Order
		found bool
	)

	if err := a.metrics.ObserveDatabase("select_order", func() error {
		return a.simulateWork(func() {
			a.mu.RLock()
			defer a.mu.RUnlock()

			order, found = a.orders[id]
		})
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if !found {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id": order.ID, "customer": order.Customer, "cents": order.Cents,
	})
}

func (a *API) listOrders(w http.ResponseWriter, r *http.Request) {
	var orders []Order

	if err := a.metrics.ObserveDatabase("list_orders", func() error {
		return a.simulateWork(func() {
			a.mu.RLock()
			defer a.mu.RUnlock()

			for _, order := range a.orders {
				orders = append(orders, order)
			}
		})
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"count": len(orders)})
}

// chaos tunes the failure and slowness rates so the metrics have something
// interesting to show.
func (a *API) chaos(w http.ResponseWriter, r *http.Request) {
	failure, _ := strconv.ParseFloat(r.URL.Query().Get("failure_rate"), 64)
	slow, _ := strconv.ParseFloat(r.URL.Query().Get("slow_rate"), 64)

	a.mu.Lock()
	a.failureRate = clamp(failure)
	a.slowRate = clamp(slow)
	a.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]float64{
		"failure_rate": clamp(failure),
		"slow_rate":    clamp(slow),
	})
}

func clamp(value float64) float64 {
	switch {
	case value < 0:
		return 0
	case value > 1:
		return 1
	default:
		return value
	}
}

// simulateWork stands in for a real database call: some latency, sometimes an
// error.
func (a *API) simulateWork(work func()) error {
	a.mu.RLock()
	failureRate, slowRate := a.failureRate, a.slowRate
	a.mu.RUnlock()

	//nolint:gosec // math/rand is correct here: this is load shaping, not security
	if rand.Float64() < failureRate {
		time.Sleep(time.Duration(rand.IntN(3)) * time.Millisecond)

		return errors.New("connection reset by peer")
	}

	if rand.Float64() < slowRate {
		time.Sleep(time.Duration(50+rand.IntN(250)) * time.Millisecond)
	} else {
		time.Sleep(time.Duration(rand.IntN(3)) * time.Millisecond)
	}

	work()

	return nil
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
