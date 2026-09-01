package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ============================================================
// DAY 35 — FIRST REST API PROJECT
// FINAL MVP VERSION
//
// CRUD
// JSON
// HTTP STATUS CODES
// VALIDATION
// EDGE CASES
// MIDDLEWARE
// TESTS
// GRACEFUL SHUTDOWN
// TIMEOUTS
// REFLECTION
// ============================================================

// ============================================================
// MODEL
// ============================================================

type Note struct {
	ID        int       `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ============================================================
// REQUEST DTO
// ============================================================

type NoteRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// ============================================================
// API ERROR
// ============================================================

type APIError struct {
	Error string `json:"error"`
}

// ============================================================
// IN-MEMORY STORE
//
// In a real production application this would normally be
// separated into its own package.
//
// For this exercise everything remains in main.go.
// ============================================================

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

// ------------------------------------------------------------
// LIST
// ------------------------------------------------------------

func (s *NoteStore) List() []Note {
	s.mu.RLock()
	defer s.mu.RUnlock()

	notes := make([]Note, 0, len(s.notes))

	for _, note := range s.notes {
		notes = append(notes, note)
	}

	return notes
}

// ------------------------------------------------------------
// FIND
// ------------------------------------------------------------

func (s *NoteStore) Find(id int) (Note, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	note, exists := s.notes[id]

	return note, exists
}

// ------------------------------------------------------------
// TITLE EXISTS
//
// Used to prevent duplicate note titles.
// ------------------------------------------------------------

func (s *NoteStore) TitleExists(title string, exceptID int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, note := range s.notes {
		if note.ID == exceptID {
			continue
		}

		if strings.EqualFold(
			strings.TrimSpace(note.Title),
			strings.TrimSpace(title),
		) {
			return true
		}
	}

	return false
}

// ------------------------------------------------------------
// CREATE
// ------------------------------------------------------------

func (s *NoteStore) Create(
	title string,
	content string,
) Note {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	note := Note{
		ID:        s.nextID,
		Title:     title,
		Content:   content,
		CreatedAt: now,
		UpdatedAt: now,
	}

	s.notes[note.ID] = note
	s.nextID++

	return note
}

// ------------------------------------------------------------
// UPDATE
// ------------------------------------------------------------

func (s *NoteStore) Update(
	id int,
	title string,
	content string,
) (Note, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	note, exists := s.notes[id]

	if !exists {
		return Note{}, false
	}

	note.Title = title
	note.Content = content
	note.UpdatedAt = time.Now()

	s.notes[id] = note

	return note, true
}

// ------------------------------------------------------------
// DELETE
// ------------------------------------------------------------

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
// JSON HELPERS
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
			"response encoding error: %v",
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
		APIError{
			Error: message,
		},
	)
}

// ============================================================
// REQUEST VALIDATION
// ============================================================

func decodeNoteRequest(
	w http.ResponseWriter,
	r *http.Request,
) (NoteRequest, bool) {

	if r.Body == nil {
		writeError(
			w,
			http.StatusBadRequest,
			"request body is required",
		)

		return NoteRequest{}, false
	}

	var request NoteRequest

	decoder := json.NewDecoder(r.Body)

	// Reject unknown JSON fields.
	decoder.DisallowUnknownFields()

	err := decoder.Decode(&request)

	if err != nil {

		if errors.Is(err, context.Canceled) {
			return NoteRequest{}, false
		}

		writeError(
			w,
			http.StatusBadRequest,
			"invalid JSON body",
		)

		return NoteRequest{}, false
	}

	// Make sure there isn't a second JSON value.
	var extra any

	err = decoder.Decode(&extra)

	if err == nil {
		writeError(
			w,
			http.StatusBadRequest,
			"request body must contain one JSON object",
		)

		return NoteRequest{}, false
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

		return NoteRequest{}, false
	}

	if len(request.Title) > 200 {
		writeError(
			w,
			http.StatusBadRequest,
			"title must be 200 characters or fewer",
		)

		return NoteRequest{}, false
	}

	if len(request.Content) > 10000 {
		writeError(
			w,
			http.StatusBadRequest,
			"content must be 10000 characters or fewer",
		)

		return NoteRequest{}, false
	}

	return request, true
}

// ============================================================
// ID PARSING
// ============================================================

func parseNoteID(
	w http.ResponseWriter,
	r *http.Request,
) (int, bool) {

	idString := strings.TrimPrefix(
		r.URL.Path,
		"/notes/",
	)

	if idString == "" || idString == r.URL.Path {
		writeError(
			w,
			http.StatusBadRequest,
			"note ID is required",
		)

		return 0, false
	}

	id, err := strconv.Atoi(idString)

	if err != nil || id <= 0 {
		writeError(
			w,
			http.StatusBadRequest,
			"invalid note ID",
		)

		return 0, false
	}

	return id, true
}

// ============================================================
// GET /health
//
// Small health endpoint.
//
// Useful for smoke testing the service.
// ============================================================

func (s *Server) health(
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

	writeJSON(
		w,
		http.StatusOK,
		map[string]string{
			"status": "ok",
		},
	)
}

// ============================================================
// GET /notes
//
// READ
// ============================================================

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
// GET /notes/{id}
//
// READ ONE
// ============================================================

func (s *Server) getNote(
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

	id, ok := parseNoteID(w, r)

	if !ok {
		return
	}

	note, exists := s.store.Find(id)

	if !exists {
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
		note,
	)
}

// ============================================================
// POST /notes
//
// CREATE
// ============================================================

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

	request, ok := decodeNoteRequest(
		w,
		r,
	)

	if !ok {
		return
	}

	// Duplicate title check.
	if s.store.TitleExists(
		request.Title,
		0,
	) {
		writeError(
			w,
			http.StatusConflict,
			"a note with this title already exists",
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
// PUT /notes/{id}
//
// UPDATE
// ============================================================

func (s *Server) updateNote(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodPut {
		writeError(
			w,
			http.StatusMethodNotAllowed,
			"method not allowed",
		)

		return
	}

	id, ok := parseNoteID(w, r)

	if !ok {
		return
	}

	_, exists := s.store.Find(id)

	if !exists {
		writeError(
			w,
			http.StatusNotFound,
			"note not found",
		)

		return
	}

	request, ok := decodeNoteRequest(
		w,
		r,
	)

	if !ok {
		return
	}

	if s.store.TitleExists(
		request.Title,
		id,
	) {
		writeError(
			w,
			http.StatusConflict,
			"a note with this title already exists",
		)

		return
	}

	note, updated := s.store.Update(
		id,
		request.Title,
		request.Content,
	)

	if !updated {
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
		note,
	)
}

// ============================================================
// DELETE /notes/{id}
//
// DELETE
// ============================================================

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

	id, ok := parseNoteID(w, r)

	if !ok {
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
	// Health
	// --------------------------------------------------------

	mux.HandleFunc(
		"/health",
		s.health,
	)

	// --------------------------------------------------------
	// /notes
	//
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
	//
	// GET
	// PUT
	// DELETE
	// --------------------------------------------------------

	mux.HandleFunc(
		"/notes/",
		func(w http.ResponseWriter, r *http.Request) {

			switch r.Method {

			case http.MethodGet:
				s.getNote(w, r)

			case http.MethodPut:
				s.updateNote(w, r)

			case http.MethodDelete:
				s.deleteNote(w, r)

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
	// Middleware chain
	//
	// Recovery
	//     ↓
	// Logging
	//     ↓
	// Router
	// --------------------------------------------------------

	return recoveryMiddleware(
		loggingMiddleware(mux),
	)
}

// ============================================================
// LOGGING MIDDLEWARE
// ============================================================

type responseRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *responseRecorder) WriteHeader(
	statusCode int,
) {
	r.statusCode = statusCode

	r.ResponseWriter.WriteHeader(
		statusCode,
	)
}

func (r *responseRecorder) Write(
	data []byte,
) (int, error) {

	if r.statusCode == 0 {
		r.statusCode = http.StatusOK
	}

	return r.ResponseWriter.Write(data)
}

func loggingMiddleware(
	next http.Handler,
) http.Handler {

	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {

			start := time.Now()

			recorder := &responseRecorder{
				ResponseWriter: w,
			}

			next.ServeHTTP(
				recorder,
				r,
			)

			status := recorder.statusCode

			if status == 0 {
				status = http.StatusOK
			}

			log.Printf(
				"%s %s -> %d (%v)",
				r.Method,
				r.URL.Path,
				status,
				time.Since(start),
			)
		},
	)
}

// ============================================================
// RECOVERY MIDDLEWARE
// ============================================================

func recoveryMiddleware(
	next http.Handler,
) http.Handler {

	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {

			defer func() {

				if recovered := recover(); recovered != nil {

					log.Printf(
						"panic recovered: %v",
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
) error {

	if got != want {
		return fmt.Errorf(
			"%s: got status %d, want %d",
			name,
			got,
			want,
		)
	}

	return nil
}

func decodeTestNote(
	recorder *httptest.ResponseRecorder,
) (Note, error) {

	var note Note

	err := json.Unmarshal(
		recorder.Body.Bytes(),
		&note,
	)

	return note, err
}

// ============================================================
// TEST 1
//
// HEALTH
// ============================================================

func testHealth() error {

	handler := newTestHandler()

	request := httptest.NewRequest(
		http.MethodGet,
		"/health",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		request,
	)

	if err := assertStatus(
		"health",
		recorder.Code,
		http.StatusOK,
	); err != nil {
		return err
	}

	return nil
}

// ============================================================
// TEST 2
//
// CREATE
// ============================================================

func testCreate() error {

	handler := newTestHandler()

	body := strings.NewReader(
		`{
			"title": "Go REST API",
			"content": "Build an MVP"
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

	if err := assertStatus(
		"create",
		recorder.Code,
		http.StatusCreated,
	); err != nil {
		return err
	}

	note, err := decodeTestNote(recorder)

	if err != nil {
		return fmt.Errorf(
			"create returned invalid JSON: %w",
			err,
		)
	}

	if note.ID != 1 {
		return fmt.Errorf(
			"expected ID 1, got %d",
			note.ID,
		)
	}

	if note.Title != "Go REST API" {
		return fmt.Errorf(
			"unexpected title: %s",
			note.Title,
		)
	}

	return nil
}

// ============================================================
// TEST 3
//
// LIST
// ============================================================

func testList() error {

	store := NewNoteStore()

	store.Create(
		"First",
		"First note",
	)

	store.Create(
		"Second",
		"Second note",
	)

	server := NewServer(store)

	handler := server.routes()

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

	if err := assertStatus(
		"list",
		recorder.Code,
		http.StatusOK,
	); err != nil {
		return err
	}

	var notes []Note

	err := json.Unmarshal(
		recorder.Body.Bytes(),
		&notes,
	)

	if err != nil {
		return fmt.Errorf(
			"list returned invalid JSON: %w",
			err,
		)
	}

	if len(notes) != 2 {
		return fmt.Errorf(
			"expected 2 notes, got %d",
			len(notes),
		)
	}

	return nil
}

// ============================================================
// TEST 4
//
// GET ONE
// ============================================================

func testGetOne() error {

	store := NewNoteStore()

	store.Create(
		"Find Me",
		"Content",
	)

	server := NewServer(store)

	handler := server.routes()

	request := httptest.NewRequest(
		http.MethodGet,
		"/notes/1",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		request,
	)

	if err := assertStatus(
		"get one",
		recorder.Code,
		http.StatusOK,
	); err != nil {
		return err
	}

	note, err := decodeTestNote(recorder)

	if err != nil {
		return err
	}

	if note.ID != 1 {
		return fmt.Errorf(
			"expected ID 1, got %d",
			note.ID,
		)
	}

	return nil
}

// ============================================================
// TEST 5
//
// UPDATE
// ============================================================

func testUpdate() error {

	store := NewNoteStore()

	store.Create(
		"Old Title",
		"Old content",
	)

	server := NewServer(store)

	handler := server.routes()

	body := strings.NewReader(
		`{
			"title": "New Title",
			"content": "New content"
		}`,
	)

	request := httptest.NewRequest(
		http.MethodPut,
		"/notes/1",
		body,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		request,
	)

	if err := assertStatus(
		"update",
		recorder.Code,
		http.StatusOK,
	); err != nil {
		return err
	}

	note, err := decodeTestNote(recorder)

	if err != nil {
		return err
	}

	if note.Title != "New Title" {
		return fmt.Errorf(
			"expected New Title, got %s",
			note.Title,
		)
	}

	if note.Content != "New content" {
		return fmt.Errorf(
			"expected New content, got %s",
			note.Content,
		)
	}

	return nil
}

// ============================================================
// TEST 6
//
// DELETE
// ============================================================

func testDelete() error {

	store := NewNoteStore()

	store.Create(
		"Delete Me",
		"Temporary",
	)

	server := NewServer(store)

	handler := server.routes()

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

	if err := assertStatus(
		"delete",
		recorder.Code,
		http.StatusOK,
	); err != nil {
		return err
	}

	_, exists := store.Find(1)

	if exists {
		return fmt.Errorf(
			"note still exists after delete",
		)
	}

	return nil
}

// ============================================================
// TEST 7
//
// INVALID JSON
// ============================================================

func testInvalidJSON() error {

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

	return assertStatus(
		"invalid JSON",
		recorder.Code,
		http.StatusBadRequest,
	)
}

// ============================================================
// TEST 8
//
// EMPTY BODY
// ============================================================

func testEmptyBody() error {

	handler := newTestHandler()

	request := httptest.NewRequest(
		http.MethodPost,
		"/notes",
		strings.NewReader(""),
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		request,
	)

	return assertStatus(
		"empty body",
		recorder.Code,
		http.StatusBadRequest,
	)
}

// ============================================================
// TEST 9
//
// DUPLICATE CREATE
// ============================================================

func testDuplicateCreate() error {

	store := NewNoteStore()

	store.Create(
		"Unique",
		"Existing",
	)

	server := NewServer(store)

	handler := server.routes()

	body := strings.NewReader(
		`{
			"title": "Unique",
			"content": "Duplicate"
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

	return assertStatus(
		"duplicate create",
		recorder.Code,
		http.StatusConflict,
	)
}

// ============================================================
// TEST 10
//
// INVALID ID
// ============================================================

func testInvalidID() error {

	handler := newTestHandler()

	request := httptest.NewRequest(
		http.MethodGet,
		"/notes/abc",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		request,
	)

	return assertStatus(
		"invalid ID",
		recorder.Code,
		http.StatusBadRequest,
	)
}

// ============================================================
// TEST 11
//
// MISSING ID
// ============================================================

func testMissingID() error {

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

	return assertStatus(
		"missing ID",
		recorder.Code,
		http.StatusBadRequest,
	)
}

// ============================================================
// TEST 12
//
// NOT FOUND
// ============================================================

func testNotFound() error {

	handler := newTestHandler()

	request := httptest.NewRequest(
		http.MethodGet,
		"/notes/999",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		request,
	)

	return assertStatus(
		"not found",
		recorder.Code,
		http.StatusNotFound,
	)
}

// ============================================================
// TEST 13
//
// WRONG METHOD
// ============================================================

func testWrongMethod() error {

	handler := newTestHandler()

	request := httptest.NewRequest(
		http.MethodPatch,
		"/notes",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		request,
	)

	return assertStatus(
		"wrong method",
		recorder.Code,
		http.StatusMethodNotAllowed,
	)
}

// ============================================================
// TEST 14
//
// UNKNOWN JSON FIELD
// ============================================================

func testUnknownJSONField() error {

	handler := newTestHandler()

	body := strings.NewReader(
		`{
			"title": "Test",
			"content": "Test",
			"unknown": "not allowed"
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

	return assertStatus(
		"unknown JSON field",
		recorder.Code,
		http.StatusBadRequest,
	)
}

// ============================================================
// TEST 15
//
// FULL CRUD FLOW
//
// CREATE
// LIST
// GET
// UPDATE
// DELETE
// GET -> 404
// ============================================================

func testFullCRUD() error {

	store := NewNoteStore()

	server := NewServer(store)

	handler := server.routes()

	// --------------------------------------------------------
	// CREATE
	// --------------------------------------------------------

	createBody := strings.NewReader(
		`{
			"title": "MVP",
			"content": "First REST API"
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

	if err := assertStatus(
		"CRUD create",
		createRecorder.Code,
		http.StatusCreated,
	); err != nil {
		return err
	}

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

	if err := assertStatus(
		"CRUD list",
		listRecorder.Code,
		http.StatusOK,
	); err != nil {
		return err
	}

	// --------------------------------------------------------
	// GET ONE
	// --------------------------------------------------------

	getRequest := httptest.NewRequest(
		http.MethodGet,
		"/notes/1",
		nil,
	)

	getRecorder := httptest.NewRecorder()

	handler.ServeHTTP(
		getRecorder,
		getRequest,
	)

	if err := assertStatus(
		"CRUD get",
		getRecorder.Code,
		http.StatusOK,
	); err != nil {
		return err
	}

	// --------------------------------------------------------
	// UPDATE
	// --------------------------------------------------------

	updateBody := strings.NewReader(
		`{
			"title": "Updated MVP",
			"content": "Updated REST API"
		}`,
	)

	updateRequest := httptest.NewRequest(
		http.MethodPut,
		"/notes/1",
		updateBody,
	)

	updateRecorder := httptest.NewRecorder()

	handler.ServeHTTP(
		updateRecorder,
		updateRequest,
	)

	if err := assertStatus(
		"CRUD update",
		updateRecorder.Code,
		http.StatusOK,
	); err != nil {
		return err
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

	if err := assertStatus(
		"CRUD delete",
		deleteRecorder.Code,
		http.StatusOK,
	); err != nil {
		return err
	}

	// --------------------------------------------------------
	// GET AFTER DELETE
	// --------------------------------------------------------

	getDeletedRequest := httptest.NewRequest(
		http.MethodGet,
		"/notes/1",
		nil,
	)

	getDeletedRecorder := httptest.NewRecorder()

	handler.ServeHTTP(
		getDeletedRecorder,
		getDeletedRequest,
	)

	return assertStatus(
		"CRUD final get",
		getDeletedRecorder.Code,
		http.StatusNotFound,
	)
}

// ============================================================
// TEST RUNNER
// ============================================================

func runTests() {

	tests := []struct {
		name string
		fn   func() error
	}{
		{"Health", testHealth},
		{"Create", testCreate},
		{"List", testList},
		{"Get One", testGetOne},
		{"Update", testUpdate},
		{"Delete", testDelete},
		{"Invalid JSON", testInvalidJSON},
		{"Empty Body", testEmptyBody},
		{"Duplicate Create", testDuplicateCreate},
		{"Invalid ID", testInvalidID},
		{"Missing ID", testMissingID},
		{"Not Found", testNotFound},
		{"Wrong Method", testWrongMethod},
		{"Unknown JSON Field", testUnknownJSONField},
		{"Full CRUD", testFullCRUD},
	}

	fmt.Println()
	fmt.Println("==============================================")
	fmt.Println("DAY 35 — MVP TEST SUITE")
	fmt.Println("==============================================")

	passed := 0

	for _, test := range tests {

		err := test.fn()

		if err != nil {
			fmt.Printf(
				"FAIL  %-25s %v\n",
				test.name,
				err,
			)

			continue
		}

		fmt.Printf(
			"PASS  %-25s\n",
			test.name,
		)

		passed++
	}

	fmt.Println("----------------------------------------------")

	if passed == len(tests) {
		fmt.Printf(
			"RESULT: ALL TESTS PASSED (%d/%d)\n",
			passed,
			len(tests),
		)
	} else {
		fmt.Printf(
			"RESULT: %d/%d PASSED\n",
			passed,
			len(tests),
		)
	}

	fmt.Println("==============================================")
	fmt.Println()
}

// ============================================================
// MANUAL TESTING / CURL CHEAT SHEET
//
// Start:
//
// go run main.go
//
// ------------------------------------------------------------
//
// HEALTH
//
// curl http://localhost:8080/health
//
// ------------------------------------------------------------
//
// CREATE
//
// curl -X POST http://localhost:8080/notes \
//   -H "Content-Type: application/json" \
//   -d '{"title":"Learn Go","content":"Build a REST API"}'
//
// ------------------------------------------------------------
//
// LIST
//
// curl http://localhost:8080/notes
//
// ------------------------------------------------------------
//
// GET ONE
//
// curl http://localhost:8080/notes/1
//
// ------------------------------------------------------------
//
// UPDATE
//
// curl -X PUT http://localhost:8080/notes/1 \
//   -H "Content-Type: application/json" \
//   -d '{"title":"Learn Go Advanced","content":"Build a production API"}'
//
// ------------------------------------------------------------
//
// DELETE
//
// curl -X DELETE http://localhost:8080/notes/1
//
// ------------------------------------------------------------
//
// EDGE CASE: INVALID JSON
//
// curl -X POST http://localhost:8080/notes \
//   -H "Content-Type: application/json" \
//   -d '{"title":'
//
// Expected: 400
//
// ------------------------------------------------------------
//
// EDGE CASE: EMPTY BODY
//
// curl -X POST http://localhost:8080/notes
//
// Expected: 400
//
// ------------------------------------------------------------
//
// EDGE CASE: DUPLICATE TITLE
//
// curl -X POST http://localhost:8080/notes \
//   -H "Content-Type: application/json" \
//   -d '{"title":"Learn Go","content":"Duplicate"}'
//
// Expected: 409
//
// ------------------------------------------------------------
//
// EDGE CASE: INVALID ID
//
// curl http://localhost:8080/notes/abc
//
// Expected: 400
//
// ------------------------------------------------------------
//
// EDGE CASE: MISSING NOTE
//
// curl http://localhost:8080/notes/999
//
// Expected: 404
//
// ------------------------------------------------------------
//
// EDGE CASE: WRONG METHOD
//
// curl -X PATCH http://localhost:8080/notes
//
// Expected: 405
//
// ============================================================

// ============================================================
// DAY 35 — REFLECTION
//
// What I learned:
//
// 1. HTTP handlers should have one clear responsibility.
// 2. HTTP status codes are part of the API contract.
// 3. JSON validation is important because clients can send
//    malformed or unexpected input.
// 4. In-memory storage is useful for learning the HTTP layer,
//    but it is not persistent.
// 5. Shared state requires synchronization when concurrent
//    requests can access it.
// 6. Middleware allows cross-cutting concerns such as logging
//    and panic recovery to stay outside business logic.
// 7. httptest makes HTTP handlers testable without starting a
//    real network server.
// 8. Edge cases are as important as successful CRUD flows.
// 9. Duplicate data should be rejected explicitly when the
//    API contract requires uniqueness.
// 10. A working MVP should be reviewed for consistency before
//     adding databases, authentication, caching, or queues.
//
// Most challenging parts:
//
// - Designing consistent status codes.
// - Handling invalid IDs safely.
// - Validating JSON bodies.
// - Protecting shared in-memory state.
// - Keeping handlers small while still covering edge cases.
//
// Next improvements:
//
// - PostgreSQL persistence
// - Repository interface
// - Service layer
// - Authentication / authorization
// - Request validation package
// - Structured logging
// - OpenAPI documentation
// - Integration tests
// - Docker
// - CI/CD
//
// Technical debt:
//
// The current application intentionally keeps all code inside
// main.go for the learning exercise. In a real project this
// should be split into packages such as:
//
// cmd/api
// internal/handler
// internal/service
// internal/repository
// internal/model
//
// ============================================================

// ============================================================
// MAIN
// ============================================================

func main() {

	// --------------------------------------------------------
	// Run internal test suite before starting the server.
	// --------------------------------------------------------

	runTests()

	// --------------------------------------------------------
	// Application dependencies.
	// --------------------------------------------------------

	store := NewNoteStore()

	server := NewServer(store)

	handler := server.routes()

	// --------------------------------------------------------
	// HTTP server configuration.
	//
	// Timeouts prevent connections from blocking forever.
	// --------------------------------------------------------

	httpServer := &http.Server{
		Addr:              ":8080",
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// --------------------------------------------------------
	// Graceful shutdown.
	// --------------------------------------------------------

	serverErrors := make(chan error, 1)

	go func() {

		log.Println(
			"Notes API running on http://localhost:8080",
		)

		serverErrors <- httpServer.ListenAndServe()
	}()

	// --------------------------------------------------------
	// Wait for Ctrl+C / SIGTERM.
	// --------------------------------------------------------

	signalChannel := make(
		chan os.Signal,
		1,
	)

	signal.Notify(
		signalChannel,
		os.Interrupt,
		syscall.SIGTERM,
	)

	select {

	case err := <-serverErrors:

		if !errors.Is(
			err,
			http.ErrServerClosed,
		) {
			log.Fatalf(
				"HTTP server error: %v",
				err,
			)
		}

	case signalReceived := <-signalChannel:

		log.Printf(
			"shutdown signal received: %v",
			signalReceived,
		)

		shutdownContext, cancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)

		defer cancel()

		if err := httpServer.Shutdown(
			shutdownContext,
		); err != nil {
			log.Printf(
				"graceful shutdown failed: %v",
				err,
			)

			if closeErr := httpServer.Close(); closeErr != nil {
				log.Printf(
					"server close failed: %v",
					closeErr,
				)
			}
		}

		log.Println("server stopped")
	}
}
