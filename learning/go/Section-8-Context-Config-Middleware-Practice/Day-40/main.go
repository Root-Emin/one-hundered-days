package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

/*
Day 40 - Context, Config & Middleware

Environment variables:

	PORT            HTTP server port. Default: 8080
	READ_TIMEOUT    Maximum time for reading a request. Default: 5s
	WRITE_TIMEOUT   Maximum time for writing a response. Default: 10s
	IDLE_TIMEOUT    Maximum idle connection time. Default: 60s
	SHUTDOWN_TIMEOUT Graceful shutdown timeout. Default: 10s

Example:

	PORT=9090 READ_TIMEOUT=3s go run main.go

Run:

	go run main.go

Test:

	curl http://localhost:8080/health
	curl http://localhost:8080/notes

Create:

	curl -X POST http://localhost:8080/notes \
	  -H "Content-Type: application/json" \
	  -d '{"title":"Learn Go","content":"Study context and middleware"}'

Delete:

	curl -X DELETE http://localhost:8080/notes/1

Shutdown:

	CTRL+C

The server:
- loads configuration from environment variables
- creates a request ID for every request
- logs every request with request ID
- recovers from panics
- applies request context timeouts
- protects shared state with a mutex
- gracefully shuts down on SIGINT/SIGTERM
- waits for active requests to finish
- cancels active requests through context
*/

//
// CONFIG
//

type Config struct {
	Port            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

func loadConfig() Config {
	return Config{
		Port:            getEnv("PORT", "8080"),
		ReadTimeout:     getDurationEnv("READ_TIMEOUT", 5*time.Second),
		WriteTimeout:    getDurationEnv("WRITE_TIMEOUT", 10*time.Second),
		IdleTimeout:     getDurationEnv("IDLE_TIMEOUT", 60*time.Second),
		ShutdownTimeout: getDurationEnv("SHUTDOWN_TIMEOUT", 10*time.Second),
	}
}

func getEnv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))

	if value == "" {
		return fallback
	}

	return value
}

func getDurationEnv(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))

	if value == "" {
		return fallback
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		log.Printf("invalid %s=%q, using default %s", key, value, fallback)
		return fallback
	}

	if duration <= 0 {
		log.Printf("invalid %s=%q, using default %s", key, value, fallback)
		return fallback
	}

	return duration
}

//
// MODEL
//

type Note struct {
	ID      int       `json:"id"`
	Title   string    `json:"title"`
	Content string    `json:"content"`
	Created time.Time `json:"created"`
}

type CreateNoteRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

//
// IN-MEMORY STORE
//

type NoteStore struct {
	mu     sync.RWMutex
	notes  map[int]Note
	nextID int
}

func NewNoteStore() *NoteStore {
	return &NoteStore{
		notes:  make(map[int]Note),
		nextID: 1,
	}
}

func (s *NoteStore) List() []Note {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Note, 0, len(s.notes))

	for _, note := range s.notes {
		result = append(result, note)
	}

	return result
}

func (s *NoteStore) Create(title, content string) Note {
	s.mu.Lock()
	defer s.mu.Unlock()

	note := Note{
		ID:      s.nextID,
		Title:   title,
		Content: content,
		Created: time.Now(),
	}

	s.notes[note.ID] = note
	s.nextID++

	return note
}

func (s *NoteStore) Delete(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.notes[id]; !exists {
		return false
	}

	delete(s.notes, id)

	return true
}

//
// REQUEST ID
//

type contextKey string

const requestIDKey contextKey = "request_id"

var requestCounter uint64

func generateRequestID() string {
	id := atomic.AddUint64(&requestCounter, 1)

	return fmt.Sprintf("req-%d-%d", time.Now().UnixNano(), id)
}

func getRequestID(ctx context.Context) string {
	value := ctx.Value(requestIDKey)

	if value == nil {
		return "unknown"
	}

	requestID, ok := value.(string)

	if !ok {
		return "unknown"
	}

	return requestID
}

//
// HANDLER
//

type App struct {
	store *NoteStore
}

func NewApp() *App {
	return &App{
		store: NewNoteStore(),
	}
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()

	// Business endpoints get a per-request context timeout so a
	// slow dependency can't hang a request forever.
	withTimeout := contextTimeoutMiddleware(10 * time.Second)

	mux.Handle("/health", withTimeout(http.HandlerFunc(a.healthHandler)))
	mux.Handle("/notes", withTimeout(http.HandlerFunc(a.notesHandler)))
	mux.Handle("/notes/", withTimeout(http.HandlerFunc(a.noteByIDHandler)))

	// Shutdown test endpoint.
	// This intentionally waits so SIGINT/SIGTERM can be tested
	// while a request is still running. It is deliberately NOT
	// wrapped in the context timeout above: that timeout would
	// cut it off on its own after 10s regardless of whether a
	// shutdown signal was ever sent, masking the actual drain
	// behavior this endpoint exists to demonstrate.
	mux.HandleFunc("/slow", a.slowHandler)

	return mux
}

func (a *App) healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

//
// NOTES
//

func (a *App) notesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.listNotes(w, r)

	case http.MethodPost:
		a.createNote(w, r)

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) listNotes(w http.ResponseWriter, r *http.Request) {
	notes := a.store.List()

	writeJSON(w, http.StatusOK, map[string]any{
		"notes": notes,
	})
}

func (a *App) createNote(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var input CreateNoteRequest

	decoder := json.NewDecoder(r.Body)

	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	input.Title = strings.TrimSpace(input.Title)
	input.Content = strings.TrimSpace(input.Content)

	if input.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	note := a.store.Create(
		input.Title,
		input.Content,
	)

	writeJSON(w, http.StatusCreated, note)
}

func (a *App) noteByIDHandler(w http.ResponseWriter, r *http.Request) {
	idString := strings.TrimPrefix(r.URL.Path, "/notes/")

	if idString == "" {
		writeError(w, http.StatusBadRequest, "note ID is required")
		return
	}

	id, err := strconv.Atoi(idString)

	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid note ID")
		return
	}

	switch r.Method {
	case http.MethodDelete:
		a.deleteNote(w, r, id)

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) deleteNote(w http.ResponseWriter, r *http.Request, id int) {
	deleted := a.store.Delete(id)

	if !deleted {
		writeError(w, http.StatusNotFound, "note not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message": "note deleted",
		"id":      id,
	})
}

//
// SLOW REQUEST
//

func (a *App) slowHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Kept under the default 10s ShutdownTimeout so the request can
	// always finish (or be observed draining) before Shutdown gives up,
	// no matter when during its lifetime the signal arrives.
	seconds := 5 * time.Second

	timer := time.NewTimer(seconds)
	defer timer.Stop()

	select {
	case <-timer.C:
		writeJSON(w, http.StatusOK, map[string]string{
			"message": "slow request completed",
		})

	case <-r.Context().Done():
		log.Printf(
			"request_id=%s slow request cancelled: %v",
			getRequestID(r.Context()),
			r.Context().Err(),
		)
	}
}

//
// JSON HELPERS
//

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")

	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("failed to encode response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{
		"error": message,
	})
}

//
// MIDDLEWARE
//

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := generateRequestID()

		ctx := context.WithValue(
			r.Context(),
			requestIDKey,
			requestID,
		)

		w.Header().Set("X-Request-ID", requestID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		requestID := getRequestID(r.Context())

		log.Printf(
			"request_id=%s method=%s path=%s started",
			requestID,
			r.Method,
			r.URL.Path,
		)

		next.ServeHTTP(w, r)

		log.Printf(
			"request_id=%s method=%s path=%s duration=%s completed",
			requestID,
			r.Method,
			r.URL.Path,
			time.Since(start),
		)
	})
}

func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				requestID := getRequestID(r.Context())

				log.Printf(
					"request_id=%s panic=%v",
					requestID,
					recovered,
				)

				writeError(
					w,
					http.StatusInternalServerError,
					"internal server error",
				)
			}
		}()

		next.ServeHTTP(w, r)
	})
}

func contextTimeoutMiddleware(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(
				r.Context(),
				timeout,
			)
			defer cancel()

			next.ServeHTTP(
				w,
				r.WithContext(ctx),
			)
		})
	}
}

func middlewareChain(
	handler http.Handler,
	middlewares ...func(http.Handler) http.Handler,
) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}

	return handler
}

//
// SERVER
//

func newServer(config Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:         ":" + config.Port,
		Handler:      handler,
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
		IdleTimeout:  config.IdleTimeout,
	}
}

//
// MAIN
//

func main() {
	config := loadConfig()

	app := NewApp()

	handler := middlewareChain(
		app.routes(),

		// Outer -> Inner:
		// Request ID
		// Logging
		// Recovery
		//
		// The per-request context timeout is applied selectively
		// inside routes(), not here, so it doesn't also cut off
		// the /slow shutdown-test endpoint.
		requestIDMiddleware,
		loggingMiddleware,
		recoveryMiddleware,
	)

	server := newServer(
		config,
		handler,
	)

	serverErrors := make(chan error, 1)

	go func() {
		log.Printf(
			"server starting addr=:%s read_timeout=%s write_timeout=%s idle_timeout=%s shutdown_timeout=%s",
			config.Port,
			config.ReadTimeout,
			config.WriteTimeout,
			config.IdleTimeout,
			config.ShutdownTimeout,
		)

		if err := server.ListenAndServe(); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	shutdownSignal := make(chan os.Signal, 1)

	signal.Notify(
		shutdownSignal,
		syscall.SIGINT,
		syscall.SIGTERM,
	)

	select {
	case err := <-serverErrors:
		log.Fatalf("server error: %v", err)

	case signal := <-shutdownSignal:
		log.Printf("shutdown signal received: %s", signal)
	}

	shutdownCtx, cancel := context.WithTimeout(
		context.Background(),
		config.ShutdownTimeout,
	)
	defer cancel()

	log.Printf("graceful shutdown started")

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)

		if closeErr := server.Close(); closeErr != nil {
			log.Printf("forced server close failed: %v", closeErr)
		}
	}

	log.Printf("server stopped cleanly")
}
