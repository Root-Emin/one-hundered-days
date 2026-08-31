package main

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

// ============================================================
// MIDDLEWARE TYPE
// ============================================================

// Middleware is a function that receives a handler
// and returns a new handler.
//
// Example:
//
//	loggingMiddleware(handler)
//
// becomes:
//
//	loggingMiddleware -> handler
//
// This is the fundamental middleware pattern in Go.
type Middleware func(http.Handler) http.Handler

// ============================================================
// LOGGING MIDDLEWARE
// ============================================================

// loggingMiddleware records:
//
// - HTTP method
// - request path
// - response status
// - request duration
//
// It wraps the next handler and performs work both
// before and after the handler executes.
func loggingMiddleware(next http.Handler) http.Handler {

	return http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {

		start := time.Now()

		// ----------------------------------------------------
		// ResponseWriter wrapper
		//
		// We need to capture the HTTP status code written
		// by the actual handler.
		// ----------------------------------------------------

		recorder := &statusRecorder{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		// ----------------------------------------------------
		// Call the next handler
		// ----------------------------------------------------

		next.ServeHTTP(recorder, r)

		// ----------------------------------------------------
		// Calculate duration
		// ----------------------------------------------------

		duration := time.Since(start)

		// ----------------------------------------------------
		// Log request information
		// ----------------------------------------------------

		log.Printf(
			"%s %s -> %d (%s)",
			r.Method,
			r.URL.Path,
			recorder.statusCode,
			duration,
		)
	})
}

// ============================================================
// STATUS RECORDER
// ============================================================

// statusRecorder allows middleware to know which
// HTTP status code was written by the handler.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code before forwarding
// the call to the real ResponseWriter.
func (r *statusRecorder) WriteHeader(statusCode int) {

	r.statusCode = statusCode

	r.ResponseWriter.WriteHeader(statusCode)
}

// Write ensures that if the handler writes a body
// without explicitly calling WriteHeader,
// the status is still recorded as 200.
func (r *statusRecorder) Write(body []byte) (int, error) {

	if r.statusCode == 0 {
		r.statusCode = http.StatusOK
	}

	return r.ResponseWriter.Write(body)
}

// ============================================================
// RECOVERY MIDDLEWARE
// ============================================================

// recoveryMiddleware catches panics that happen inside
// downstream handlers.
//
// Instead of allowing the panic to crash the request,
// we convert it into HTTP 500 Internal Server Error.
func recoveryMiddleware(next http.Handler) http.Handler {

	return http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {

		defer func() {

			if recovered := recover(); recovered != nil {

				log.Printf(
					"PANIC RECOVERED: %v",
					recovered,
				)

				http.Error(
					w,
					"internal server error",
					http.StatusInternalServerError,
				)
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// ============================================================
// AUTH PLACEHOLDER MIDDLEWARE
// ============================================================

// authMiddleware is intentionally simple.
//
// In a real application this would usually:
//
// - validate a JWT
// - check a session
// - inspect an API key
// - attach user information to context
//
// For Day 29 we only demonstrate middleware composition.
func authMiddleware(next http.Handler) http.Handler {

	return http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {

		token := r.Header.Get("Authorization")

		// ----------------------------------------------------
		// Placeholder authentication
		// ----------------------------------------------------

		if token == "" {

			http.Error(
				w,
				"authorization required",
				http.StatusUnauthorized,
			)

			return
		}

		// ----------------------------------------------------
		// Authentication succeeded.
		// Continue to the next middleware/handler.
		// ----------------------------------------------------

		next.ServeHTTP(w, r)
	})
}

// ============================================================
// MIDDLEWARE CHAIN
// ============================================================

// chain applies middleware in the order provided.
//
// If we call:
//
//	chain(
//	    loggingMiddleware,
//	    recoveryMiddleware,
//	    authMiddleware,
//	)
//
// the resulting execution order is:
//
//	logging
//	    ↓
//	recovery
//	    ↓
//	auth
//	    ↓
//	handler
//
// The reverse wrapping loop is important because
// middleware are nested around the handler.
func chain(
	handler http.Handler,
	middlewares ...Middleware,
) http.Handler {

	for i := len(middlewares) - 1; i >= 0; i-- {

		handler = middlewares[i](handler)
	}

	return handler
}

// ============================================================
// APPLICATION HANDLERS
// ============================================================

// healthHandler is a normal endpoint.
func healthHandler(
	w http.ResponseWriter,
	r *http.Request,
) {

	w.Header().Set(
		"Content-Type",
		"application/json",
	)

	w.WriteHeader(http.StatusOK)

	fmt.Fprintln(
		w,
		`{"status":"ok"}`,
	)
}

// taskHandler simulates a successful API endpoint.
func taskHandler(
	w http.ResponseWriter,
	r *http.Request,
) {

	w.Header().Set(
		"Content-Type",
		"application/json",
	)

	w.WriteHeader(http.StatusOK)

	fmt.Fprintln(
		w,
		`{"id":1,"title":"Learn Go Middleware"}`,
	)
}

// panicHandler deliberately panics.
//
// This allows us to prove that recoveryMiddleware
// actually works.
func panicHandler(
	w http.ResponseWriter,
	r *http.Request,
) {

	panic("something went seriously wrong")
}

// ============================================================
// MAIN
// ============================================================

func main() {

	// --------------------------------------------------------
	// Create handlers
	// --------------------------------------------------------

	healthHandler := http.HandlerFunc(
		healthHandler,
	)

	taskHandler := http.HandlerFunc(
		taskHandler,
	)

	panicHandler := http.HandlerFunc(
		panicHandler,
	)

	// --------------------------------------------------------
	// Build middleware chain
	// --------------------------------------------------------

	//
	// Execution order:
	//
	// Request
	//    ↓
	// Logging
	//    ↓
	// Recovery
	//    ↓
	// Auth
	//    ↓
	// Handler
	//    ↓
	// Response
	//

	protectedTaskHandler := chain(
		taskHandler,
		loggingMiddleware,
		recoveryMiddleware,
		authMiddleware,
	)

	protectedPanicHandler := chain(
		panicHandler,
		loggingMiddleware,
		recoveryMiddleware,
		authMiddleware,
	)

	// --------------------------------------------------------
	// Router
	// --------------------------------------------------------

	mux := http.NewServeMux()

	mux.Handle(
		"/health",
		healthHandler,
	)

	mux.Handle(
		"/tasks",
		protectedTaskHandler,
	)

	mux.Handle(
		"/panic",
		protectedPanicHandler,
	)

	// --------------------------------------------------------
	// Start server
	// --------------------------------------------------------

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	log.Println(
		"HTTP server listening on :8080",
	)

	err := server.ListenAndServe()

	if err != nil && err != http.ErrServerClosed {

		log.Fatalf(
			"server failed: %v",
			err,
		)
	}
}
