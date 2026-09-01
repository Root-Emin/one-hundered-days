package main

import (
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
// DOMAIN MODEL
// ============================================================

type Task struct {
	ID        int       `json:"id"`
	Title     string    `json:"title"`
	Completed bool      `json:"completed"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ============================================================
// DTOs
// Data Transfer Objects
// ============================================================

type CreateTaskRequest struct {
	Title string `json:"title"`
}

type UpdateTaskRequest struct {
	Title     string `json:"title"`
	Completed bool   `json:"completed"`
}

type TaskResponse struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Completed bool   `json:"completed"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type TaskListResponse struct {
	Tasks []TaskResponse `json:"tasks"`
	Count int            `json:"count"`
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
// DOMAIN ERRORS
// ============================================================

var (
	ErrTaskNotFound = errors.New("task not found")
	ErrEmptyTitle   = errors.New("task title cannot be empty")
	ErrInvalidID    = errors.New("invalid task id")
	ErrInvalidJSON  = errors.New("invalid request body")
)

// ============================================================
// IN-MEMORY REPOSITORY
// ============================================================

type TaskRepository struct {
	mu     sync.RWMutex
	tasks  map[int]Task
	nextID int
}

func NewTaskRepository() *TaskRepository {
	return &TaskRepository{
		tasks:  make(map[int]Task),
		nextID: 1,
	}
}

func (r *TaskRepository) Create(title string) Task {

	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()

	task := Task{
		ID:        r.nextID,
		Title:     title,
		Completed: false,
		CreatedAt: now,
		UpdatedAt: now,
	}

	r.tasks[task.ID] = task
	r.nextID++

	return task
}

func (r *TaskRepository) Get(id int) (Task, error) {

	r.mu.RLock()
	defer r.mu.RUnlock()

	task, exists := r.tasks[id]

	if !exists {
		return Task{}, ErrTaskNotFound
	}

	return task, nil
}

func (r *TaskRepository) GetAll() []Task {

	r.mu.RLock()
	defer r.mu.RUnlock()

	tasks := make([]Task, 0, len(r.tasks))

	for _, task := range r.tasks {
		tasks = append(tasks, task)
	}

	return tasks
}

func (r *TaskRepository) Update(
	id int,
	title string,
	completed bool,
) (Task, error) {

	r.mu.Lock()
	defer r.mu.Unlock()

	task, exists := r.tasks[id]

	if !exists {
		return Task{}, ErrTaskNotFound
	}

	task.Title = title
	task.Completed = completed
	task.UpdatedAt = time.Now()

	r.tasks[id] = task

	return task, nil
}

func (r *TaskRepository) Delete(id int) error {

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tasks[id]; !exists {
		return ErrTaskNotFound
	}

	delete(r.tasks, id)

	return nil
}

// ============================================================
// SERVICE
// ============================================================

type TaskService struct {
	repository *TaskRepository
}

func NewTaskService(repository *TaskRepository) *TaskService {
	return &TaskService{
		repository: repository,
	}
}

func (s *TaskService) CreateTask(title string) (Task, error) {

	title = strings.TrimSpace(title)

	if title == "" {
		return Task{}, ErrEmptyTitle
	}

	return s.repository.Create(title), nil
}

func (s *TaskService) GetTask(id int) (Task, error) {

	return s.repository.Get(id)
}

func (s *TaskService) GetTasks() []Task {

	return s.repository.GetAll()
}

func (s *TaskService) UpdateTask(
	id int,
	title string,
	completed bool,
) (Task, error) {

	title = strings.TrimSpace(title)

	if title == "" {
		return Task{}, ErrEmptyTitle
	}

	return s.repository.Update(
		id,
		title,
		completed,
	)
}

func (s *TaskService) DeleteTask(id int) error {

	return s.repository.Delete(id)
}

// ============================================================
// HANDLER
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
// RESPONSE HELPERS
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
		log.Printf("failed to encode response: %v", err)
	}
}

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

func writeTask(
	w http.ResponseWriter,
	status int,
	task Task,
) {
	writeJSON(
		w,
		status,
		toTaskResponse(task),
	)
}

// ============================================================
// DTO MAPPING
// ============================================================

func toTaskResponse(task Task) TaskResponse {

	return TaskResponse{
		ID:        task.ID,
		Title:     task.Title,
		Completed: task.Completed,
		CreatedAt: task.CreatedAt.Format(time.RFC3339),
		UpdatedAt: task.UpdatedAt.Format(time.RFC3339),
	}
}

// ============================================================
// ID PARSING
// ============================================================

func parseTaskID(path string) (int, error) {

	parts := strings.Split(
		strings.Trim(path, "/"),
		"/",
	)

	if len(parts) != 4 {
		return 0, ErrInvalidID
	}

	id, err := strconv.Atoi(parts[3])

	if err != nil || id <= 0 {
		return 0, ErrInvalidID
	}

	return id, nil
}

// ============================================================
// GET /api/v1/tasks
// POST /api/v1/tasks
// ============================================================

func (h *TaskHandler) Tasks(w http.ResponseWriter, r *http.Request) {

	switch r.Method {

	case http.MethodGet:

		h.listTasks(w)

	case http.MethodPost:

		h.createTask(w, r)

	default:

		writeError(
			w,
			http.StatusMethodNotAllowed,
			"METHOD_NOT_ALLOWED",
			"method not allowed",
		)
	}
}

// ============================================================
// GET /api/v1/tasks
// ============================================================

func (h *TaskHandler) listTasks(w http.ResponseWriter) {

	tasks := h.service.GetTasks()

	response := TaskListResponse{
		Tasks: make([]TaskResponse, 0, len(tasks)),
		Count: len(tasks),
	}

	for _, task := range tasks {
		response.Tasks = append(
			response.Tasks,
			toTaskResponse(task),
		)
	}

	writeJSON(
		w,
		http.StatusOK,
		response,
	)
}

// ============================================================
// POST /api/v1/tasks
// ============================================================

func (h *TaskHandler) createTask(
	w http.ResponseWriter,
	r *http.Request,
) {

	var request CreateTaskRequest

	decoder := json.NewDecoder(r.Body)

	if err := decoder.Decode(&request); err != nil {

		writeError(
			w,
			http.StatusBadRequest,
			"INVALID_JSON",
			"request body contains invalid JSON",
		)

		return
	}

	task, err := h.service.CreateTask(request.Title)

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

		writeError(
			w,
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"internal server error",
		)

		return
	}

	writeTask(
		w,
		http.StatusCreated,
		task,
	)
}

// ============================================================
// GET /api/v1/tasks/{id}
// PUT /api/v1/tasks/{id}
// DELETE /api/v1/tasks/{id}
// ============================================================

func (h *TaskHandler) TaskByID(
	w http.ResponseWriter,
	r *http.Request,
) {

	id, err := parseTaskID(r.URL.Path)

	if err != nil {

		writeError(
			w,
			http.StatusBadRequest,
			"INVALID_TASK_ID",
			"task id must be a positive integer",
		)

		return
	}

	switch r.Method {

	case http.MethodGet:

		h.getTask(w, id)

	case http.MethodPut:

		h.updateTask(w, r, id)

	case http.MethodDelete:

		h.deleteTask(w, id)

	default:

		writeError(
			w,
			http.StatusMethodNotAllowed,
			"METHOD_NOT_ALLOWED",
			"method not allowed",
		)
	}
}

// ============================================================
// GET /api/v1/tasks/{id}
// ============================================================

func (h *TaskHandler) getTask(
	w http.ResponseWriter,
	id int,
) {

	task, err := h.service.GetTask(id)

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

		writeError(
			w,
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"internal server error",
		)

		return
	}

	writeTask(
		w,
		http.StatusOK,
		task,
	)
}

// ============================================================
// PUT /api/v1/tasks/{id}
// ============================================================

func (h *TaskHandler) updateTask(
	w http.ResponseWriter,
	r *http.Request,
	id int,
) {

	var request UpdateTaskRequest

	decoder := json.NewDecoder(r.Body)

	if err := decoder.Decode(&request); err != nil {

		writeError(
			w,
			http.StatusBadRequest,
			"INVALID_JSON",
			"request body contains invalid JSON",
		)

		return
	}

	task, err := h.service.UpdateTask(
		id,
		request.Title,
		request.Completed,
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

		case errors.Is(err, ErrEmptyTitle):

			writeError(
				w,
				http.StatusBadRequest,
				"EMPTY_TITLE",
				"task title cannot be empty",
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

	writeTask(
		w,
		http.StatusOK,
		task,
	)
}

// ============================================================
// DELETE /api/v1/tasks/{id}
// ============================================================

func (h *TaskHandler) deleteTask(
	w http.ResponseWriter,
	id int,
) {

	err := h.service.DeleteTask(id)

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

		writeError(
			w,
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"internal server error",
		)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ============================================================
// API ROUTER
// ============================================================

func buildRouter(handler *TaskHandler) http.Handler {

	mux := http.NewServeMux()

	mux.HandleFunc(
		"/api/v1/tasks",
		handler.Tasks,
	)

	mux.HandleFunc(
		"/api/v1/tasks/",
		handler.TaskByID,
	)

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

	// --------------------------------------------------------
	// REPOSITORY
	// --------------------------------------------------------

	repository := NewTaskRepository()

	// --------------------------------------------------------
	// SERVICE
	// --------------------------------------------------------

	service := NewTaskService(repository)

	// --------------------------------------------------------
	// HANDLER
	// --------------------------------------------------------

	handler := NewTaskHandler(service)

	// --------------------------------------------------------
	// ROUTER
	// --------------------------------------------------------

	router := buildRouter(handler)

	// --------------------------------------------------------
	// SERVER
	// --------------------------------------------------------

	server := &http.Server{
		Addr:              ":8080",
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	fmt.Println("========================================")
	fmt.Println("Task REST API")
	fmt.Println("========================================")
	fmt.Println("Server: http://localhost:8080")
	fmt.Println()
	fmt.Println("Endpoints:")
	fmt.Println("GET    /health")
	fmt.Println("GET    /api/v1/tasks")
	fmt.Println("GET    /api/v1/tasks/{id}")
	fmt.Println("POST   /api/v1/tasks")
	fmt.Println("PUT    /api/v1/tasks/{id}")
	fmt.Println("DELETE /api/v1/tasks/{id}")
	fmt.Println("========================================")

	log.Fatal(
		server.ListenAndServe(),
	)
}
