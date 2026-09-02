package httpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-99/internal/config"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-99/internal/httpserver"
)

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newServer(t *testing.T) *httpserver.Server {
	t.Helper()

	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"

	return httpserver.New(cfg, quiet())
}

func get(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, path, nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	return recorder
}

// Liveness must not check dependencies: a probe that fails when the database
// is down gets the container killed, which does not fix the database and does
// lose the cache that was keeping redirects working.
func TestHealthzIgnoresDependencies(t *testing.T) {
	server := newServer(t)

	server.AddChecker("database", httpserver.CheckerFunc(func(context.Context) error {
		return errors.New("database is down")
	}))

	recorder := get(t, server.Handler(), "/healthz")

	if recorder.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 even with a failing dependency", recorder.Code)
	}
}

// Readiness starts false: a load balancer must not route to a process that is
// still migrating.
func TestReadyzIsFalseUntilMarkedReady(t *testing.T) {
	server := newServer(t)

	if recorder := get(t, server.Handler(), "/readyz"); recorder.Code != http.StatusServiceUnavailable {
		t.Errorf("status before MarkReady = %d, want 503", recorder.Code)
	}

	server.MarkReady()

	if recorder := get(t, server.Handler(), "/readyz"); recorder.Code != http.StatusOK {
		t.Errorf("status after MarkReady = %d, want 200", recorder.Code)
	}
}

func TestReadyzReportsFailingDependencies(t *testing.T) {
	server := newServer(t)
	server.MarkReady()

	server.AddChecker("database", httpserver.CheckerFunc(func(context.Context) error {
		return errors.New("connection refused")
	}))

	server.AddChecker("cache", httpserver.CheckerFunc(func(context.Context) error { return nil }))

	recorder := get(t, server.Handler(), "/readyz")

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}

	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}

	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.Checks["database"] != "connection refused" {
		t.Errorf("checks = %v, want the database's error", body.Checks)
	}

	if body.Checks["cache"] != "ok" {
		t.Errorf("a healthy dependency was not reported as ok: %v", body.Checks)
	}
}

// A readiness check that hangs hangs the probe, and a probe timeout looks the
// same as a crash.
func TestReadyzBoundsASlowChecker(t *testing.T) {
	server := newServer(t)
	server.MarkReady()

	server.AddChecker("slow", httpserver.CheckerFunc(func(ctx context.Context) error {
		select {
		case <-time.After(30 * time.Second):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}))

	start := time.Now()

	recorder := get(t, server.Handler(), "/readyz")

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("readyz took %s - the checker was not bounded", elapsed)
	}

	if recorder.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when a checker times out", recorder.Code)
	}
}

func TestRequestIDIsAssignedAndEchoed(t *testing.T) {
	handler := httpserver.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := httpserver.RequestIDFrom(r.Context()); id == "" {
			t.Error("no request id in the context")
		}

		w.WriteHeader(http.StatusOK)
	}))

	recorder := get(t, handler, "/")

	if recorder.Header().Get(httpserver.RequestIDHeader) == "" {
		t.Error("no request id in the response headers")
	}
}

// An id supplied upstream is kept, so a trace survives across services.
func TestRequestIDFromUpstreamIsKept(t *testing.T) {
	var seen string

	handler := httpserver.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = httpserver.RequestIDFrom(r.Context())
	}))

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(httpserver.RequestIDHeader, "upstream-123")

	handler.ServeHTTP(httptest.NewRecorder(), request)

	if seen != "upstream-123" {
		t.Errorf("request id = %q, want the upstream one", seen)
	}
}

// An unbounded client-supplied id ends up in every log line, and a log line is
// somewhere an attacker would like to write.
func TestOversizedRequestIDIsReplaced(t *testing.T) {
	var seen string

	handler := httpserver.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = httpserver.RequestIDFrom(r.Context())
	}))

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(httpserver.RequestIDHeader, strings.Repeat("x", 500))

	handler.ServeHTTP(httptest.NewRecorder(), request)

	if len(seen) > 64 {
		t.Errorf("request id is %d characters; the oversized header was trusted", len(seen))
	}
}

// A panic must become a 500 rather than a request that never answers.
func TestRecoverTurnsAPanicIntoAResponse(t *testing.T) {
	var recorded strings.Builder

	logger := slog.New(slog.NewTextHandler(&recorded, &slog.HandlerOptions{Level: slog.LevelError}))

	handler := httpserver.Recover(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("something went wrong")
	}))

	recorder := get(t, handler, "/")

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", recorder.Code)
	}

	// The stack goes to the log and never to the response: it names internal
	// paths, package versions, and sometimes the data being processed.
	if strings.Contains(recorder.Body.String(), "goroutine") {
		t.Errorf("the stack leaked into the response: %s", recorder.Body.String())
	}

	if !strings.Contains(recorded.String(), "panic recovered") {
		t.Error("the panic was not logged")
	}

	if !strings.Contains(recorded.String(), "goroutine") {
		t.Error("the stack was not logged")
	}
}

// http.ErrAbortHandler is the documented way a handler says "stop, silently".
func TestRecoverPassesThroughErrAbortHandler(t *testing.T) {
	handler := httpserver.Recover(quiet())(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		if recovered := recover(); recovered == nil {
			t.Error("ErrAbortHandler was swallowed; the http package's contract expects it to propagate")
		}
	}()

	get(t, handler, "/")
}

func TestLogRequestsRecordsStatusAndDuration(t *testing.T) {
	var recorded strings.Builder

	logger := slog.New(slog.NewTextHandler(&recorded, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := httpserver.LogRequests(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusTeapot)
	}))

	get(t, handler, "/some/path")

	line := recorded.String()

	for _, want := range []string{"status=418", "path=/some/path", "duration="} {
		if !strings.Contains(line, want) {
			t.Errorf("log line %q is missing %q", line, want)
		}
	}
}

// A readiness check every second is 86,400 lines a day saying "ok".
func TestSuccessfulProbesLogAtDebug(t *testing.T) {
	var recorded strings.Builder

	logger := slog.New(slog.NewTextHandler(&recorded, &slog.HandlerOptions{Level: slog.LevelInfo}))

	handler := httpserver.LogRequests(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	get(t, handler, "/healthz")

	if recorded.Len() != 0 {
		t.Errorf("a successful probe was logged at info: %s", recorded.String())
	}
}

// A FAILING probe is exactly what you want to see, so it is not suppressed.
func TestFailingProbeIsLogged(t *testing.T) {
	var recorded strings.Builder

	logger := slog.New(slog.NewTextHandler(&recorded, &slog.HandlerOptions{Level: slog.LevelInfo}))

	handler := httpserver.LogRequests(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))

	get(t, handler, "/readyz")

	if !strings.Contains(recorded.String(), "status=503") {
		t.Errorf("a failing probe was suppressed: %q", recorded.String())
	}
}

// The whole point of the walking skeleton: it starts, serves, and stops when
// its context is cancelled.
func TestStartAndGracefulShutdown(t *testing.T) {
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	cfg.ShutdownTimeout = 2 * time.Second

	server := httpserver.New(cfg, quiet())
	server.MarkReady()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)

	go func() {
		done <- server.Start(ctx)
	}()

	// Wait for the bound address rather than polling: with port 0 there is no
	// address to dial until the listener exists.
	select {
	case <-server.Started():
	case <-time.After(3 * time.Second):
		t.Fatal("the listener never opened")
	}

	response, err := http.Get("http://" + server.Addr() + "/healthz") //nolint:noctx // short-lived test request
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if response.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", response.StatusCode)
	}

	if err := response.Body.Close(); err != nil {
		t.Errorf("close: %v", err)
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start returned %v, want nil after a clean shutdown", err)
		}

	case <-time.After(10 * time.Second):
		t.Fatal("the server did not stop within 10s of its context being cancelled")
	}
}

// Readiness must fail BEFORE the listener closes, so traffic drains away while
// the server can still serve it.
func TestReadinessFailsDuringShutdown(t *testing.T) {
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	cfg.ShutdownTimeout = 2 * time.Second

	server := httpserver.New(cfg, quiet())
	server.MarkReady()

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		_ = server.Start(ctx)
	}()

	select {
	case <-server.Started():
	case <-time.After(3 * time.Second):
		t.Fatal("the listener never opened")
	}

	addr := server.Addr()

	statuses := make(chan int, 20)

	stop := make(chan struct{})

	go func() {
		for {
			select {
			case <-stop:
				close(statuses)

				return
			default:
			}

			response, err := http.Get("http://" + addr + "/readyz") //nolint:noctx // short-lived test request
			if err != nil {
				continue
			}

			select {
			case statuses <- response.StatusCode:
			default:
			}

			if err := response.Body.Close(); err != nil {
				return
			}

			time.Sleep(20 * time.Millisecond)
		}
	}()

	cancel()

	time.Sleep(200 * time.Millisecond)

	close(stop)

	sawUnavailable := false

	for status := range statuses {
		if status == http.StatusServiceUnavailable {
			sawUnavailable = true
		}
	}

	if !sawUnavailable {
		t.Error("readyz never reported 503 during shutdown; traffic would be cut off rather than drained")
	}
}
