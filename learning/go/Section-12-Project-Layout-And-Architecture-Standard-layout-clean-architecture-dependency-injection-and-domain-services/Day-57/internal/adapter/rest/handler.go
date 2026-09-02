// Package rest is a driving adapter: HTTP comes in, use cases get called,
// JSON goes out.
//
// It imports the use cases and the domain. Nothing imports it except the
// binary. Replacing REST with gRPC (Section 13) means adding a sibling
// package, not touching a single business rule.
package rest

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-57/internal/domain"
	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-57/internal/usecase"
)

type Handler struct {
	subscriptions *usecase.Subscriptions
}

func NewHandler(subscriptions *usecase.Subscriptions) *Handler {
	return &Handler{subscriptions: subscriptions}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /subscriptions", h.subscribe)
	mux.HandleFunc("GET /subscriptions/{id}", h.get)
	mux.HandleFunc("PUT /subscriptions/{id}/plan", h.changePlan)
	mux.HandleFunc("POST /subscriptions/{id}/activate", h.activate)
	mux.HandleFunc("DELETE /subscriptions/{id}", h.cancel)
	mux.HandleFunc("GET /revenue", h.revenue)

	return mux
}

//
// WIRE FORMAT
//

type subscriptionResponse struct {
	ID         int64  `json:"id"`
	CustomerID string `json:"customer_id"`
	Plan       string `json:"plan"`
	State      string `json:"state"`
	StartedAt  string `json:"started_at"`
	TrialEnds  string `json:"trial_ends,omitempty"`
	Charge     string `json:"monthly_charge"`
}

func (h *Handler) toResponse(subscription domain.Subscription) subscriptionResponse {
	response := subscriptionResponse{
		ID:         subscription.ID,
		CustomerID: subscription.CustomerID,
		Plan:       string(subscription.Plan),
		State:      string(subscription.State),
		StartedAt:  subscription.StartedAt.Format(time.RFC3339),
	}

	if !subscription.TrialEnds.IsZero() {
		response.TrialEnds = subscription.TrialEnds.Format(time.RFC3339)
	}

	// The price is asked of the entity, not recomputed here. A pricing change
	// must never require editing the transport layer.
	if charge, err := subscription.MonthlyCharge("EUR", subscription.StartedAt); err == nil {
		response.Charge = charge.String()
	}

	return response
}

//
// HANDLERS: thin by construction
//

func (h *Handler) subscribe(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CustomerID string `json:"customer_id"`
		Plan       string `json:"plan"`
		Trial      bool   `json:"trial"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	subscription, err := h.subscriptions.Subscribe(
		r.Context(), input.CustomerID, domain.Plan(input.Plan), input.Trial)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, h.toResponse(subscription))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	subscription, err := h.subscriptions.Get(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, h.toResponse(subscription))
}

func (h *Handler) changePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var input struct {
		Plan string `json:"plan"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	subscription, err := h.subscriptions.ChangePlan(r.Context(), id, domain.Plan(input.Plan))
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, h.toResponse(subscription))
}

func (h *Handler) activate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	subscription, err := h.subscriptions.Activate(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, h.toResponse(subscription))
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	subscription, err := h.subscriptions.Cancel(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, h.toResponse(subscription))
}

func (h *Handler) revenue(w http.ResponseWriter, r *http.Request) {
	total, billable, err := h.subscriptions.MonthlyRevenue(r.Context(), "EUR")
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"monthly_recurring_revenue": total.String(),
		"billable_subscriptions":    billable,
	})
}

//
// TRANSLATION
//

// respondError maps domain vocabulary to HTTP vocabulary. This function is
// the entire reason the inner layers never import net/http.
func respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")

	case errors.Is(err, domain.ErrAlreadyExists):
		writeError(w, http.StatusConflict, message(err))

	case errors.Is(err, domain.ErrNotAllowed):
		// 409: the request is well formed, the entity is in the wrong state.
		writeError(w, http.StatusConflict, message(err))

	case errors.Is(err, domain.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, message(err))

	default:
		log.Printf("internal error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

func message(err error) string {
	text := err.Error()

	for _, prefix := range []string{"invalid: ", "not allowed in this state: ", "already exists: "} {
		if _, after, found := strings.Cut(text, prefix); found {
			return after
		}
	}

	return text
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
