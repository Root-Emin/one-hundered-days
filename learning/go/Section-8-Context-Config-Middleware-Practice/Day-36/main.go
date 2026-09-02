package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
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
// ERRORS
// ============================================================

var (
	ErrTaskNotFound = errors.New("task not found")
	ErrEmptyTitle   = errors.New("task title cannot be empty")
	ErrTimeout      = errors.New("operation timed out")
)

// ============================================================
// REQUEST DTO
// ============================================================

type CreateTaskRequest struct {
	Title string `json:"title"`
}

// ============================================================
// ERROR RESPONSE
// ============================================================

type ErrorResponse struct {
	Error APIError `json:"error"`
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ============================================================
// IN-MEMORY STORE
// ============================================================

type TaskStore struct {
	mu     sync.RWMutex
	tasks  map[int]Task
	nextID int
}

func NewTaskStore() *TaskStore {
	return &TaskStore{
		tasks:  make(map[int]Task),
		nextID: 1,
	}
}

// ============================================================
// CREATE
// ============================================================

func (s *TaskStore) Create(
	ctx context.Context,
	title string,
) (Task, error) {

	// --------------------------------------------------------
	// CONTEXT CHECK
	// --------------------------------------------------------

	select {
	case <-ctx.Done():
		return Task{}, ctx.Err()

	default:
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	task := Task{
		ID:        s.nextID,
		Title:     title,
		Completed: false,
		CreatedAt: time.Now(),
	}

	s.tasks[task.ID] = task
	s.nextID++

	return task, nil
}

// ============================================================
// GET
// ============================================================

func (s *TaskStore) Get(
	ctx context.Context,
	id int,
) (Task, error) {

	// Simulate a slow database operation.
	//
	// Gerçek hayatta burası:
	// - PostgreSQL
	// - Redis
	// - external API
	// - filesystem
	// gibi yavaşlayabilecek bir işlem olabilir.

	select {

	case <-time.After(500 * time.Millisecond):
		// Operation completed.

	case <-ctx.Done():
		// Request was cancelled or timed out.
		return Task{}, ctx.Err()
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	task, exists := s.tasks[id]

	if !exists {
		return Task{}, ErrTaskNotFound
	}

	return task, nil
}

// ============================================================
// LIST
// ============================================================

func (s *TaskStore) List(
	ctx context.Context,
) ([]Task, error) {

	s.mu.RLock()
	defer s.mu.RUnlock()

	tasks := make([]Task, 0, len(s.tasks))

	for _, task := range s.tasks {

		// ----------------------------------------------------
		// IMPORTANT:
		// Check cancellation inside loops.
		// ----------------------------------------------------

		select {

		case <-ctx.Done():
			return nil, ctx.Err()

		default:
		}

		tasks = append(tasks, task)
	}

	return tasks, nil
}

// ============================================================
// SERVICE
// ============================================================

type TaskService struct {
	store *TaskStore
}

func NewTaskService(store *TaskStore) *TaskService {
	return &TaskService{
		store: store,
	}
}

// ============================================================
// CREATE TASK
// ============================================================

func (s *TaskService) CreateTask(
	ctx context.Context,
	title string,
) (Task, error) {

	// --------------------------------------------------------
	// Validate context before expensive work.
	// --------------------------------------------------------

	if err := ctx.Err(); err != nil {
		return Task{}, err
	}

	title = strings.TrimSpace(title)

	if title == "" {
		return Task{}, ErrEmptyTitle
	}

	return s.store.Create(ctx, title)
}

// ============================================================
// GET TASK WITH DEADLINE
// ============================================================

func (s *TaskService) GetTask(
	ctx context.Context,
	id int,
) (Task, error) {

	// --------------------------------------------------------
	// SET DEADLINE
	//
	// Store operation has at most 2 seconds.
	// --------------------------------------------------------

	ctx, cancel := context.WithTimeout(
		ctx,
		2*time.Second,
	)

	defer cancel()

	// --------------------------------------------------------
	// Respect cancellation before expensive operation.
	// --------------------------------------------------------

	if err := ctx.Err(); err != nil {
		return Task{}, err
	}

	return s.store.Get(ctx, id)
}

// ============================================================
// LIST TASKS
// ============================================================

func (s *TaskService) ListTasks(
	ctx context.Context,
) ([]Task, error) {

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return s.store.List(ctx)
}

// ============================================================
// BACKGROUND WORK
// ============================================================

func processTask(
	ctx context.Context,
	taskID int,
) error {

	log.Printf(
		"background worker started for task %d",
		taskID,
	)

	// --------------------------------------------------------
	// Simulate expensive background work.
	// --------------------------------------------------------

	for step := 1; step <= 10; step++ {

		// ----------------------------------------------------
		// VERY IMPORTANT:
		// Check cancellation before every expensive step.
		// ----------------------------------------------------

		select {

		case <-ctx.Done():

			log.Printf(
				"background worker cancelled for task %d: %v",
				taskID,
				ctx.Err(),
			)

			return ctx.Err()

		default:
		}

		log.Printf(
			"task %d -> processing step %d/10",
			taskID,
			step,
		)

		time.Sleep(300 * time.Millisecond)
	}

	log.Printf(
		"background worker completed for task %d",
		taskID,
	)

	return nil
}

// ============================================================
// HTTP HANDLER
// ============================================================

type TaskHandler struct {
	service *TaskService
}

func NewTaskHandler(service *TaskService) *TaskHandler {
	return &TaskHandler{
		service: service,
	}
}

// ============================================================
// JSON RESPONSE
// ============================================================

func writeJSON(
	w http.ResponseWriter,
	status int,
	data any,
) {

	w.Header().Set(
		"Content-Type",
		"application/json; charset=utf-8",
	)

	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf(
			"failed to encode response: %v",
			err,
		)
	}
}

// ============================================================
// ERROR RESPONSE
// ============================================================

func writeError(
	w http.ResponseWriter,
	status int,
	code string,
	message string,
) {

	writeJSON(
		w,
		status,
		ErrorResponse{
			Error: APIError{
				Code:    code,
				Message: message,
			},
		},
	)
}

// ============================================================
// POST /api/v1/tasks
// ============================================================

func (h *TaskHandler) CreateTask(
	w http.ResponseWriter,
	r *http.Request,
) {

	if r.Method != http.MethodPost {

		writeError(
			w,
			http.StatusMethodNotAllowed,
			"METHOD_NOT_ALLOWED",
			"method not allowed",
		)

		return
	}

	// --------------------------------------------------------
	// Request-scoped context.
	//
	// r.Context() is cancelled when the request is cancelled.
	// --------------------------------------------------------

	ctx := r.Context()

	var request CreateTaskRequest

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {

		writeError(
			w,
			http.StatusBadRequest,
			"INVALID_JSON",
			"invalid request body",
		)

		return
	}

	task, err := h.service.CreateTask(
		ctx,
		request.Title,
	)

	if err != nil {

		if errors.Is(err, ErrEmptyTitle) {

			writeError(
				w,
				http.StatusBadRequest,
				"EMPTY_TITLE",
				"task title cannot be empty",
			)

			return
		}

		if errors.Is(err, context.Canceled) {

			writeError(
				w,
				http.StatusRequestTimeout,
				"REQUEST_CANCELLED",
				"request was cancelled",
			)

			return
		}

		writeError(
			w,
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"internal server error",
		)

		return
	}

	writeJSON(
		w,
		http.StatusCreated,
		task,
	)
}

// ============================================================
// GET /api/v1/tasks/{id}
// ============================================================

func (h *TaskHandler) GetTask(
	w http.ResponseWriter,
	r *http.Request,
) {

	if r.Method != http.MethodGet {

		writeError(
			w,
			http.StatusMethodNotAllowed,
			"METHOD_NOT_ALLOWED",
			"method not allowed",
		)

		return
	}

	idText := strings.TrimPrefix(
		r.URL.Path,
		"/api/v1/tasks/",
	)

	id, err := strconv.Atoi(idText)

	if err != nil || id <= 0 {

		writeError(
			w,
			http.StatusBadRequest,
			"INVALID_ID",
			"task id must be a positive integer",
		)

		return
	}

	// --------------------------------------------------------
	// PASS REQUEST CONTEXT INTO SERVICE
	// --------------------------------------------------------

	task, err := h.service.GetTask(
		r.Context(),
		id,
	)

	if err != nil {

		switch {

		case errors.Is(err, ErrTaskNotFound):

			writeError(
				w,
				http.StatusNotFound,
				"TASK_NOT_FOUND",
				"task not found",
			)

		case errors.Is(err, context.DeadlineExceeded):

			writeError(
				w,
				http.StatusGatewayTimeout,
				"DEADLINE_EXCEEDED",
				"task lookup exceeded its deadline",
			)

		case errors.Is(err, context.Canceled):

			writeError(
				w,
				http.StatusRequestTimeout,
				"REQUEST_CANCELLED",
				"request was cancelled",
			)

		default:

			writeError(
				w,
				http.StatusInternalServerError,
				"INTERNAL_ERROR",
				"internal server error",
			)
		}

		return
	}

	writeJSON(
		w,
		http.StatusOK,
		task,
	)
}

// ============================================================
// GET /api/v1/tasks
// ============================================================

func (h *TaskHandler) ListTasks(
	w http.ResponseWriter,
	r *http.Request,
) {

	if r.Method != http.MethodGet {

		writeError(
			w,
			http.StatusMethodNotAllowed,
			"METHOD_NOT_ALLOWED",
			"method not allowed",
		)

		return
	}

	tasks, err := h.service.ListTasks(
		r.Context(),
	)

	if err != nil {

		if errors.Is(err, context.Canceled) {

			writeError(
				w,
				http.StatusRequestTimeout,
				"REQUEST_CANCELLED",
				"request was cancelled",
			)

			return
		}

		writeError(
			w,
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"internal server error",
		)

		return
	}

	writeJSON(
		w,
		http.StatusOK,
		map[string]any{
			"tasks": tasks,
			"count": len(tasks),
		},
	)
}

// ============================================================
// POST /api/v1/tasks/{id}/process
//
// Demonstrates:
// - context.WithCancel
// - ctx.Done()
// - cancellable background work
// ============================================================

func (h *TaskHandler) ProcessTask(
	w http.ResponseWriter,
	r *http.Request,
) {

	if r.Method != http.MethodPost {

		writeError(
			w,
			http.StatusMethodNotAllowed,
			"METHOD_NOT_ALLOWED",
			"method not allowed",
		)

		return
	}

	idText := strings.TrimPrefix(
		r.URL.Path,
		"/api/v1/tasks/",
	)

	idText = strings.TrimSuffix(
		idText,
		"/process",
	)

	id, err := strconv.Atoi(idText)

	if err != nil || id <= 0 {

		writeError(
			w,
			http.StatusBadRequest,
			"INVALID_ID",
			"task id must be a positive integer",
		)

		return
	}

	// --------------------------------------------------------
	// Verify task exists.
	// --------------------------------------------------------

	_, err = h.service.GetTask(
		r.Context(),
		id,
	)

	if err != nil {

		if errors.Is(err, ErrTaskNotFound) {

			writeError(
				w,
				http.StatusNotFound,
				"TASK_NOT_FOUND",
				"task not found",
			)

			return
		}

		if errors.Is(err, context.DeadlineExceeded) {

			writeError(
				w,
				http.StatusGatewayTimeout,
				"DEADLINE_EXCEEDED",
				"task lookup exceeded its deadline",
			)

			return
		}

		writeError(
			w,
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"internal server error",
		)

		return
	}

	// --------------------------------------------------------
	// CONTEXT.WITHCANCEL
	// --------------------------------------------------------
	//
	// Create a child context.
	//
	// If the request is cancelled, the parent context is
	// cancelled and our child context follows it.
	//
	// We can also explicitly call cancel().
	// --------------------------------------------------------

	workCtx, cancel := context.WithCancel(
		r.Context(),
	)

	defer cancel()

	result := make(chan error, 1)

	// --------------------------------------------------------
	// START BACKGROUND WORKER
	// --------------------------------------------------------

	go func() {

		result <- processTask(
			workCtx,
			id,
		)

	}()

	// --------------------------------------------------------
	// WAIT FOR:
	//
	// 1. Worker completion
	// 2. Request cancellation
	// --------------------------------------------------------

	select {

	case err := <-result:

		if err != nil {

			if errors.Is(err, context.Canceled) {

				writeError(
					w,
					http.StatusRequestTimeout,
					"REQUEST_CANCELLED",
					"background work was cancelled",
				)

				return
			}

			if errors.Is(err, context.DeadlineExceeded) {

				writeError(
					w,
					http.StatusGatewayTimeout,
					"DEADLINE_EXCEEDED",
					"background work exceeded its deadline",
				)

				return
			}

			writeError(
				w,
				http.StatusInternalServerError,
				"PROCESSING_FAILED",
				"background processing failed",
			)

			return
		}

		writeJSON(
			w,
			http.StatusOK,
			map[string]any{
				"message": "task processed successfully",
				"task_id": id,
			},
		)

	case <-r.Context().Done():

		// ----------------------------------------------------
		// CLIENT DISCONNECTED / REQUEST CANCELLED
		// ----------------------------------------------------

		cancel()

		log.Printf(
			"request cancelled, stopping task %d",
			id,
		)

		// Client may already be gone, so there is no reason
		// to attempt another response.
		return
	}
}

// ============================================================
// ROUTER
// ============================================================

func buildRouter(
	handler *TaskHandler,
) http.Handler {

	mux := http.NewServeMux()

	// --------------------------------------------------------
	// Health
	// --------------------------------------------------------

	mux.HandleFunc(
		"/health",
		func(w http.ResponseWriter, r *http.Request) {

			if r.Method != http.MethodGet {

				writeError(
					w,
					http.StatusMethodNotAllowed,
					"METHOD_NOT_ALLOWED",
					"method not allowed",
				)

				return
			}

			writeJSON(
				w,
				http.StatusOK,
				map[string]string{
					"status": "ok",
				},
			)
		},
	)

	// --------------------------------------------------------
	// GET + POST /api/v1/tasks
	// --------------------------------------------------------

	mux.HandleFunc(
		"/api/v1/tasks",
		func(w http.ResponseWriter, r *http.Request) {

			switch r.Method {

			case http.MethodGet:

				handler.ListTasks(w, r)

			case http.MethodPost:

				handler.CreateTask(w, r)

			default:

				writeError(
					w,
					http.StatusMethodNotAllowed,
					"METHOD_NOT_ALLOWED",
					"method not allowed",
				)
			}
		},
	)

	// --------------------------------------------------------
	// /api/v1/tasks/{id}
	// /api/v1/tasks/{id}/process
	// --------------------------------------------------------

	mux.HandleFunc(
		"/api/v1/tasks/",
		func(w http.ResponseWriter, r *http.Request) {

			if strings.HasSuffix(
				r.URL.Path,
				"/process",
			) {

				handler.ProcessTask(
					w,
					r,
				)

				return
			}

			handler.GetTask(
				w,
				r,
			)
		},
	)

	// --------------------------------------------------------
	// 404
	// --------------------------------------------------------

	mux.HandleFunc(
		"/",
		func(w http.ResponseWriter, r *http.Request) {

			writeError(
				w,
				http.StatusNotFound,
				"NOT_FOUND",
				"endpoint not found",
			)
		},
	)

	return loggingMiddleware(mux)
}

// ============================================================
// LOGGING MIDDLEWARE
// ============================================================

func loggingMiddleware(
	next http.Handler,
) http.Handler {

	return http.HandlerFunc(
		func(
			w http.ResponseWriter,
			r *http.Request,
		) {

			start := time.Now()

			next.ServeHTTP(w, r)

			log.Printf(
				"%s %s %s",
				r.Method,
				r.URL.Path,
				time.Since(start),
			)
		},
	)
}

// ============================================================
// MAIN
// ============================================================

func main() {

	store := NewTaskStore()

	service := NewTaskService(
		store,
	)

	handler := NewTaskHandler(
		service,
	)

	router := buildRouter(
		handler,
	)

	server := &http.Server{
		Addr:              ":8080",
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	fmt.Println("==========================================")
	fmt.Println("Day 36 - Context API")
	fmt.Println("==========================================")
	fmt.Println("Server: http://localhost:8080")
	fmt.Println()
	fmt.Println("Endpoints:")
	fmt.Println()
	fmt.Println("GET    /health")
	fmt.Println("GET    /api/v1/tasks")
	fmt.Println("POST   /api/v1/tasks")
	fmt.Println("GET    /api/v1/tasks/{id}")
	fmt.Println("POST   /api/v1/tasks/{id}/process")
	fmt.Println()
	fmt.Println("Context features:")
	fmt.Println("- request context propagation")
	fmt.Println("- context.WithCancel")
	fmt.Println("- context.WithTimeout")
	fmt.Println("- ctx.Done()")
	fmt.Println("- ctx.Err()")
	fmt.Println("==========================================")

	log.Fatal(
		server.ListenAndServe(),
	)
}
