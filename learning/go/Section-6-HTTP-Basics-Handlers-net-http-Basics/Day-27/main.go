package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
)

// ============================================================
// MODEL
// ============================================================

type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// ============================================================
// IN-MEMORY DATA
// ============================================================

var users = map[int]User{
	1: {
		ID:   1,
		Name: "Emin",
	},
	2: {
		ID:   2,
		Name: "Ahmet",
	},
	3: {
		ID:   3,
		Name: "Mehmet",
	},
}

// ============================================================
// HEALTH HANDLER
//
// GET /health
//
// Task:
// - Use ServeMux
// - Organize handler by resource
// ============================================================

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	response := map[string]string{
		"status": "ok",
	}

	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("failed to encode health response: %v", err)
	}
}

// ============================================================
// USER LIST HANDLER
//
// GET /users
//
// Task:
// - Use ServeMux
// - Organize handlers by resource
// ============================================================

func usersHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	userList := make([]User, 0, len(users))

	for _, user := range users {
		userList = append(userList, user)
	}

	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(userList); err != nil {
		log.Printf("failed to encode users response: %v", err)
	}
}

// ============================================================
// USER DETAIL HANDLER
//
// GET /users/{id}
//
// Task:
// - Parse path parameter
// - Return 404 when user doesn't exist
// ============================================================

func userHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// --------------------------------------------------------
	// PATH PARAMETER
	// --------------------------------------------------------

	idString := r.PathValue("id")

	id, err := strconv.Atoi(idString)

	if err != nil {
		http.Error(
			w,
			`{"error":"invalid user id"}`,
			http.StatusBadRequest,
		)

		return
	}

	// --------------------------------------------------------
	// FIND USER
	// --------------------------------------------------------

	user, exists := users[id]

	if !exists {
		http.Error(
			w,
			`{"error":"user not found"}`,
			http.StatusNotFound,
		)

		return
	}

	// --------------------------------------------------------
	// RESPONSE
	// --------------------------------------------------------

	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(user); err != nil {
		log.Printf("failed to encode user response: %v", err)
	}
}

// ============================================================
// METHOD CHECK EXAMPLE
//
// This handler demonstrates explicit method validation.
//
// POST /users
// DELETE /users
// etc.
//
// Only GET is supported here.
//
// Task:
// - Return 405 Method Not Allowed when method doesn't match.
// ============================================================

func methodCheckHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)

		http.Error(
			w,
			"method not allowed",
			http.StatusMethodNotAllowed,
		)

		return
	}

	w.WriteHeader(http.StatusOK)

	if _, err := w.Write([]byte("GET method is allowed")); err != nil {
		log.Printf("failed to write response: %v", err)
	}
}

// ============================================================
// USER ROUTER
//
// This demonstrates manually organized routing for users.
//
// The actual /users/{id} route is registered through ServeMux,
// while this helper demonstrates how a resource can have
// focused handlers instead of one giant handler.
//
// Task:
// - Organize handlers by resource.
// ============================================================

func userResourceHandler(w http.ResponseWriter, r *http.Request) {
	// We deliberately keep this function small.
	// Resource-specific behavior is delegated to focused handlers.

	switch {
	case r.Method == http.MethodGet:
		usersHandler(w, r)

	default:
		w.Header().Set("Allow", http.MethodGet)

		http.Error(
			w,
			"method not allowed",
			http.StatusMethodNotAllowed,
		)
	}
}

// ============================================================
// REQUEST LOGGING MIDDLEWARE
// ============================================================

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf(
			"%s %s",
			r.Method,
			r.URL.Path,
		)

		next.ServeHTTP(w, r)
	})
}

// ============================================================
// NOT FOUND HANDLER
// ============================================================

func notFoundHandler(w http.ResponseWriter, r *http.Request) {
	http.Error(
		w,
		"route not found",
		http.StatusNotFound,
	)
}

// ============================================================
// ROUTER
//
// Day 27's main focus.
//
// ServeMux maps:
//
// GET /health
// GET /users
// GET /users/{id}
//
// to focused handlers.
//
// ============================================================

func newRouter() http.Handler {
	mux := http.NewServeMux()

	// --------------------------------------------------------
	// HEALTH
	// --------------------------------------------------------

	mux.HandleFunc(
		"GET /health",
		healthHandler,
	)

	// --------------------------------------------------------
	// USERS
	// --------------------------------------------------------

	mux.HandleFunc(
		"GET /users",
		usersHandler,
	)

	// --------------------------------------------------------
	// USER BY ID
	// --------------------------------------------------------

	mux.HandleFunc(
		"GET /users/{id}",
		userHandler,
	)

	// --------------------------------------------------------
	// EXPLICIT METHOD CHECK EXAMPLE
	// --------------------------------------------------------

	mux.HandleFunc(
		"/method-check",
		methodCheckHandler,
	)

	// --------------------------------------------------------
	// FALLBACK
	// --------------------------------------------------------

	mux.HandleFunc(
		"/",
		func(w http.ResponseWriter, r *http.Request) {
			// ServeMux already handles many routing cases,
			// but this demonstrates a final fallback handler.

			if r.URL.Path == "/" {
				w.WriteHeader(http.StatusOK)

				_, err := w.Write(
					[]byte("Day 27 HTTP API"),
				)

				if err != nil {
					log.Printf(
						"failed to write root response: %v",
						err,
					)
				}

				return
			}

			notFoundHandler(w, r)
		},
	)

	return loggingMiddleware(mux)
}

// ============================================================
// SMALL CLIENT HELPER
//
// This exists only so the example demonstrates path handling
// and makes it clear how a client would construct the URL.
// ============================================================

func userURL(id int) string {
	return fmt.Sprintf(
		"/users/%d",
		id,
	)
}

// ============================================================
// PATH VALIDATION HELPER
//
// Small helper for understanding path parameters.
//
// Example:
//
// /users/42
//
// becomes:
//
// ["users", "42"]
// ============================================================

func parseUserIDFromPath(path string) (int, error) {
	parts := strings.Split(
		strings.Trim(path, "/"),
		"/",
	)

	if len(parts) != 2 || parts[0] != "users" {
		return 0, fmt.Errorf(
			"invalid user path: %s",
			path,
		)
	}

	id, err := strconv.Atoi(parts[1])

	if err != nil {
		return 0, fmt.Errorf(
			"invalid user id %q: %w",
			parts[1],
			err,
		)
	}

	return id, nil
}

// ============================================================
// MAIN
// ============================================================

func main() {
	router := newRouter()

	server := &http.Server{
		Addr:    ":8080",
		Handler: router,
		// In production you would normally configure
		// timeouts as well.
		// This simple server is intentionally focused on
		// Day 27 routing fundamentals.
		//
		// ReadHeaderTimeout
		// WriteTimeout
		// IdleTimeout
		// etc.
		//
		// Those will become important in production HTTP work.
		//
		// For Day 27, the important part is:
		//
		// Handler: router
		//
	}

	log.Println("HTTP server listening on http://localhost:8080")

	if err := server.ListenAndServe(); err != nil &&
		err != http.ErrServerClosed {
		log.Fatalf(
			"server failed: %v",
			err,
		)
	}
}

// ============================================================
// EXAMPLE REQUESTS
//
// GET /
// GET /health
// GET /users
// GET /users/1
// GET /users/2
// GET /users/999
// GET /users/abc
//
// Method check:
//
// POST /method-check
//      -> 405 Method Not Allowed
//
// GET /method-check
//      -> 200 OK
//
// ============================================================

// ============================================================
// TASK CHECKLIST
//
// 1. Use ServeMux
//    ✓ http.NewServeMux()
//    ✓ GET /health
//    ✓ GET /users
//    ✓ GET /users/{id}
//
// 2. Path Parameters
//    ✓ r.PathValue("id")
//    ✓ strconv.Atoi()
//
// 3. Method Checks
//    ✓ r.Method
//    ✓ http.StatusMethodNotAllowed
//    ✓ Allow header
//
// 4. Organize Handlers
//    ✓ healthHandler
//    ✓ usersHandler
//    ✓ userHandler
//    ✓ methodCheckHandler
//    ✓ resource-oriented organization
//
// ============================================================
