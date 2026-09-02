package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-97/internal/auth"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-97/internal/domain"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-97/internal/service"
)

// API is the HTTP surface over the service.
type API struct {
	service *service.Service
	logger  *slog.Logger
	baseURL string
}

// NewAPI builds the handlers.
func NewAPI(svc *service.Service, baseURL string, logger *slog.Logger) *API {
	if logger == nil {
		logger = slog.Default()
	}

	return &API{service: svc, baseURL: baseURL, logger: logger}
}

// linkResponse is the wire shape of a link.
//
// A separate type from domain.Link on purpose: the domain type is free to
// change without breaking clients, and the wire type is free to include things
// the domain does not have - like the full short URL, which depends on the
// deployment rather than the data.
type linkResponse struct {
	Code      string `json:"code"`
	ShortURL  string `json:"short_url"`
	Target    string `json:"target"`
	Active    bool   `json:"active"`
	Clicks    int64  `json:"clicks"`
	ExpiresAt string `json:"expires_at,omitempty"`
	CreatedAt string `json:"created_at"`
}

func (a *API) toResponse(link domain.Link) linkResponse {
	response := linkResponse{
		Code:      link.Code.String(),
		ShortURL:  a.baseURL + "/" + link.Code.String(),
		Target:    link.Target,
		Active:    link.Active,
		Clicks:    link.Clicks,
		CreatedAt: link.CreatedAt.UTC().Format(time.RFC3339),
	}

	if !link.ExpiresAt.IsZero() {
		response.ExpiresAt = link.ExpiresAt.UTC().Format(time.RFC3339)
	}

	return response
}

// createLink handles POST /api/links.
func (a *API) createLink(w http.ResponseWriter, r *http.Request) {
	owner, ok := auth.OwnerFrom(r.Context())
	if !ok {
		a.writeError(w, r, domain.ErrUnauthorized)

		return
	}

	var body struct {
		Target    string `json:"target"`
		Code      string `json:"code,omitempty"`
		ExpiresAt string `json:"expires_at,omitempty"`
	}

	if err := decodeJSON(r, &body); err != nil {
		a.writeProblem(w, r, http.StatusBadRequest, "invalid_request", err.Error())

		return
	}

	request := service.CreateRequest{Owner: owner, Target: body.Target, Code: body.Code}

	if body.ExpiresAt != "" {
		expiresAt, err := time.Parse(time.RFC3339, body.ExpiresAt)
		if err != nil {
			a.writeProblem(w, r, http.StatusBadRequest, "invalid_request",
				"expires_at must be RFC 3339, e.g. 2026-12-01T00:00:00Z")

			return
		}

		request.ExpiresAt = expiresAt
	}

	link, err := a.service.CreateLink(r.Context(), request)
	if err != nil {
		a.writeError(w, r, err)

		return
	}

	w.Header().Set("Location", a.baseURL+"/"+link.Code.String())

	writeJSON(w, http.StatusCreated, a.toResponse(link))
}

// listLinks handles GET /api/links.
func (a *API) listLinks(w http.ResponseWriter, r *http.Request) {
	owner, ok := auth.OwnerFrom(r.Context())
	if !ok {
		a.writeError(w, r, domain.ErrUnauthorized)

		return
	}

	limit := 50

	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 200 {
			a.writeProblem(w, r, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 200")

			return
		}

		limit = parsed
	}

	links, err := a.service.ListLinks(r.Context(), owner, limit)
	if err != nil {
		a.writeError(w, r, err)

		return
	}

	// A non-nil empty slice, so the JSON is [] rather than null. A client
	// ranging over null in JavaScript throws; over [] it does nothing.
	responses := make([]linkResponse, 0, len(links))

	for _, link := range links {
		responses = append(responses, a.toResponse(link))
	}

	writeJSON(w, http.StatusOK, responses)
}

// getLink handles GET /api/links/{code}.
func (a *API) getLink(w http.ResponseWriter, r *http.Request) {
	owner, ok := auth.OwnerFrom(r.Context())
	if !ok {
		a.writeError(w, r, domain.ErrUnauthorized)

		return
	}

	link, err := a.service.GetLink(r.Context(), owner, r.PathValue("code"))
	if err != nil {
		a.writeError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, a.toResponse(link))
}

// deleteLink handles DELETE /api/links/{code}.
func (a *API) deleteLink(w http.ResponseWriter, r *http.Request) {
	owner, ok := auth.OwnerFrom(r.Context())
	if !ok {
		a.writeError(w, r, domain.ErrUnauthorized)

		return
	}

	if err := a.service.DeactivateLink(r.Context(), owner, r.PathValue("code")); err != nil {
		a.writeError(w, r, err)

		return
	}

	// 204: the link is deactivated and there is nothing to say about it.
	w.WriteHeader(http.StatusNoContent)
}

// redirect handles GET /{code} - the hot path.
func (a *API) redirect(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	link, err := a.service.Follow(r.Context(), code)
	if err != nil {
		a.writeError(w, r, err)

		return
	}

	// 302, not 301: a permanent redirect is cached by the browser forever,
	// which means deactivating the link stops working for anyone who has
	// visited it, and the click is never counted again.
	http.Redirect(w, r, link.Target, http.StatusFound)

	// After the response. The click is recorded on a context detached from
	// the request, because the request's context is cancelled the moment the
	// client disconnects - and a user who closes the tab still clicked.
	go a.recordClick(link, r)
}

func (a *API) recordClick(link domain.Link, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()

	click := domain.Click{
		Code:       link.Code,
		OccurredAt: time.Now().UTC(),
		Referrer:   r.Referer(),
		UserAgent:  r.UserAgent(),
	}

	if err := a.service.RecordClick(ctx, click); err != nil {
		// Logged, never returned: the redirect already happened, and failing
		// the request now would be reporting a metrics problem as a broken
		// link.
		a.logger.Error("record click",
			slog.String("code", link.Code.String()),
			slog.String("error", err.Error()))
	}
}

//
// ERRORS
//

// errorResponse is the one shape every failure uses.
type errorResponse struct {
	Error     string `json:"error"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// writeError maps a domain error onto a status code, in one place.
//
// Scattered across handlers, the same failure becomes a 404 on one endpoint
// and a 500 on another, and a client cannot tell which errors are worth
// retrying.
func (a *API) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		a.writeProblem(w, r, http.StatusNotFound, "not_found", "no such link")

	case errors.Is(err, domain.ErrGone):
		// 410, not 404: Gone tells a crawler to forget the URL, Not Found
		// invites it back tomorrow.
		a.writeProblem(w, r, http.StatusGone, "gone", err.Error())

	case errors.Is(err, domain.ErrCodeTaken):
		a.writeProblem(w, r, http.StatusConflict, "code_taken", "that code is already in use")

	case errors.Is(err, domain.ErrInvalidCode), errors.Is(err, domain.ErrInvalidTarget):
		a.writeProblem(w, r, http.StatusBadRequest, "invalid_request", err.Error())

	case errors.Is(err, domain.ErrUnauthorized):
		// The WWW-Authenticate header is what makes a 401 a 401 rather than a
		// 403 with the wrong number.
		w.Header().Set("WWW-Authenticate", `Bearer realm="linkr"`)
		a.writeProblem(w, r, http.StatusUnauthorized, "unauthorized", "a valid API key is required")

	default:
		// The internal error goes to the log with its request id; the client
		// gets the id and nothing else. An error string can carry a table
		// name, a file path or a row's contents.
		a.logger.Error("unhandled error",
			slog.String("request_id", RequestIDFrom(r.Context())),
			slog.String("path", r.URL.Path),
			slog.String("error", err.Error()))

		a.writeProblem(w, r, http.StatusInternalServerError, "internal", "internal error")
	}
}

func (a *API) writeProblem(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, errorResponse{
		Error:     code,
		Message:   message,
		RequestID: RequestIDFrom(r.Context()),
	})
}

// maxBodyBytes bounds a request body. Without it, a client can stream
// gigabytes into a handler that expected a hundred bytes of JSON.
const maxBodyBytes = 64 << 10

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBodyBytes))

	// A misspelled field is a client bug that would otherwise be silently
	// ignored - and "expires_at was not applied" is a very expensive silence.
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}

	return nil
}
