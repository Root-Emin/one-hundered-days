package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
)

// ============================================================
// DOMAIN
// ============================================================

type CreateTaskRequest struct {
	Title    string `json:"title"`
	Priority int    `json:"priority"`
}

type Task struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	Priority int    `json:"priority"`
}

type ProblemDetails struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// ============================================================
// APPLICATION
// ============================================================

type App struct {
	nextTaskID int64
}

// ============================================================
// REQUEST ID
// ============================================================

func getRequestID(r *http.Request) string {

	requestID := r.Header.Get("X-Request-ID")

	if requestID == "" {
		requestID = "generated-request-id"
	}

	return requestID
}

// ============================================================
// JSON RESPONSE
// ============================================================
//
// Task:
// Write JSON Responses
//
// Bütün JSON response'ların Content-Type'ı burada
// application/json olarak ayarlanıyor.
// ============================================================

func writeJSON(
	w http.ResponseWriter,
	status int,
	data any,
) {

	w.Header().Set(
		"Content-Type",
		"application/json",
	)

	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf(
			"failed to encode JSON response: %v",
			err,
		)
	}
}

// ============================================================
// PROBLEM RESPONSE
// ============================================================
//
// Task:
// Return Problem Details
//
// API'deki bütün structured error response'ları
// aynı formatta dönüyor.
// ============================================================

func writeProblem(
	w http.ResponseWriter,
	status int,
	code string,
	message string,
	requestID string,
) {

	problem := ProblemDetails{
		Code:      code,
		Message:   message,
		RequestID: requestID,
	}

	writeJSON(
		w,
		status,
		problem,
	)
}

// ============================================================
// VALIDATION
// ============================================================

func validateCreateTaskRequest(
	request CreateTaskRequest,
) error {

	// Required field
	if strings.TrimSpace(request.Title) == "" {
		return errors.New(
			"title is required",
		)
	}

	// Priority constraint
	if request.Priority < 1 || request.Priority > 5 {
		return errors.New(
			"priority must be between 1 and 5",
		)
	}

	return nil
}

// ============================================================
// CREATE TASK HANDLER
// ============================================================
//
// Endpoint:
//
// POST /tasks
//
// Body:
//
// {
//     "title": "Learn Go",
//     "priority": 5
// }
//
// ============================================================

func (app *App) CreateTask(
	w http.ResponseWriter,
	r *http.Request,
) {

	// ========================================================
	// REQUEST ID
	// ========================================================
	//
	// Task:
	// Use Headers
	//
	// Client X-Request-ID gönderirse onu kullanıyoruz.
	// Göndermezse fallback oluşturuyoruz.
	// ========================================================

	requestID := getRequestID(r)

	// Response'a da request ID ekliyoruz.
	w.Header().Set(
		"X-Request-ID",
		requestID,
	)

	// ========================================================
	// METHOD CHECK
	// ========================================================

	if r.Method != http.MethodPost {

		writeProblem(
			w,
			http.StatusMethodNotAllowed,
			"METHOD_NOT_ALLOWED",
			"only POST is allowed",
			requestID,
		)

		return
	}

	// ========================================================
	// CONTENT TYPE CHECK
	// ========================================================

	contentType := r.Header.Get("Content-Type")

	if !strings.HasPrefix(
		contentType,
		"application/json",
	) {

		writeProblem(
			w,
			http.StatusUnsupportedMediaType,
			"UNSUPPORTED_MEDIA_TYPE",
			"Content-Type must be application/json",
			requestID,
		)

		return
	}

	// ========================================================
	// READ JSON BODY
	// ========================================================
	//
	// Task:
	// Read JSON Bodies
	//
	// json.NewDecoder HTTP request body'yi okuyup
	// Go struct'ına decode ediyor.
	// ========================================================

	var request CreateTaskRequest

	decoder := json.NewDecoder(r.Body)

	err := decoder.Decode(&request)

	if err != nil {

		writeProblem(
			w,
			http.StatusBadRequest,
			"INVALID_JSON",
			"request body contains invalid JSON",
			requestID,
		)

		return
	}

	// ========================================================
	// VALIDATION
	// ========================================================
	//
	// JSON parse edildi fakat alanlar geçerli mi?
	// ========================================================

	if err := validateCreateTaskRequest(request); err != nil {

		writeProblem(
			w,
			http.StatusBadRequest,
			"VALIDATION_ERROR",
			err.Error(),
			requestID,
		)

		return
	}

	// ========================================================
	// BUSINESS LOGIC
	// ========================================================

	taskID := atomic.AddInt64(
		&app.nextTaskID,
		1,
	)

	task := Task{
		ID:       int(taskID),
		Title:    strings.TrimSpace(request.Title),
		Priority: request.Priority,
	}

	// ========================================================
	// CACHE HINT
	// ========================================================
	//
	// Task:
	// Use Headers
	//
	// Bu response'un cache edilmemesi gerektiğini belirtiyoruz.
	// ========================================================

	w.Header().Set(
		"Cache-Control",
		"no-store",
	)

	// ========================================================
	// JSON RESPONSE
	// ========================================================
	//
	// Task:
	// Write JSON Responses
	// ========================================================

	writeJSON(
		w,
		http.StatusCreated,
		task,
	)
}

// ============================================================
// HEALTH HANDLER
// ============================================================

func (app *App) Health(
	w http.ResponseWriter,
	r *http.Request,
) {

	requestID := getRequestID(r)

	w.Header().Set(
		"X-Request-ID",
		requestID,
	)

	// Health endpoint için kısa süreli cache.
	w.Header().Set(
		"Cache-Control",
		"no-cache",
	)

	response := map[string]string{
		"status":     "ok",
		"request_id": requestID,
	}

	writeJSON(
		w,
		http.StatusOK,
		response,
	)
}

// ============================================================
// ROUTER
// ============================================================

func (app *App) routes() http.Handler {

	mux := http.NewServeMux()

	mux.HandleFunc(
		"/tasks",
		app.CreateTask,
	)

	mux.HandleFunc(
		"/health",
		app.Health,
	)

	return mux
}

// ============================================================
// SERVER
// ============================================================

func main() {

	app := &App{}

	handler := app.routes()

	server := &http.Server{
		Addr:    ":8080",
		Handler: handler,
	}

	fmt.Println("HTTP server running on http://localhost:8080")

	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
