// Package httpserver is the walking skeleton: routing, middleware and a
// lifecycle that starts and stops cleanly.
//
// It is built first, before any feature, because the wiring is where the
// surprises live - signals, timeouts, shutdown ordering, a probe that lies.
// Finding those on day one costs an hour; finding them on day five costs the
// day.
//
// Today it serves only the probes. Day 97 hangs the real handlers on the same
// router, and nothing here changes.
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-100/internal/auth"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-100/internal/config"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-100/internal/metrics"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-100/internal/ratelimit"
)

// Checker reports whether a dependency is usable. The store implements it on
// Day 97; today there is nothing to check, which is why Ready starts false.
type Checker interface {
	// Check returns nil when the dependency is usable.
	Check(ctx context.Context) error
}

// CheckerFunc adapts a function to Checker.
type CheckerFunc func(ctx context.Context) error

// Check calls f.
func (f CheckerFunc) Check(ctx context.Context) error {
	return f(ctx)
}

// Server owns the HTTP lifecycle.
type Server struct {
	config config.Config
	logger *slog.Logger

	// ready separates liveness from readiness. It flips true only once
	// startup has finished, so a load balancer does not route to a process
	// that is still migrating.
	ready atomic.Bool
	// shuttingDown makes /readyz fail BEFORE the listener closes, so traffic
	// drains away while the server is still able to serve it.
	shuttingDown atomic.Bool

	checkers map[string]Checker

	// api and resolver are nil in the walking skeleton; when they are set,
	// Handler mounts the real routes. Keeping the server usable without them
	// is what let Day 96 exist at all.
	api      *API
	resolver auth.Resolver

	// limiter and metrics are optional: the server runs without them, which
	// is what let Days 96 and 97 exist.
	limiter *ratelimit.Limiter
	metrics *metrics.Metrics

	server *http.Server

	// mu guards listener, which Start writes and Addr reads from another
	// goroutine - a test waiting for the bound port is doing exactly that.
	mu       sync.RWMutex
	listener net.Listener

	// started is closed once the listener is bound, so a caller can wait for
	// the real address instead of polling. With Addr ":0" - which every test
	// uses, to avoid fighting over ports - there is no other way to know.
	started chan struct{}

	startedAt time.Time
}

// New builds a server. The listener is not opened until Start.
func New(cfg config.Config, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	return &Server{
		config:    cfg,
		logger:    logger,
		checkers:  make(map[string]Checker),
		started:   make(chan struct{}),
		startedAt: time.Now(),
	}
}

// Mount attaches the API and its authenticator.
//
// Called by main after the store is open. Before it, the server still starts
// and still answers its probes - which is what makes "the database is down" a
// degraded service rather than a crash loop.
func (s *Server) Mount(api *API, resolver auth.Resolver) {
	s.api = api
	s.resolver = resolver
}

// SetRateLimiter attaches the per-key limiter.
func (s *Server) SetRateLimiter(limiter *ratelimit.Limiter) {
	s.limiter = limiter
}

// SetMetrics attaches the Prometheus collectors.
func (s *Server) SetMetrics(m *metrics.Metrics) {
	s.metrics = m

	if s.api != nil {
		s.api.SetObserver(m)
	}
}

// MetricsHandler returns the /metrics handler for the private listener.
//
// It is served on a SEPARATE listener bound to localhost, never on the public
// one: /metrics tells an attacker your traffic shape, your error rate and your
// deployment size for free.
func (s *Server) MetricsHandler() http.Handler {
	if s.metrics == nil {
		return http.NotFoundHandler()
	}

	mux := http.NewServeMux()

	mux.Handle("GET /metrics", s.metrics.Handler())

	return mux
}

// AddChecker registers a readiness dependency.
func (s *Server) AddChecker(name string, checker Checker) {
	s.checkers[name] = checker
}

// MarkReady declares startup finished.
func (s *Server) MarkReady() {
	s.ready.Store(true)

	s.logger.Info("ready", slog.String("addr", s.config.Addr))
}

// Handler builds the router with its middleware chain.
//
// The order is deliberate, outermost first:
//
//	recover   nothing above it can panic the process
//	requestID every log line below it can be correlated
//	logging   sees the final status, including a recovered panic
//	          (auth and rate limiting slot in here on Day 98)
//	routes
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)

	if s.api != nil && s.resolver != nil {
		// The API routes sit behind auth; the redirect deliberately does not.
		// Wrapping the whole mux in the auth middleware would put a bearer
		// token in front of every short link, which is the opposite of what a
		// short link is for.
		authenticate := auth.Middleware(s.resolver, func(w http.ResponseWriter, r *http.Request, err error) {
			s.api.writeError(w, r, err)
		})

		api := http.NewServeMux()

		api.HandleFunc("POST /api/links", s.api.createLink)
		api.HandleFunc("GET /api/links", s.api.listLinks)
		api.HandleFunc("GET /api/links/{code}", s.api.getLink)
		api.HandleFunc("DELETE /api/links/{code}", s.api.deleteLink)
		api.HandleFunc("GET /api/links/{code}/stats", s.api.stats)

		var apiHandler http.Handler = api

		if s.limiter != nil {
			// AFTER authentication, so the bucket is keyed by the
			// authenticated owner rather than by an IP address - which would
			// punish everyone behind one NAT and do nothing to a client with
			// a thousand addresses.
			apiHandler = s.limiter.Middleware(func(w http.ResponseWriter, r *http.Request) {
				s.api.writeProblem(w, r, http.StatusTooManyRequests, "rate_limited",
					"too many requests for this API key")
			})(apiHandler)
		}

		mux.Handle("/api/", authenticate(apiHandler))

		// The hot path. {code} cannot collide with /api, /healthz or /readyz:
		// ServeMux prefers the more specific pattern, and domain.ParseCode
		// rejects those words anyway - belt and braces, because the router's
		// precedence rules are not something to rely on for security.
		mux.HandleFunc("GET /{code}", s.api.redirect)
	}

	mux.HandleFunc("GET /{$}", s.handleRoot)

	var handler http.Handler = mux

	if s.metrics != nil {
		// Inside the logging middleware, so the recorded duration is the
		// handler's rather than the whole chain's - and outside the routes, so
		// a 404 is still counted.
		handler = s.metrics.Middleware(handler)
	}

	return Recover(s.logger)(RequestID(LogRequests(s.logger)(handler)))
}

// handleHealth answers liveness: is the process running?
//
// It must not check dependencies. A liveness probe that fails when the
// database is down gets the container killed and restarted, which does not fix
// the database and does lose the cache that was keeping redirects working.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"uptime": time.Since(s.startedAt).Round(time.Second).String(),
	})
}

// handleReady answers readiness: should traffic be sent here?
//
// This one DOES check dependencies, and it fails during shutdown before the
// listener closes - so the load balancer stops sending work while the process
// can still finish what it has.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if s.shuttingDown.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "shutting down",
		})

		return
	}

	if !s.ready.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "starting",
		})

		return
	}

	// A readiness check that can hang is a readiness check that hangs the
	// probe, and a probe timeout looks the same as a crash.
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	checks := make(map[string]string, len(s.checkers))
	healthy := true

	for name, checker := range s.checkers {
		if err := checker.Check(ctx); err != nil {
			checks[name] = err.Error()
			healthy = false

			continue
		}

		checks[name] = "ok"
	}

	status := http.StatusOK

	if !healthy {
		status = http.StatusServiceUnavailable
	}

	writeJSON(w, status, map[string]any{"status": statusWord(healthy), "checks": checks})
}

func statusWord(healthy bool) string {
	if healthy {
		return "ok"
	}

	return "degraded"
}

// handleRoot answers the bare "/" - which is not a code, and must not be
// treated as one.
func (s *Server) handleRoot(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "linkr",
		"docs":    "see docs/REQUIREMENTS.md",
	})
}

// Start opens the listener and serves until the context is cancelled.
//
// The listener is opened synchronously so a port already in use is an error
// from Start rather than a goroutine failing silently a moment later.
func (s *Server) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.config.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.config.Addr, err)
	}

	s.mu.Lock()
	s.listener = listener
	s.mu.Unlock()

	// Signal the bound address before serving, so a waiter never races the
	// first request.
	close(s.started)

	s.server = &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: s.config.ReadHeaderTimeout,
		WriteTimeout:      s.config.WriteTimeout,
		IdleTimeout:       s.config.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(s.logger.Handler(), slog.LevelWarn),
	}

	errCh := make(chan error, 1)

	go func() {
		s.logger.Info("listening", slog.String("addr", s.Addr()))

		errCh <- s.server.Serve(listener)
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}

		return nil

	case <-ctx.Done():
		return s.Shutdown()
	}
}

// Shutdown drains in-flight requests and closes the listener.
//
// The order matters and is the whole reason this is a method rather than a
// line in main:
//
//  1. fail /readyz, so the load balancer stops sending new work
//  2. wait a moment for it to notice - a probe interval, not zero
//  3. close the listener and let in-flight requests finish
//
// Skipping step 2 means the last few requests arrive after the listener has
// closed, and the client sees a connection reset rather than a response.
func (s *Server) Shutdown() error {
	s.shuttingDown.Store(true)

	s.logger.Info("draining", slog.Duration("grace", s.config.ShutdownTimeout))

	// In production this is the load balancer's probe interval. Kept short
	// here so tests and demos do not wait.
	drainDelay := 250 * time.Millisecond

	if s.config.Production() {
		drainDelay = 3 * time.Second
	}

	time.Sleep(drainDelay)

	ctx, cancel := context.WithTimeout(context.Background(), s.config.ShutdownTimeout)
	defer cancel()

	if err := s.server.Shutdown(ctx); err != nil {
		// Shutdown returning an error means requests were still running when
		// the grace period expired. Worth saying out loud: it is data being
		// cut off, not a formality.
		return fmt.Errorf("shutdown: %w", err)
	}

	s.logger.Info("stopped")

	return nil
}

// Started is closed once the listener is bound.
//
// Waiting on it is how a caller learns the real address when the configured
// port is 0. Polling Addr instead is a race, and a retry loop around Dial only
// hides it.
func (s *Server) Started() <-chan struct{} {
	return s.started
}

// Addr returns the address actually bound, which differs from the configured
// one when the port is 0 - the form every test uses.
//
// Before the listener is open it returns the configured address; wait on
// Started to be sure of the real one.
func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.listener == nil {
		return s.config.Addr
	}

	return s.listener.Addr().String()
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// The status line is already written; there is nothing left to say.
		_ = err
	}
}
