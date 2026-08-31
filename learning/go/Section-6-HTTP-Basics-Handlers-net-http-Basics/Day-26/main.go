package main

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

// ============================================================
// RESPONSE HELPERS
// ============================================================

func writeText(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintln(w, message)
}

// ============================================================
// REQUEST INSPECTION
// ============================================================

func inspectRequest(r *http.Request) {
	log.Println("========== INCOMING REQUEST ==========")
	log.Println("Method:", r.Method)
	log.Println("Path:", r.URL.Path)
	log.Println("Remote Address:", r.RemoteAddr)

	log.Println("Headers:")

	for key, values := range r.Header {
		for _, value := range values {
			log.Printf("  %s: %s\n", key, value)
		}
	}

	log.Println("======================================")
}

// ============================================================
// GET /
// ============================================================

func homeHandler(w http.ResponseWriter, r *http.Request) {

	inspectRequest(r)

	if r.Method != http.MethodGet {
		writeText(
			w,
			http.StatusMethodNotAllowed,
			"method not allowed",
		)
		return
	}

	writeText(
		w,
		http.StatusOK,
		"Welcome to Day 26 HTTP API",
	)
}

// ============================================================
// GET /health
// ============================================================

func healthHandler(w http.ResponseWriter, r *http.Request) {

	inspectRequest(r)

	if r.Method != http.MethodGet {
		writeText(
			w,
			http.StatusMethodNotAllowed,
			"method not allowed",
		)
		return
	}

	writeText(
		w,
		http.StatusOK,
		"OK",
	)
}

// ============================================================
// GET /api/tasks
// ============================================================

func tasksHandler(w http.ResponseWriter, r *http.Request) {

	inspectRequest(r)

	if r.Method != http.MethodGet {
		writeText(
			w,
			http.StatusMethodNotAllowed,
			"method not allowed",
		)
		return
	}

	writeText(
		w,
		http.StatusOK,
		"Task 1: Prepare report\nTask 2: Send invoice\nTask 3: Backup database",
	)
}

// ============================================================
// POST /api/tasks
// ============================================================

func createTaskHandler(w http.ResponseWriter, r *http.Request) {

	inspectRequest(r)

	if r.Method != http.MethodPost {
		writeText(
			w,
			http.StatusMethodNotAllowed,
			"method not allowed",
		)
		return
	}

	writeText(
		w,
		http.StatusCreated,
		"task created",
	)
}

// ============================================================
// 404 HANDLER
// ============================================================

func notFoundHandler(w http.ResponseWriter, r *http.Request) {

	inspectRequest(r)

	writeText(
		w,
		http.StatusNotFound,
		"endpoint not found",
	)
}

// ============================================================
// SERVER
// ============================================================

func main() {

	// --------------------------------------------------------
	// SERVE MUX
	// --------------------------------------------------------

	mux := http.NewServeMux()

	mux.HandleFunc("/", homeHandler)
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/api/tasks", tasksHandler)
	mux.HandleFunc("/api/tasks/create", createTaskHandler)

	// --------------------------------------------------------
	// HTTP SERVER
	// --------------------------------------------------------

	server := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// --------------------------------------------------------
	// START SERVER
	// --------------------------------------------------------

	log.Println("HTTP server starting on http://localhost:8080")

	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
