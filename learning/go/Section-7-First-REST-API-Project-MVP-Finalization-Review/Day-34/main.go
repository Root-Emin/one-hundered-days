package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// ============================================================
// DOMAIN
// ============================================================

type Task struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

type CreateTaskRequest struct {
	Title string `json:"title"`
}

// ============================================================
// APPLICATION ERRORS
// ============================================================

var (
	ErrValidation = errors.New("validation error")
	ErrNotFound   = errors.New("task not found")
	ErrInternal   = errors.New("internal error")
)

// ============================================================
// PROBLEM RESPONSE
// ============================================================

type ProblemResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// ============================================================
// CENTRALIZED ERROR WRITING
// ============================================================

func writeProblem(
	w http.ResponseWriter,
	status int,
	code string,
	message string,
	details string,
) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	response := ProblemResponse{
		Code:    code,
		Message: message,
		Details: details,
	}

	_ = json.NewEncoder(w).Encode(response)
}

// ============================================================
// ERROR -> HTTP STATUS MAPPING
// ============================================================

func mapErrorToStatus(err error) int {
	switch {
	case errors.Is(err, ErrValidation):
		return http.StatusBadRequest

	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound

	case errors.Is(err, ErrInternal):
		return http.StatusInternalServerError

	default:
		return http.StatusInternalServerError
	}
}

// ============================================================
// ERROR HANDLER
// ============================================================

func writeError(
	w http.ResponseWriter,
	err error,
	details string,
) {
	switch {
	case errors.Is(err, ErrValidation):
		writeProblem(
			w,
			http.StatusBadRequest,
			"VALIDATION_ERROR",
			"request validation failed",
			details,
		)

	case errors.Is(err, ErrNotFound):
		writeProblem(
			w,
			http.StatusNotFound,
			"NOT_FOUND",
			"requested resource was not found",
			details,
		)

	case errors.Is(err, ErrInternal):
		writeProblem(
			w,
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"internal server error",
			"",
		)

	default:
		writeProblem(
			w,
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"internal server error",
			"",
		)
	}
}

// ============================================================
// VALIDATION
// ============================================================

const maxTitleLength = 100

func validateTitle(title string) error {
	title = strings.TrimSpace(title)

	if title == "" {
		return fmt.Errorf(
			"%w: title is required",
			ErrValidation,
		)
	}

	if len([]rune(title)) > maxTitleLength {
		return fmt.Errorf(
			"%w: title must be at most %d characters",
			ErrValidation,
			maxTitleLength,
		)
	}

	return nil
}

func validateID(rawID string) (int, error) {
	rawID = strings.TrimSpace(rawID)

	if rawID == "" {
		return 0, fmt.Errorf(
			"%w: id is required",
			ErrValidation,
		)
	}

	id, err := strconv.Atoi(rawID)
	if err != nil {
		return 0, fmt.Errorf(
			"%w: id must be a valid integer",
			ErrValidation,
		)
	}

	if id <= 0 {
		return 0, fmt.Errorf(
			"%w: id must be greater than zero",
			ErrValidation,
		)
	}

	return id, nil
}

// ============================================================
// STORE
// ============================================================

type TaskStore struct {
	tasks map[int]Task
}

func NewTaskStore() *TaskStore {
	return &TaskStore{
		tasks: map[int]Task{
			1: {
				ID:    1,
				Title: "Learn Go",
			},
			2: {
				ID:    2,
				Title: "Build REST API",
			},
		},
	}
}

func (s *TaskStore) Get(id int) (Task, error) {
	task, exists := s.tasks[id]

	if !exists {
		return Task{}, fmt.Errorf(
			"%w: task %d does not exist",
			ErrNotFound,
			id,
		)
	}

	return task, nil
}

func (s *TaskStore) Create(title string) (Task, error) {
	if err := validateTitle(title); err != nil {
		return Task{}, err
	}

	nextID := len(s.tasks) + 1

	task := Task{
		ID:    nextID,
		Title: strings.TrimSpace(title),
	}

	s.tasks[nextID] = task

	return task, nil
}

// ============================================================
// HANDLER
// ============================================================

type TaskHandler struct {
	store *TaskStore
}

func NewTaskHandler(store *TaskStore) *TaskHandler {
	return &TaskHandler{
		store: store,
	}
}

// ============================================================
// POST /tasks
// ============================================================

func (h *TaskHandler) CreateTask(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodPost {
		writeError(
			w,
			fmt.Errorf(
				"%w: method must be POST",
				ErrValidation,
			),
			"unsupported HTTP method",
		)

		return
	}

	var request CreateTaskRequest

	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		writeError(
			w,
			fmt.Errorf(
				"%w: invalid JSON",
				ErrValidation,
			),
			"request body must contain valid JSON",
		)

		return
	}

	// --------------------------------------------------------
	// Validate title
	// --------------------------------------------------------

	if err := validateTitle(request.Title); err != nil {
		writeError(
			w,
			err,
			"field: title",
		)

		return
	}

	// --------------------------------------------------------
	// Create task
	// --------------------------------------------------------

	task, err := h.store.Create(request.Title)
	if err != nil {
		writeError(
			w,
			err,
			"could not create task",
		)

		return
	}

	// --------------------------------------------------------
	// Success
	// --------------------------------------------------------

	w.Header().Set(
		"Content-Type",
		"application/json",
	)

	w.WriteHeader(http.StatusCreated)

	_ = json.NewEncoder(w).Encode(task)
}

// ============================================================
// GET /tasks/{id}
// ============================================================

func (h *TaskHandler) GetTask(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		writeError(
			w,
			fmt.Errorf(
				"%w: method must be GET",
				ErrValidation,
			),
			"unsupported HTTP method",
		)

		return
	}

	rawID := strings.TrimPrefix(
		r.URL.Path,
		"/tasks/",
	)

	id, err := validateID(rawID)
	if err != nil {
		writeError(
			w,
			err,
			"field: id",
		)

		return
	}

	task, err := h.store.Get(id)
	if err != nil {
		writeError(
			w,
			err,
			fmt.Sprintf("task id: %d", id),
		)

		return
	}

	w.Header().Set(
		"Content-Type",
		"application/json",
	)

	w.WriteHeader(http.StatusOK)

	_ = json.NewEncoder(w).Encode(task)
}

// ============================================================
// MAIN
// ============================================================

func main() {
	store := NewTaskStore()
	handler := NewTaskHandler(store)

	mux := http.NewServeMux()

	mux.HandleFunc(
		"/tasks",
		handler.CreateTask,
	)

	mux.HandleFunc(
		"/tasks/",
		handler.GetTask,
	)

	fmt.Println("Server running on http://localhost:8080")

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	err := server.ListenAndServe()

	if err != nil && err != http.ErrServerClosed {
		panic(err)
	}
}
