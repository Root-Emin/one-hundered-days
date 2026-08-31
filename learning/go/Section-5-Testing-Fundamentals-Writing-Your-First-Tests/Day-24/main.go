package main

import (
	"encoding/json"
	"net/http"
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

type ErrorResponse struct {
	Error string `json:"error"`
}

// ============================================================
// HANDLER
// ============================================================

func createTaskHandler(w http.ResponseWriter, r *http.Request) {

	// --------------------------------------------------------
	// Method validation
	// --------------------------------------------------------

	if r.Method != http.MethodPost {

		writeJSON(
			w,
			http.StatusMethodNotAllowed,
			ErrorResponse{
				Error: "method not allowed",
			},
		)

		return
	}

	// --------------------------------------------------------
	// Decode request body
	// --------------------------------------------------------

	var input CreateTaskRequest

	err := json.NewDecoder(r.Body).Decode(&input)

	if err != nil {

		writeJSON(
			w,
			http.StatusBadRequest,
			ErrorResponse{
				Error: "invalid JSON",
			},
		)

		return
	}

	// --------------------------------------------------------
	// Validate title
	// --------------------------------------------------------

	if input.Title == "" {

		writeJSON(
			w,
			http.StatusBadRequest,
			ErrorResponse{
				Error: "title is required",
			},
		)

		return
	}

	// --------------------------------------------------------
	// Create task
	// --------------------------------------------------------

	task := Task{
		ID:    1,
		Title: input.Title,
	}

	// --------------------------------------------------------
	// Success response
	// --------------------------------------------------------

	writeJSON(
		w,
		http.StatusCreated,
		task,
	)
}

// ============================================================
// RESPONSE HELPER
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

	_ = json.NewEncoder(w).Encode(data)
}

// ============================================================
// SERVER
// ============================================================

func main() {

	http.HandleFunc(
		"/tasks",
		createTaskHandler,
	)

	http.ListenAndServe(
		":8080",
		nil,
	)
}
