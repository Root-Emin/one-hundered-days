package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// ============================================================
// CONFIG
// ============================================================

type Config struct {
	HTTPAddr        string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

func loadConfig() Config {
	return Config{
		HTTPAddr:        ":8080",
		ReadTimeout:     5 * time.Second,
		WriteTimeout:    10 * time.Second,
		IdleTimeout:     60 * time.Second,
		ShutdownTimeout: 10 * time.Second,
	}
}

// ============================================================
// LOGGER
// ============================================================

type AppLogger struct {
	logger *log.Logger
}

func NewAppLogger() *AppLogger {
	return &AppLogger{
		logger: log.New(
			os.Stdout,
			"[APP] ",
			log.LstdFlags|log.Lmicroseconds,
		),
	}
}

func (l *AppLogger) Info(message string) {
	l.logger.Println(message)
}

func (l *AppLogger) Error(message string) {
	l.logger.Println("ERROR:", message)
}

func (l *AppLogger) Flush() {
	// Standard log.Logger writes directly to its output.
	// This method represents the shutdown hook that would flush
	// buffered logs in a real logging system.
	l.logger.Println("logger flushed")
}

// ============================================================
// DATABASE
// ============================================================

type Database struct {
	closed bool
}

func NewDatabase() *Database {
	return &Database{}
}

func (db *Database) Query(ctx context.Context) error {
	select {
	case <-time.After(100 * time.Millisecond):
		return nil

	case <-ctx.Done():
		return ctx.Err()
	}
}

func (db *Database) Close() error {
	if db.closed {
		return nil
	}

	db.closed = true

	log.Println("[DB] database connection closed")

	return nil
}

// ============================================================
// APPLICATION
// ============================================================

type Application struct {
	config Config
	logger *AppLogger
	db     *Database
}

func NewApplication(
	config Config,
	logger *AppLogger,
	db *Database,
) *Application {
	return &Application{
		config: config,
		logger: logger,
		db:     db,
	}
}

// ============================================================
// MIDDLEWARE
// ============================================================

func loggingMiddleware(
	logger *AppLogger,
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		start := time.Now()

		logger.Info(
			fmt.Sprintf(
				"request started method=%s path=%s",
				r.Method,
				r.URL.Path,
			),
		)

		next.ServeHTTP(w, r)

		logger.Info(
			fmt.Sprintf(
				"request completed method=%s path=%s duration=%s",
				r.Method,
				r.URL.Path,
				time.Since(start),
			),
		)
	})
}

// ============================================================
// HANDLERS
// ============================================================

func (app *Application) healthHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	w.Header().Set(
		"Content-Type",
		"application/json",
	)

	w.WriteHeader(http.StatusOK)

	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (app *Application) slowHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	app.logger.Info(
		"slow request started",
	)

	// Simulate long-running work.
	//
	// The important part here is that the handler listens
	// to r.Context(). If the request is cancelled, the work
	// can stop instead of continuing forever.

	select {
	case <-time.After(5 * time.Second):

		app.logger.Info(
			"slow request finished normally",
		)

		w.Header().Set(
			"Content-Type",
			"application/json",
		)

		w.WriteHeader(http.StatusOK)

		_, _ = w.Write(
			[]byte(`{"status":"completed"}`),
		)

	case <-r.Context().Done():

		app.logger.Info(
			"slow request cancelled",
		)

		// The client/request context was cancelled.
		// Do not continue doing unnecessary work.
		return
	}
}

func (app *Application) databaseHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	err := app.db.Query(r.Context())

	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}

		http.Error(
			w,
			"database error",
			http.StatusInternalServerError,
		)

		return
	}

	w.Header().Set(
		"Content-Type",
		"application/json",
	)

	w.WriteHeader(http.StatusOK)

	_, _ = w.Write(
		[]byte(`{"status":"database query successful"}`),
	)
}

// ============================================================
// ROUTER
// ============================================================

func (app *Application) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc(
		"/health",
		app.healthHandler,
	)

	mux.HandleFunc(
		"/slow",
		app.slowHandler,
	)

	mux.HandleFunc(
		"/database",
		app.databaseHandler,
	)

	// Middleware chain.
	//
	// Request
	//    ↓
	// logging middleware
	//    ↓
	// mux
	//    ↓
	// handler
	//
	return loggingMiddleware(
		app.logger,
		mux,
	)
}

// ============================================================
// HTTP SERVER
// ============================================================

func newHTTPServer(
	config Config,
	handler http.Handler,
) *http.Server {
	return &http.Server{
		Addr:    config.HTTPAddr,
		Handler: handler,

		// ----------------------------------------------------
		// ReadTimeout
		//
		// Maximum amount of time allowed to read the request.
		// Helps protect against slow clients.
		// ----------------------------------------------------
		ReadTimeout: config.ReadTimeout,

		// ----------------------------------------------------
		// WriteTimeout
		//
		// Maximum amount of time allowed to write a response.
		// ----------------------------------------------------
		WriteTimeout: config.WriteTimeout,

		// ----------------------------------------------------
		// IdleTimeout
		//
		// Maximum amount of time to wait for the next request
		// when keep-alive connections are used.
		// ----------------------------------------------------
		IdleTimeout: config.IdleTimeout,
	}
}

// ============================================================
// GRACEFUL SHUTDOWN
// ============================================================

func gracefulShutdown(
	server *http.Server,
	app *Application,
) {
	app.logger.Info(
		"starting graceful shutdown",
	)

	// --------------------------------------------------------
	// Create a shutdown context.
	//
	// The server gets a limited amount of time to:
	//
	// 1. Stop accepting new connections.
	// 2. Wait for active requests to finish.
	// 3. Close idle connections.
	//
	// If the timeout expires, Shutdown returns an error.
	// --------------------------------------------------------

	shutdownCtx, cancel := context.WithTimeout(
		context.Background(),
		app.config.ShutdownTimeout,
	)

	defer cancel()

	err := server.Shutdown(shutdownCtx)

	if err != nil {
		app.logger.Error(
			fmt.Sprintf(
				"HTTP server shutdown failed: %v",
				err,
			),
		)
	} else {
		app.logger.Info(
			"HTTP server shutdown completed",
		)
	}

	// --------------------------------------------------------
	// Close application resources.
	//
	// In a real project this could include:
	//
	// - database connections
	// - Redis
	// - message brokers
	// - files
	// - telemetry exporters
	// - buffered loggers
	// --------------------------------------------------------

	if err := app.db.Close(); err != nil {
		app.logger.Error(
			fmt.Sprintf(
				"database close failed: %v",
				err,
			),
		)
	}

	app.logger.Flush()

	app.logger.Info(
		"application shutdown completed",
	)
}

// ============================================================
// MAIN
// ============================================================

func main() {
	// --------------------------------------------------------
	// CONFIG
	// --------------------------------------------------------

	config := loadConfig()

	// --------------------------------------------------------
	// DEPENDENCIES
	// --------------------------------------------------------

	logger := NewAppLogger()

	db := NewDatabase()

	app := NewApplication(
		config,
		logger,
		db,
	)

	// --------------------------------------------------------
	// ROUTES + MIDDLEWARE
	// --------------------------------------------------------

	handler := app.routes()

	// --------------------------------------------------------
	// HTTP SERVER
	// --------------------------------------------------------

	server := newHTTPServer(
		config,
		handler,
	)

	// --------------------------------------------------------
	// SIGNAL CONTEXT
	//
	// Trap:
	//
	// SIGINT  -> Ctrl+C
	// SIGTERM -> Kubernetes/Docker/systemd/etc.
	//
	// signal.NotifyContext automatically cancels the
	// context when one of these signals arrives.
	// --------------------------------------------------------

	signalCtx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)

	defer stop()

	// --------------------------------------------------------
	// START SERVER
	// --------------------------------------------------------

	serverErr := make(chan error, 1)

	go func() {
		logger.Info(
			fmt.Sprintf(
				"HTTP server listening on %s",
				config.HTTPAddr,
			),
		)

		err := server.ListenAndServe()

		if err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}

		serverErr <- nil
	}()

	// --------------------------------------------------------
	// WAIT
	//
	// The application waits for either:
	//
	// 1. OS shutdown signal
	// 2. Unexpected HTTP server error
	// --------------------------------------------------------

	select {
	case <-signalCtx.Done():

		logger.Info(
			fmt.Sprintf(
				"shutdown signal received: %v",
				signalCtx.Err(),
			),
		)

	case err := <-serverErr:

		if err != nil {
			logger.Error(
				fmt.Sprintf(
					"HTTP server failed: %v",
					err,
				),
			)

			// Even though the server failed unexpectedly,
			// application resources still need to be closed.
		}
	}

	// --------------------------------------------------------
	// GRACEFUL SHUTDOWN
	// --------------------------------------------------------

	gracefulShutdown(
		server,
		app,
	)
}
