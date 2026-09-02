package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ============================================================
// DOMAIN
// ============================================================

type Task struct {
	ID        int       `json:"id"`
	Title     string    `json:"title"`
	Completed bool      `json:"completed"`
	CreatedAt time.Time `json:"created_at"`
}

// ============================================================
// STORE
// ============================================================

type Store interface {
	Create(task Task) (Task, error)
	Get(id int) (Task, error)
	List() []Task
}

var ErrTaskNotFound = errors.New("task not found")

type InMemoryStore struct {
	mu     sync.RWMutex
	tasks  map[int]Task
	nextID int64
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		tasks: make(map[int]Task),
	}
}

func (s *InMemoryStore) Create(task Task) (Task, error) {
	if strings.TrimSpace(task.Title) == "" {
		return Task{}, errors.New("title is required")
	}

	task.ID = int(atomic.AddInt64(&s.nextID, 1))
	task.Title = strings.TrimSpace(task.Title)
	task.CreatedAt = time.Now()

	s.mu.Lock()
	s.tasks[task.ID] = task
	s.mu.Unlock()

	return task, nil
}

func (s *InMemoryStore) Get(id int) (Task, error) {
	s.mu.RLock()
	task, ok := s.tasks[id]
	s.mu.RUnlock()

	if !ok {
		return Task{}, ErrTaskNotFound
	}

	return task, nil
}

func (s *InMemoryStore) List() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Task, 0, len(s.tasks))

	for _, task := range s.tasks {
		result = append(result, task)
	}

	return result
}

// ============================================================
// CONFIG
// ============================================================

type Config struct {
	AppName string
	Env     string
	Port    string
}

// ============================================================
// CONTEXT KEYS
// ============================================================
//
// Context key olarak string kullanmak yerine private type
// kullanıyoruz.
//
// Böylece başka package'ların aynı key ile collision oluşturma
// ihtimalini azaltıyoruz.
// ============================================================

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	loggerKey    contextKey = "logger"
	configKey    contextKey = "config"
	storeKey     contextKey = "store"
)

// ============================================================
// CONTEXT HELPERS
// ============================================================

func withRequestID(
	ctx context.Context,
	requestID string,
) context.Context {

	return context.WithValue(
		ctx,
		requestIDKey,
		requestID,
	)
}

func requestIDFromContext(
	ctx context.Context,
) string {

	value := ctx.Value(requestIDKey)

	requestID, ok := value.(string)

	if !ok {
		return ""
	}

	return requestID
}

func withLogger(
	ctx context.Context,
	logger *log.Logger,
) context.Context {

	return context.WithValue(
		ctx,
		loggerKey,
		logger,
	)
}

func loggerFromContext(
	ctx context.Context,
) *log.Logger {

	value := ctx.Value(loggerKey)

	logger, ok := value.(*log.Logger)

	if !ok {
		return nil
	}

	return logger
}

func withConfig(
	ctx context.Context,
	config Config,
) context.Context {

	return context.WithValue(
		ctx,
		configKey,
		config,
	)
}

func configFromContext(
	ctx context.Context,
) (Config, bool) {

	value := ctx.Value(configKey)

	config, ok := value.(Config)

	return config, ok
}

func withStore(
	ctx context.Context,
	store Store,
) context.Context {

	return context.WithValue(
		ctx,
		storeKey,
		store,
	)
}

func storeFromContext(
	ctx context.Context,
) (Store, bool) {

	value := ctx.Value(storeKey)

	store, ok := value.(Store)

	return store, ok
}

// ============================================================
// MIDDLEWARE TYPE
// ============================================================

type Middleware func(http.Handler) http.Handler

// ============================================================
// REQUEST ID MIDDLEWARE
// ============================================================
//
// Task:
// Context Middleware
//
// Client X-Request-ID gönderirse onu kullanıyoruz.
//
// Göndermezse kendimiz request ID oluşturuyoruz.
//
// Sonra request ID:
// 1. Context'e
// 2. Response header'a
//
// ekleniyor.
// ============================================================

func requestIDMiddleware(
	next http.Handler,
) http.Handler {

	return http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {

		requestID := r.Header.Get("X-Request-ID")

		if requestID == "" {
			requestID = fmt.Sprintf(
				"req-%d",
				time.Now().UnixNano(),
			)
		}

		ctx := withRequestID(
			r.Context(),
			requestID,
		)

		w.Header().Set(
			"X-Request-ID",
			requestID,
		)

		next.ServeHTTP(
			w,
			r.WithContext(ctx),
		)
	})
}

// ============================================================
// LOGGER MIDDLEWARE
// ============================================================
//
// Request ID middleware'den sonra çalıştığı için logger
// context içerisindeki request ID'yi kullanabilir.
// ============================================================

func loggerMiddleware(
	logger *log.Logger,
) Middleware {

	return func(next http.Handler) http.Handler {

		return http.HandlerFunc(func(
			w http.ResponseWriter,
			r *http.Request,
		) {

			ctx := withLogger(
				r.Context(),
				logger,
			)

			requestID := requestIDFromContext(ctx)

			logger.Printf(
				"request started method=%s path=%s request_id=%s",
				r.Method,
				r.URL.Path,
				requestID,
			)

			start := time.Now()

			next.ServeHTTP(
				w,
				r.WithContext(ctx),
			)

			logger.Printf(
				"request completed method=%s path=%s request_id=%s duration=%s",
				r.Method,
				r.URL.Path,
				requestID,
				time.Since(start),
			)
		})
	}
}

// ============================================================
// CONFIG MIDDLEWARE
// ============================================================
//
// Task:
// Config Middleware
//
// Config global variable olarak tutulmuyor.
//
// Her request'in context'ine dependency olarak inject ediliyor.
// ============================================================

func configMiddleware(
	config Config,
) Middleware {

	return func(next http.Handler) http.Handler {

		return http.HandlerFunc(func(
			w http.ResponseWriter,
			r *http.Request,
		) {

			ctx := withConfig(
				r.Context(),
				config,
			)

			next.ServeHTTP(
				w,
				r.WithContext(ctx),
			)
		})
	}
}

// ============================================================
// STORE MIDDLEWARE
// ============================================================
//
// Store dependency'sini context üzerinden handler'a
// ulaştırıyoruz.
// ============================================================

func storeMiddleware(
	store Store,
) Middleware {

	return func(next http.Handler) http.Handler {

		return http.HandlerFunc(func(
			w http.ResponseWriter,
			r *http.Request,
		) {

			ctx := withStore(
				r.Context(),
				store,
			)

			next.ServeHTTP(
				w,
				r.WithContext(ctx),
			)
		})
	}
}

// ============================================================
// RECOVERY MIDDLEWARE
// ============================================================
//
// Middleware sıralamasında recovery dışarıda olmalı.
//
// Böylece içerideki middleware veya handler panic ederse
// recovery bunu yakalayabilir.
// ============================================================

func recoveryMiddleware(
	logger *log.Logger,
) Middleware {

	return func(next http.Handler) http.Handler {

		return http.HandlerFunc(func(
			w http.ResponseWriter,
			r *http.Request,
		) {

			defer func() {

				if recovered := recover(); recovered != nil {

					requestID := requestIDFromContext(
						r.Context(),
					)

					logger.Printf(
						"panic recovered request_id=%s panic=%v",
						requestID,
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
}

// ============================================================
// MIDDLEWARE CHAIN
// ============================================================
//
// Task:
// Compose Chains
//
// Kullanım:
//
// Chain(
//     handler,
//     middlewareA,
//     middlewareB,
//     middlewareC,
// )
//
// şeklinde.
//
// Beklenen execution:
//
// A
//   ↓
// B
//   ↓
// C
//   ↓
// Handler
//
// Response dönüşünde ise:
//
// Handler
//   ↑
// C
//   ↑
// B
//   ↑
// A
// ============================================================

func Chain(
	handler http.Handler,
	middlewares ...Middleware,
) http.Handler {

	for i := len(middlewares) - 1; i >= 0; i-- {

		handler = middlewares[i](handler)
	}

	return handler
}

// ============================================================
// HANDLER
// ============================================================
//
// Handler dependency'leri global değişkenlerden almıyor.
//
// Config ve Store context üzerinden geliyor.
// ============================================================

func createTaskHandler(
	w http.ResponseWriter,
	r *http.Request,
) {

	if r.Method != http.MethodPost {

		http.Error(
			w,
			"method not allowed",
			http.StatusMethodNotAllowed,
		)

		return
	}

	// --------------------------------------------------------
	// Context'ten request ID
	// --------------------------------------------------------

	requestID := requestIDFromContext(
		r.Context(),
	)

	// --------------------------------------------------------
	// Context'ten logger
	// --------------------------------------------------------

	logger := loggerFromContext(
		r.Context(),
	)

	if logger != nil {

		logger.Printf(
			"handler executing request_id=%s",
			requestID,
		)
	}

	// --------------------------------------------------------
	// Context'ten config
	// --------------------------------------------------------

	config, ok := configFromContext(
		r.Context(),
	)

	if !ok {

		http.Error(
			w,
			"config dependency missing",
			http.StatusInternalServerError,
		)

		return
	}

	// --------------------------------------------------------
	// Context'ten store
	// --------------------------------------------------------

	store, ok := storeFromContext(
		r.Context(),
	)

	if !ok {

		http.Error(
			w,
			"store dependency missing",
			http.StatusInternalServerError,
		)

		return
	}

	// --------------------------------------------------------
	// Request parameters
	// --------------------------------------------------------

	title := r.URL.Query().Get("title")

	if strings.TrimSpace(title) == "" {

		http.Error(
			w,
			"title is required",
			http.StatusBadRequest,
		)

		return
	}

	// --------------------------------------------------------
	// Create
	// --------------------------------------------------------

	task, err := store.Create(Task{
		Title: title,
	})

	if err != nil {

		http.Error(
			w,
			err.Error(),
			http.StatusBadRequest,
		)

		return
	}

	// --------------------------------------------------------
	// Response headers
	// --------------------------------------------------------

	w.Header().Set(
		"Content-Type",
		"text/plain; charset=utf-8",
	)

	w.Header().Set(
		"X-App-Name",
		config.AppName,
	)

	w.Header().Set(
		"X-Environment",
		config.Env,
	)

	w.Header().Set(
		"X-Request-ID",
		requestID,
	)

	w.WriteHeader(
		http.StatusCreated,
	)

	fmt.Fprintf(
		w,
		"task created: id=%d title=%q app=%s env=%s request_id=%s\n",
		task.ID,
		task.Title,
		config.AppName,
		config.Env,
		requestID,
	)
}

// ============================================================
// LIST HANDLER
// ============================================================

func listTasksHandler(
	w http.ResponseWriter,
	r *http.Request,
) {

	if r.Method != http.MethodGet {

		http.Error(
			w,
			"method not allowed",
			http.StatusMethodNotAllowed,
		)

		return
	}

	store, ok := storeFromContext(
		r.Context(),
	)

	if !ok {

		http.Error(
			w,
			"store dependency missing",
			http.StatusInternalServerError,
		)

		return
	}

	tasks := store.List()

	w.Header().Set(
		"Content-Type",
		"text/plain; charset=utf-8",
	)

	w.Header().Set(
		"X-Request-ID",
		requestIDFromContext(
			r.Context(),
		),
	)

	for _, task := range tasks {

		fmt.Fprintf(
			w,
			"id=%d title=%q completed=%t\n",
			task.ID,
			task.Title,
			task.Completed,
		)
	}
}

// ============================================================
// GET TASK HANDLER
// ============================================================

func getTaskHandler(
	w http.ResponseWriter,
	r *http.Request,
) {

	if r.Method != http.MethodGet {

		http.Error(
			w,
			"method not allowed",
			http.StatusMethodNotAllowed,
		)

		return
	}

	store, ok := storeFromContext(
		r.Context(),
	)

	if !ok {

		http.Error(
			w,
			"store dependency missing",
			http.StatusInternalServerError,
		)

		return
	}

	idText := strings.TrimPrefix(
		r.URL.Path,
		"/tasks/",
	)

	id, err := strconv.Atoi(idText)

	if err != nil {

		http.Error(
			w,
			"invalid task id",
			http.StatusBadRequest,
		)

		return
	}

	task, err := store.Get(id)

	if err != nil {

		if errors.Is(err, ErrTaskNotFound) {

			http.Error(
				w,
				"task not found",
				http.StatusNotFound,
			)

			return
		}

		http.Error(
			w,
			"internal server error",
			http.StatusInternalServerError,
		)

		return
	}

	w.Header().Set(
		"Content-Type",
		"text/plain; charset=utf-8",
	)

	w.Header().Set(
		"X-Request-ID",
		requestIDFromContext(
			r.Context(),
		),
	)

	fmt.Fprintf(
		w,
		"id=%d title=%q completed=%t\n",
		task.ID,
		task.Title,
		task.Completed,
	)
}

// ============================================================
// HEALTH HANDLER
// ============================================================

func healthHandler(
	w http.ResponseWriter,
	r *http.Request,
) {

	config, ok := configFromContext(
		r.Context(),
	)

	if !ok {

		http.Error(
			w,
			"config dependency missing",
			http.StatusInternalServerError,
		)

		return
	}

	requestID := requestIDFromContext(
		r.Context(),
	)

	w.Header().Set(
		"Content-Type",
		"text/plain; charset=utf-8",
	)

	w.Header().Set(
		"X-Request-ID",
		requestID,
	)

	w.WriteHeader(
		http.StatusOK,
	)

	fmt.Fprintf(
		w,
		"status=ok app=%s env=%s request_id=%s\n",
		config.AppName,
		config.Env,
		requestID,
	)
}

// ============================================================
// ROUTER
// ============================================================

func buildRouter(
	config Config,
	store Store,
	logger *log.Logger,
) http.Handler {

	mux := http.NewServeMux()

	mux.HandleFunc(
		"/tasks",
		func(
			w http.ResponseWriter,
			r *http.Request,
		) {

			switch r.Method {

			case http.MethodPost:
				createTaskHandler(w, r)

			case http.MethodGet:
				listTasksHandler(w, r)

			default:

				http.Error(
					w,
					"method not allowed",
					http.StatusMethodNotAllowed,
				)
			}
		},
	)

	mux.HandleFunc(
		"/tasks/",
		getTaskHandler,
	)

	mux.HandleFunc(
		"/health",
		healthHandler,
	)

	// ========================================================
	// MIDDLEWARE ORDER
	// ========================================================
	//
	// 1. Recovery
	// 2. Request ID
	// 3. Logger
	// 4. Config
	// 5. Store
	// 6. Handler
	//
	// Recovery en dışta olduğu için içeride oluşan panic'leri
	// yakalayabilir.
	// ========================================================

	return Chain(
		mux,

		recoveryMiddleware(logger),

		requestIDMiddleware,

		loggerMiddleware(logger),

		configMiddleware(config),

		storeMiddleware(store),
	)
}

// ============================================================
// MAIN
// ============================================================

func main() {

	// --------------------------------------------------------
	// CONFIG
	// --------------------------------------------------------

	config := Config{
		AppName: "task-api",
		Env:     "development",
		Port:    "8080",
	}

	// --------------------------------------------------------
	// STORE
	// --------------------------------------------------------

	store := NewInMemoryStore()

	// --------------------------------------------------------
	// LOGGER
	// --------------------------------------------------------

	var logBuffer bytes.Buffer

	logger := log.New(
		&logBuffer,
		"[TASK-API] ",
		log.LstdFlags,
	)

	// --------------------------------------------------------
	// ROUTER + MIDDLEWARE
	// --------------------------------------------------------

	handler := buildRouter(
		config,
		store,
		logger,
	)

	// --------------------------------------------------------
	// SERVER
	// --------------------------------------------------------

	server := &http.Server{
		Addr:    ":" + config.Port,
		Handler: handler,
	}

	fmt.Println(
		"server running on http://localhost:" + config.Port,
	)

	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println(
		"POST http://localhost:8080/tasks?title=Learn%20Go",
	)
	fmt.Println(
		"GET  http://localhost:8080/tasks",
	)
	fmt.Println(
		"GET  http://localhost:8080/tasks/1",
	)
	fmt.Println(
		"GET  http://localhost:8080/health",
	)

	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
