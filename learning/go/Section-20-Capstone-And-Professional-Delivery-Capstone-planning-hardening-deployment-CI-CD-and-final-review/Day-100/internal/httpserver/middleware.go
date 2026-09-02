package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"
)

// contextKey is unexported so no other package can collide with these keys.
// A string key in a context is a global namespace shared with every dependency.
type contextKey string

const requestIDKey contextKey = "request_id"

// RequestIDHeader is both read and written, so a request id assigned by a load
// balancer or an upstream service survives into this service's logs.
const RequestIDHeader = "X-Request-Id"

// RequestID attaches an id to every request.
//
// It is the first thing a support ticket carries: "it failed at 14:32" is
// unsearchable, and "request 8f3a2b1c failed" is one grep.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)

		if id == "" || len(id) > 64 {
			// Regenerate rather than trust: an unbounded client-supplied id
			// ends up in every log line, and a log line is a place an
			// attacker would like to write.
			id = newRequestID()
		}

		w.Header().Set(RequestIDHeader, id)

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

// RequestIDFrom returns the id attached to a request's context.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)

	return id
}

func newRequestID() string {
	buffer := make([]byte, 8)

	if _, err := rand.Read(buffer); err != nil {
		// An id is a correlation aid, not a security boundary; a timestamp is
		// a worse id but better than failing the request over it.
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))[:16]
	}

	return hex.EncodeToString(buffer)
}

// statusRecorder captures the status code, which http.ResponseWriter does not
// expose after the fact.
type statusRecorder struct {
	http.ResponseWriter

	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status

	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(payload []byte) (int, error) {
	if r.status == 0 {
		// A handler that writes without calling WriteHeader has implicitly
		// sent a 200.
		r.status = http.StatusOK
	}

	written, err := r.ResponseWriter.Write(payload)

	r.bytes += written

	return written, err
}

// LogRequests logs one line per request, after it completes.
//
// One line, not two: a "started" line doubles the volume and says nothing a
// completed line does not, except during a hang - and a hang is what the
// timeout metrics are for.
//
// What is deliberately NOT logged: the query string (it carries API keys when
// someone passes one wrongly), the Authorization header, and the request body.
func LogRequests(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			recorder := &statusRecorder{ResponseWriter: w}

			next.ServeHTTP(recorder, r)

			if recorder.status == 0 {
				recorder.status = http.StatusOK
			}

			level := slog.LevelInfo

			switch {
			case recorder.status >= 500:
				level = slog.LevelError
			case recorder.status >= 400:
				level = slog.LevelWarn
			}

			// The probes would otherwise be most of the log volume: a
			// readiness check every second is 86,400 lines a day saying "ok".
			if isProbe(r.URL.Path) && recorder.status < 400 {
				level = slog.LevelDebug
			}

			logger.LogAttrs(r.Context(), level, "request",
				slog.String("request_id", RequestIDFrom(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", recorder.status),
				slog.Int("bytes", recorder.bytes),
				slog.Duration("duration", time.Since(start).Round(time.Microsecond)),
			)
		})
	}
}

func isProbe(path string) bool {
	return path == "/healthz" || path == "/readyz"
}

// Recover turns a panic into a 500 instead of a dead process.
//
// It is outermost so it also covers the logging middleware. A panic in a
// handler kills the whole server otherwise: net/http recovers per connection,
// but the request that panicked gets no response at all, and the client waits
// for a timeout rather than seeing an error.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}

				// http.ErrAbortHandler is the documented way a handler says
				// "stop, silently" - re-panicking preserves that contract.
				if err, ok := recovered.(error); ok && err == http.ErrAbortHandler {
					panic(recovered)
				}

				logger.LogAttrs(r.Context(), slog.LevelError, "panic recovered",
					slog.String("request_id", RequestIDFrom(r.Context())),
					slog.String("path", r.URL.Path),
					slog.Any("panic", recovered),
					// The stack goes to the log and NEVER to the response: it
					// names internal paths, package versions and sometimes the
					// data being processed.
					slog.String("stack", stack()),
				)

				writeJSON(w, http.StatusInternalServerError, map[string]any{
					"error":      "internal",
					"request_id": RequestIDFrom(r.Context()),
				})
			}()

			next.ServeHTTP(w, r)
		})
	}
}
