package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ============================================================
// DAY 30 - HTTP BASICS & HANDLERS
//
// Tasks:
//
// 1. Build a Notes API
// 2. Add logging + recovery middleware
// 3. Write handler tests with httptest
// 4. Document endpoints + curl examples
//
// Everything is intentionally kept inside main.go.
// ============================================================

// ============================================================
// MODEL
// ============================================================

type Note struct {
	ID        int       `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// ============================================================
// IN-MEMORY STORE
// ============================================================
//
// map = temporary database
//
// mutex = protects shared state when multiple goroutines
// access the map concurrently.
//

type NoteStore struct {
	mu     sync.Mutex
	notes  map[int]Note
	nextID int
}

func NewNoteStore() *NoteStore {
	return &NoteStore{
		notes:  make(map[int]Note),
		nextID: 1,
	}
}

// List returns all notes.
func (s *NoteStore) List() []Note {
	s.mu.Lock()
	defer s.mu.Unlock()

	notes := make([]Note, 0, len(s.notes))

	for _, note := range s.notes {
		notes = append(notes, note)
	}

	return notes
}

// Create creates a new note.
func (s *NoteStore) Create(title, content string) Note {
	s.mu.Lock()
	defer s.mu.Unlock()

	note := Note{
		ID:        s.nextID,
		Title:     title,
		Content:   content,
		CreatedAt: time.Now(),
	}

	s.notes[note.ID] = note
	s.nextID++

	return note
}

// Delete removes a note.
func (s *NoteStore) Delete(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.notes[id]; !exists {
		return false
	}

	delete(s.notes, id)

	return true
}

// ============================================================
// SERVER
// ============================================================

type Server struct {
	store *NoteStore
}

func NewServer(store *NoteStore) *Server {
	return &Server{
		store: store,
	}
}

// ============================================================
// JSON RESPONSE HELPERS
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
			"JSON encode error: %v",
			err,
		)
	}
}

func writeError(
	w http.ResponseWriter,
	status int,
	message string,
) {
	writeJSON(
		w,
		status,
		map[string]string{
			"error": message,
		},
	)
}

// ============================================================
// GET /notes
// ============================================================
//
// Returns every note in memory.
//
// Example:
//
// curl http://localhost:8080/notes
//

func (s *Server) listNotes(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		writeError(
			w,
			http.StatusMethodNotAllowed,
			"method not allowed",
		)

		return
	}

	notes := s.store.List()

	writeJSON(
		w,
		http.StatusOK,
		notes,
	)
}

// ============================================================
// POST /notes
// ============================================================
//
// Creates a note.
//
// Example:
//
// curl -X POST http://localhost:8080/notes \
//   -H "Content-Type: application/json" \
//   -d '{"title":"Learn Go","content":"Study HTTP"}'
//

func (s *Server) createNote(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodPost {
		writeError(
			w,
			http.StatusMethodNotAllowed,
			"method not allowed",
		)

		return
	}

	var request struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}

	err := json.NewDecoder(
		r.Body,
	).Decode(&request)

	if err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"invalid JSON",
		)

		return
	}

	request.Title = strings.TrimSpace(
		request.Title,
	)

	if request.Title == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"title is required",
		)

		return
	}

	note := s.store.Create(
		request.Title,
		request.Content,
	)

	writeJSON(
		w,
		http.StatusCreated,
		note,
	)
}

// ============================================================
// DELETE /notes/{id}
// ============================================================
//
// Deletes a note.
//
// Example:
//
// curl -X DELETE http://localhost:8080/notes/1
//

func (s *Server) deleteNote(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodDelete {
		writeError(
			w,
			http.StatusMethodNotAllowed,
			"method not allowed",
		)

		return
	}

	idString := strings.TrimPrefix(
		r.URL.Path,
		"/notes/",
	)

	// /notes/ -> missing ID
	if idString == "" || idString == r.URL.Path {
		writeError(
			w,
			http.StatusBadRequest,
			"note ID is required",
		)

		return
	}

	id, err := strconv.Atoi(idString)

	if err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"invalid note ID",
		)

		return
	}

	deleted := s.store.Delete(id)

	if !deleted {
		writeError(
			w,
			http.StatusNotFound,
			"note not found",
		)

		return
	}

	writeJSON(
		w,
		http.StatusOK,
		map[string]string{
			"message": "note deleted",
		},
	)
}

// ============================================================
// ROUTER
// ============================================================

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// --------------------------------------------------------
	// /notes
	// GET  -> list
	// POST -> create
	// --------------------------------------------------------

	mux.HandleFunc(
		"/notes",
		func(w http.ResponseWriter, r *http.Request) {

			switch r.Method {

			case http.MethodGet:
				s.listNotes(w, r)

			case http.MethodPost:
				s.createNote(w, r)

			default:
				writeError(
					w,
					http.StatusMethodNotAllowed,
					"method not allowed",
				)
			}
		},
	)

	// --------------------------------------------------------
	// /notes/{id}
	// DELETE -> delete
	// --------------------------------------------------------

	mux.HandleFunc(
		"/notes/",
		s.deleteNote,
	)

	// --------------------------------------------------------
	// Middleware chain
	//
	// Recovery
	//    ↓
	// Logging
	//    ↓
	// Router
	// --------------------------------------------------------

	return recoveryMiddleware(
		loggingMiddleware(
			mux,
		),
	)
}

// ============================================================
// LOGGING MIDDLEWARE
// ============================================================
//
// Middleware receives a handler and returns another handler.
//
// Every request passes through here.
//

func loggingMiddleware(
	next http.Handler,
) http.Handler {

	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {

			start := time.Now()

			next.ServeHTTP(
				w,
				r,
			)

			duration := time.Since(start)

			log.Printf(
				"%s %s - %v",
				r.Method,
				r.URL.Path,
				duration,
			)
		},
	)
}

// ============================================================
// RECOVERY MIDDLEWARE
// ============================================================
//
// Prevents a panic from crashing the entire HTTP server.
//

func recoveryMiddleware(
	next http.Handler,
) http.Handler {

	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {

			defer func() {

				if recovered := recover(); recovered != nil {

					log.Printf(
						"PANIC RECOVERED: %v",
						recovered,
					)

					writeError(
						w,
						http.StatusInternalServerError,
						"internal server error",
					)
				}
			}()

			next.ServeHTTP(
				w,
				r,
			)
		},
	)
}

// ============================================================
// TEST HELPERS
// ============================================================

func newTestHandler() http.Handler {
	store := NewNoteStore()

	server := NewServer(store)

	return server.routes()
}

func assertStatus(
	name string,
	got int,
	want int,
) {
	if got != want {
		panic(
			fmt.Sprintf(
				"%s: got status %d, want %d",
				name,
				got,
				want,
			),
		)
	}
}

// ============================================================
// TEST 1
//
// GET /notes
// Empty list should return 200.
//
// Uses httptest.
// ============================================================

func testListNotes() {

	handler := newTestHandler()

	request := httptest.NewRequest(
		http.MethodGet,
		"/notes",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		request,
	)

	assertStatus(
		"GET /notes",
		recorder.Code,
		http.StatusOK,
	)

	var notes []Note

	err := json.Unmarshal(
		recorder.Body.Bytes(),
		&notes,
	)

	if err != nil {
		panic(
			fmt.Sprintf(
				"GET /notes returned invalid JSON: %v",
				err,
			),
		)
	}

	if len(notes) != 0 {
		panic(
			fmt.Sprintf(
				"expected 0 notes, got %d",
				len(notes),
			),
		)
	}
}

// ============================================================
// TEST 2
//
// POST /notes
// Valid JSON should create a note.
//
// Uses httptest.
// ============================================================

func testCreateNote() {

	handler := newTestHandler()

	body := strings.NewReader(
		`{
			"title": "Learn Go",
			"content": "Study HTTP handlers"
		}`,
	)

	request := httptest.NewRequest(
		http.MethodPost,
		"/notes",
		body,
	)

	request.Header.Set(
		"Content-Type",
		"application/json",
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		request,
	)

	assertStatus(
		"POST /notes",
		recorder.Code,
		http.StatusCreated,
	)

	var note Note

	err := json.Unmarshal(
		recorder.Body.Bytes(),
		&note,
	)

	if err != nil {
		panic(
			fmt.Sprintf(
				"POST /notes returned invalid JSON: %v",
				err,
			),
		)
	}

	if note.ID != 1 {
		panic(
			fmt.Sprintf(
				"expected ID 1, got %d",
				note.ID,
			),
		)
	}

	if note.Title != "Learn Go" {
		panic(
			fmt.Sprintf(
				"expected title Learn Go, got %s",
				note.Title,
			),
		)
	}
}

// ============================================================
// TEST 3
//
// Invalid JSON should return 400.
//

func testInvalidJSON() {

	handler := newTestHandler()

	body := strings.NewReader(
		`{"title":`,
	)

	request := httptest.NewRequest(
		http.MethodPost,
		"/notes",
		body,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		request,
	)

	assertStatus(
		"invalid JSON",
		recorder.Code,
		http.StatusBadRequest,
	)
}

// ============================================================
// TEST 4
//
// Missing title should return 400.
//

func testMissingTitle() {

	handler := newTestHandler()

	body := strings.NewReader(
		`{
			"content": "No title"
		}`,
	)

	request := httptest.NewRequest(
		http.MethodPost,
		"/notes",
		body,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		request,
	)

	assertStatus(
		"missing title",
		recorder.Code,
		http.StatusBadRequest,
	)
}

// ============================================================
// TEST 5
//
// Wrong HTTP method should return 405.
//

func testWrongMethod() {

	handler := newTestHandler()

	request := httptest.NewRequest(
		http.MethodPut,
		"/notes",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		request,
	)

	assertStatus(
		"wrong method",
		recorder.Code,
		http.StatusMethodNotAllowed,
	)
}

// ============================================================
// TEST 6
//
// DELETE existing note should return 200.
//

func testDeleteNote() {

	store := NewNoteStore()

	server := NewServer(store)

	handler := server.routes()

	store.Create(
		"Delete Me",
		"Temporary note",
	)

	request := httptest.NewRequest(
		http.MethodDelete,
		"/notes/1",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		request,
	)

	assertStatus(
		"DELETE /notes/1",
		recorder.Code,
		http.StatusOK,
	)

	// Verify the note is really gone.
	notes := store.List()

	if len(notes) != 0 {
		panic(
			fmt.Sprintf(
				"expected 0 notes after delete, got %d",
				len(notes),
			),
		)
	}
}

// ============================================================
// TEST 7
//
// DELETE missing note should return 404.
//

func testDeleteMissingNote() {

	handler := newTestHandler()

	request := httptest.NewRequest(
		http.MethodDelete,
		"/notes/999",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		request,
	)

	assertStatus(
		"DELETE missing note",
		recorder.Code,
		http.StatusNotFound,
	)
}

// ============================================================
// TEST 8
//
// Invalid ID should return 400.
//

func testInvalidID() {

	handler := newTestHandler()

	request := httptest.NewRequest(
		http.MethodDelete,
		"/notes/abc",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		request,
	)

	assertStatus(
		"invalid ID",
		recorder.Code,
		http.StatusBadRequest,
	)
}

// ============================================================
// TEST 9
//
// Missing ID should return 400.
//

func testMissingID() {

	handler := newTestHandler()

	request := httptest.NewRequest(
		http.MethodDelete,
		"/notes/",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		request,
	)

	assertStatus(
		"missing ID",
		recorder.Code,
		http.StatusBadRequest,
	)
}

// ============================================================
// TEST 10
//
// Full API flow:
//
// CREATE
//   ↓
// LIST
//   ↓
// DELETE
//   ↓
// LIST again
//
// This is our integration-style smoke test.
//

func testFullFlow() {

	store := NewNoteStore()

	server := NewServer(store)

	handler := server.routes()

	// --------------------------------------------------------
	// CREATE
	// --------------------------------------------------------

	createBody := strings.NewReader(
		`{
			"title": "Day 30",
			"content": "HTTP API practice"
		}`,
	)

	createRequest := httptest.NewRequest(
		http.MethodPost,
		"/notes",
		createBody,
	)

	createRecorder := httptest.NewRecorder()

	handler.ServeHTTP(
		createRecorder,
		createRequest,
	)

	assertStatus(
		"full flow create",
		createRecorder.Code,
		http.StatusCreated,
	)

	// --------------------------------------------------------
	// LIST
	// --------------------------------------------------------

	listRequest := httptest.NewRequest(
		http.MethodGet,
		"/notes",
		nil,
	)

	listRecorder := httptest.NewRecorder()

	handler.ServeHTTP(
		listRecorder,
		listRequest,
	)

	assertStatus(
		"full flow list",
		listRecorder.Code,
		http.StatusOK,
	)

	var notes []Note

	err := json.Unmarshal(
		listRecorder.Body.Bytes(),
		&notes,
	)

	if err != nil {
		panic(
			fmt.Sprintf(
				"full flow list JSON error: %v",
				err,
			),
		)
	}

	if len(notes) != 1 {
		panic(
			fmt.Sprintf(
				"expected 1 note, got %d",
				len(notes),
			),
		)
	}

	// --------------------------------------------------------
	// DELETE
	// --------------------------------------------------------

	deleteRequest := httptest.NewRequest(
		http.MethodDelete,
		"/notes/1",
		nil,
	)

	deleteRecorder := httptest.NewRecorder()

	handler.ServeHTTP(
		deleteRecorder,
		deleteRequest,
	)

	assertStatus(
		"full flow delete",
		deleteRecorder.Code,
		http.StatusOK,
	)

	// --------------------------------------------------------
	// LIST AGAIN
	// --------------------------------------------------------

	listAgainRequest := httptest.NewRequest(
		http.MethodGet,
		"/notes",
		nil,
	)

	listAgainRecorder := httptest.NewRecorder()

	handler.ServeHTTP(
		listAgainRecorder,
		listAgainRequest,
	)

	assertStatus(
		"full flow final list",
		listAgainRecorder.Code,
		http.StatusOK,
	)

	var remaining []Note

	err = json.Unmarshal(
		listAgainRecorder.Body.Bytes(),
		&remaining,
	)

	if err != nil {
		panic(
			fmt.Sprintf(
				"final list JSON error: %v",
				err,
			),
		)
	}

	if len(remaining) != 0 {
		panic(
			fmt.Sprintf(
				"expected 0 notes after delete, got %d",
				len(remaining),
			),
		)
	}
}

// ============================================================
// TEST RUNNER
// ============================================================
//
// Normally Go tests live in *_test.go.
//
// Because you requested ONLY main.go, we execute the tests
// manually through this function.
//

func runTests() {

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("DAY 30 - RUNNING HTTP TESTS")
	fmt.Println("========================================")

	tests := []struct {
		name string
		test func()
	}{
		{
			name: "GET /notes",
			test: testListNotes,
		},
		{
			name: "POST /notes",
			test: testCreateNote,
		},
		{
			name: "Invalid JSON",
			test: testInvalidJSON,
		},
		{
			name: "Missing Title",
			test: testMissingTitle,
		},
		{
			name: "Wrong Method",
			test: testWrongMethod,
		},
		{
			name: "DELETE /notes/{id}",
			test: testDeleteNote,
		},
		{
			name: "Missing Note",
			test: testDeleteMissingNote,
		},
		{
			name: "Invalid ID",
			test: testInvalidID,
		},
		{
			name: "Missing ID",
			test: testMissingID,
		},
		{
			name: "Full CRUD Flow",
			test: testFullFlow,
		},
	}

	passed := 0

	for _, test := range tests {

		func() {

			defer func() {

				if recovered := recover(); recovered != nil {

					fmt.Printf(
						"FAIL  %s\n",
						test.name,
					)

					fmt.Printf(
						"      %v\n",
						recovered,
					)

					return
				}

				fmt.Printf(
					"PASS  %s\n",
					test.name,
				)

				passed++
			}()

			test.test()
		}()
	}

	fmt.Println("----------------------------------------")

	if passed == len(tests) {
		fmt.Printf(
			"ALL TESTS PASSED: %d/%d\n",
			passed,
			len(tests),
		)
	} else {
		fmt.Printf(
			"TEST RESULT: %d/%d passed\n",
			passed,
			len(tests),
		)
	}

	fmt.Println("========================================")
	fmt.Println()
}

// ============================================================
// API DOCUMENTATION
// ============================================================
//
// This section acts as our Day 30 README documentation.
//
// ------------------------------------------------------------
//
// SERVER
//
// go run main.go
//
// Server:
// http://localhost:8080
//
// ------------------------------------------------------------
//
// GET /notes
//
// Lists all notes.
//
// curl:
//
// curl http://localhost:8080/notes
//
// ------------------------------------------------------------
//
// POST /notes
//
// Creates a new note.
//
// curl:
//
// curl -X POST http://localhost:8080/notes \
//   -H "Content-Type: application/json" \
//   -d '{"title":"Learn Go","content":"Study HTTP"}'
//
// ------------------------------------------------------------
//
// DELETE /notes/{id}
//
// Deletes a note.
//
// curl:
//
// curl -X DELETE http://localhost:8080/notes/1
//
// ------------------------------------------------------------
//
// ERROR EXAMPLES
//
// Invalid JSON:
//
// curl -X POST http://localhost:8080/notes \
//   -H "Content-Type: application/json" \
//   -d '{"title":'
//
// Missing title:
//
// curl -X POST http://localhost:8080/notes \
//   -H "Content-Type: application/json" \
//   -d '{"content":"hello"}'
//
// Missing note:
//
// curl -X DELETE http://localhost:8080/notes/999
//
// Invalid ID:
//
// curl -X DELETE http://localhost:8080/notes/abc
//
// Wrong method:
//
// curl -X PUT http://localhost:8080/notes
//
// ------------------------------------------------------------
//
// HTTP STATUS CODES
//
// 200 OK
//     GET /notes
//     DELETE /notes/{id}
//
// 201 Created
//     POST /notes
//
// 400 Bad Request
//     Invalid JSON
//     Missing title
//     Invalid ID
//     Missing ID
//
// 404 Not Found
//     Note does not exist
//
// 405 Method Not Allowed
//     Wrong HTTP method
//
// 500 Internal Server Error
//     Recovered panic
//
// ============================================================
// MAIN
// ============================================================

func main() {

	// --------------------------------------------------------
	// First run our httptest-based checks.
	// --------------------------------------------------------

	runTests()

	// --------------------------------------------------------
	// Create in-memory store.
	// --------------------------------------------------------

	store := NewNoteStore()

	// --------------------------------------------------------
	// Create server.
	// --------------------------------------------------------

	server := NewServer(store)

	// --------------------------------------------------------
	// Build routes + middleware.
	// --------------------------------------------------------

	handler := server.routes()

	// --------------------------------------------------------
	// Start HTTP server.
	// --------------------------------------------------------

	fmt.Println("Notes API starting...")
	fmt.Println()
	fmt.Println("Server: http://localhost:8080")
	fmt.Println()
	fmt.Println("Endpoints:")
	fmt.Println("GET    /notes")
	fmt.Println("POST   /notes")
	fmt.Println("DELETE /notes/{id}")
	fmt.Println()
	fmt.Println("Press Ctrl+C to stop.")
	fmt.Println()

	err := http.ListenAndServe(
		":8080",
		handler,
	)

	if err != nil {
		log.Fatal(err)
	}
}
