package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ============================================================
// TEST HELPERS
// ============================================================

func newJSONRequest(
	t *testing.T,
	method string,
	path string,
	body string,
) *http.Request {
	t.Helper()

	req := httptest.NewRequest(
		method,
		path,
		bytes.NewBufferString(body),
	)

	req.Header.Set(
		"Content-Type",
		"application/json",
	)

	return req
}

func decodeProblem(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
) ProblemResponse {
	t.Helper()

	var response ProblemResponse

	err := json.NewDecoder(
		recorder.Body,
	).Decode(&response)

	if err != nil {
		t.Fatalf(
			"failed to decode problem response: %v",
			err,
		)
	}

	return response
}

func decodeTask(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
) Task {
	t.Helper()

	var task Task

	err := json.NewDecoder(
		recorder.Body,
	).Decode(&task)

	if err != nil {
		t.Fatalf(
			"failed to decode task response: %v",
			err,
		)
	}

	return task
}

func assertStatus(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	expected int,
) {
	t.Helper()

	if recorder.Code != expected {
		t.Fatalf(
			"expected status %d, got %d; body=%s",
			expected,
			recorder.Code,
			recorder.Body.String(),
		)
	}
}

func assertJSONContentType(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
) {
	t.Helper()

	contentType := recorder.Header().Get(
		"Content-Type",
	)

	if contentType != "application/json" {
		t.Fatalf(
			"expected Content-Type application/json, got %q",
			contentType,
		)
	}
}

// ============================================================
// TASK 1 + TASK 4
//
// Validate input and test all unhappy paths.
// ============================================================

func TestCreateTaskUnhappyPaths(t *testing.T) {
	tests := []struct {
		name string
		body string

		expectedStatus int
		expectedCode   string
		expectedDetail string
	}{
		{
			name: "empty title",

			body: `{
				"title": ""
			}`,

			expectedStatus: http.StatusBadRequest,
			expectedCode:   "VALIDATION_ERROR",
			expectedDetail: "field: title",
		},

		{
			name: "whitespace title",

			body: `{
				"title": "     "
			}`,

			expectedStatus: http.StatusBadRequest,
			expectedCode:   "VALIDATION_ERROR",
			expectedDetail: "field: title",
		},

		{
			name: "title too long",

			body: `{
				"title": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			}`,

			expectedStatus: http.StatusBadRequest,
			expectedCode:   "VALIDATION_ERROR",
			expectedDetail: "field: title",
		},

		{
			name: "malformed JSON",

			body: `{
				"title": "Learn Go"`,

			expectedStatus: http.StatusBadRequest,
			expectedCode:   "VALIDATION_ERROR",
			expectedDetail: "request body must contain valid JSON",
		},

		{
			name: "wrong HTTP method",

			body: `{
				"title": "Learn Go"
			}`,

			expectedStatus: http.StatusBadRequest,
			expectedCode:   "VALIDATION_ERROR",
			expectedDetail: "unsupported HTTP method",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewTaskStore()
			handler := NewTaskHandler(store)

			method := http.MethodPost

			if tt.name == "wrong HTTP method" {
				method = http.MethodGet
			}

			req := newJSONRequest(
				t,
				method,
				"/tasks",
				tt.body,
			)

			recorder := httptest.NewRecorder()

			handler.CreateTask(
				recorder,
				req,
			)

			assertStatus(
				t,
				recorder,
				tt.expectedStatus,
			)

			assertJSONContentType(
				t,
				recorder,
			)

			problem := decodeProblem(
				t,
				recorder,
			)

			if problem.Code != tt.expectedCode {
				t.Fatalf(
					"expected code %q, got %q",
					tt.expectedCode,
					problem.Code,
				)
			}

			if problem.Details != tt.expectedDetail {
				t.Fatalf(
					"expected details %q, got %q",
					tt.expectedDetail,
					problem.Details,
				)
			}
		})
	}
}

// ============================================================
// VALID CREATE TASK TEST
// ============================================================

func TestCreateTaskSuccess(t *testing.T) {
	store := NewTaskStore()
	handler := NewTaskHandler(store)

	req := newJSONRequest(
		t,
		http.MethodPost,
		"/tasks",
		`{
			"title": "Learn HTTP Testing"
		}`,
	)

	recorder := httptest.NewRecorder()

	handler.CreateTask(
		recorder,
		req,
	)

	assertStatus(
		t,
		recorder,
		http.StatusCreated,
	)

	assertJSONContentType(
		t,
		recorder,
	)

	task := decodeTask(
		t,
		recorder,
	)

	if task.ID != 3 {
		t.Fatalf(
			"expected ID 3, got %d",
			task.ID,
		)
	}

	if task.Title != "Learn HTTP Testing" {
		t.Fatalf(
			"expected title %q, got %q",
			"Learn HTTP Testing",
			task.Title,
		)
	}
}

// ============================================================
// TASK 1
//
// Validate ID input.
// ============================================================

func TestValidateID(t *testing.T) {
	tests := []struct {
		name string
		id   string

		wantID      int
		shouldError bool
	}{
		{
			name:        "valid ID",
			id:          "10",
			wantID:      10,
			shouldError: false,
		},
		{
			name:        "malformed ID",
			id:          "abc",
			shouldError: true,
		},
		{
			name:        "empty ID",
			id:          "",
			shouldError: true,
		},
		{
			name:        "zero ID",
			id:          "0",
			shouldError: true,
		},
		{
			name:        "negative ID",
			id:          "-5",
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := validateID(tt.id)

			if tt.shouldError {
				if err == nil {
					t.Fatal("expected validation error, got nil")
				}

				if !errors.Is(err, ErrValidation) {
					t.Fatalf(
						"expected ErrValidation, got %v",
						err,
					)
				}

				return
			}

			if err != nil {
				t.Fatalf(
					"unexpected error: %v",
					err,
				)
			}

			if id != tt.wantID {
				t.Fatalf(
					"expected ID %d, got %d",
					tt.wantID,
					id,
				)
			}
		})
	}
}

// ============================================================
// TASK 1
//
// Validate title input.
// ============================================================

func TestValidateTitle(t *testing.T) {
	tests := []struct {
		name  string
		title string

		shouldError bool
	}{
		{
			name:        "valid title",
			title:       "Learn Go",
			shouldError: false,
		},
		{
			name:        "empty title",
			title:       "",
			shouldError: true,
		},
		{
			name:        "whitespace title",
			title:       "   ",
			shouldError: true,
		},
		{
			name: "too long title",
			title: strings.Repeat(
				"a",
				maxTitleLength+1,
			),
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTitle(tt.title)

			if tt.shouldError {
				if err == nil {
					t.Fatal("expected validation error, got nil")
				}

				if !errors.Is(err, ErrValidation) {
					t.Fatalf(
						"expected ErrValidation, got %v",
						err,
					)
				}

				return
			}

			if err != nil {
				t.Fatalf(
					"unexpected error: %v",
					err,
				)
			}
		})
	}
}

// ============================================================
// TASK 2
//
// Map validation -> 400
// Map not found -> 404
// Map internal -> 500
// ============================================================

func TestMapErrorToStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error

		expectedStatus int
	}{
		{
			name: "validation error",

			err: fmt.Errorf(
				"%w: invalid input",
				ErrValidation,
			),

			expectedStatus: http.StatusBadRequest,
		},

		{
			name: "not found error",

			err: fmt.Errorf(
				"%w: task does not exist",
				ErrNotFound,
			),

			expectedStatus: http.StatusNotFound,
		},

		{
			name: "internal error",

			err: fmt.Errorf(
				"%w: database unavailable",
				ErrInternal,
			),

			expectedStatus: http.StatusInternalServerError,
		},

		{
			name: "unknown error",

			err: errors.New(
				"unexpected database failure",
			),

			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := mapErrorToStatus(tt.err)

			if status != tt.expectedStatus {
				t.Fatalf(
					"expected status %d, got %d",
					tt.expectedStatus,
					status,
				)
			}
		})
	}
}

// ============================================================
// TASK 2 + TASK 3
//
// GET /tasks/{id}
// 400 malformed ID
// 404 missing ID
// ============================================================

func TestGetTaskUnhappyPaths(t *testing.T) {
	tests := []struct {
		name string
		path string

		expectedStatus int
		expectedCode   string
	}{
		{
			name: "malformed ID",

			path: "/tasks/abc",

			expectedStatus: http.StatusBadRequest,
			expectedCode:   "VALIDATION_ERROR",
		},

		{
			name: "empty ID",

			path: "/tasks/",

			expectedStatus: http.StatusBadRequest,
			expectedCode:   "VALIDATION_ERROR",
		},

		{
			name: "zero ID",

			path: "/tasks/0",

			expectedStatus: http.StatusBadRequest,
			expectedCode:   "VALIDATION_ERROR",
		},

		{
			name: "negative ID",

			path: "/tasks/-1",

			expectedStatus: http.StatusBadRequest,
			expectedCode:   "VALIDATION_ERROR",
		},

		{
			name: "missing task",

			path: "/tasks/999",

			expectedStatus: http.StatusNotFound,
			expectedCode:   "NOT_FOUND",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewTaskStore()
			handler := NewTaskHandler(store)

			req := httptest.NewRequest(
				http.MethodGet,
				tt.path,
				nil,
			)

			recorder := httptest.NewRecorder()

			handler.GetTask(
				recorder,
				req,
			)

			assertStatus(
				t,
				recorder,
				tt.expectedStatus,
			)

			assertJSONContentType(
				t,
				recorder,
			)

			problem := decodeProblem(
				t,
				recorder,
			)

			if problem.Code != tt.expectedCode {
				t.Fatalf(
					"expected code %q, got %q",
					tt.expectedCode,
					problem.Code,
				)
			}
		})
	}
}

// ============================================================
// GET /tasks/{id}
// SUCCESS
// ============================================================

func TestGetTaskSuccess(t *testing.T) {
	store := NewTaskStore()
	handler := NewTaskHandler(store)

	req := httptest.NewRequest(
		http.MethodGet,
		"/tasks/1",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.GetTask(
		recorder,
		req,
	)

	assertStatus(
		t,
		recorder,
		http.StatusOK,
	)

	assertJSONContentType(
		t,
		recorder,
	)

	task := decodeTask(
		t,
		recorder,
	)

	if task.ID != 1 {
		t.Fatalf(
			"expected ID 1, got %d",
			task.ID,
		)
	}

	if task.Title != "Learn Go" {
		t.Fatalf(
			"expected title %q, got %q",
			"Learn Go",
			task.Title,
		)
	}
}

// ============================================================
// TASK 3
//
// Centralized error writer.
//
// Verify that all error types produce consistent JSON.
// ============================================================

func TestWriteError(t *testing.T) {
	tests := []struct {
		name string
		err  error

		expectedStatus  int
		expectedCode    string
		expectedMessage string
	}{
		{
			name: "validation error",

			err: fmt.Errorf(
				"%w: bad title",
				ErrValidation,
			),

			expectedStatus:  http.StatusBadRequest,
			expectedCode:    "VALIDATION_ERROR",
			expectedMessage: "request validation failed",
		},

		{
			name: "not found error",

			err: fmt.Errorf(
				"%w: missing task",
				ErrNotFound,
			),

			expectedStatus:  http.StatusNotFound,
			expectedCode:    "NOT_FOUND",
			expectedMessage: "requested resource was not found",
		},

		{
			name: "internal error",

			err: fmt.Errorf(
				"%w: database failed",
				ErrInternal,
			),

			expectedStatus:  http.StatusInternalServerError,
			expectedCode:    "INTERNAL_ERROR",
			expectedMessage: "internal server error",
		},

		{
			name: "unknown error",

			err: errors.New(
				"something unexpected happened",
			),

			expectedStatus:  http.StatusInternalServerError,
			expectedCode:    "INTERNAL_ERROR",
			expectedMessage: "internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()

			writeError(
				recorder,
				tt.err,
				"test details",
			)

			assertStatus(
				t,
				recorder,
				tt.expectedStatus,
			)

			assertJSONContentType(
				t,
				recorder,
			)

			problem := decodeProblem(
				t,
				recorder,
			)

			if problem.Code != tt.expectedCode {
				t.Fatalf(
					"expected code %q, got %q",
					tt.expectedCode,
					problem.Code,
				)
			}

			if problem.Message != tt.expectedMessage {
				t.Fatalf(
					"expected message %q, got %q",
					tt.expectedMessage,
					problem.Message,
				)
			}
		})
	}
}

// ============================================================
// FULL HTTP ROUTER TEST
//
// Verify the actual HTTP routing layer as well.
// ============================================================

func TestHTTPRoutes(t *testing.T) {
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

	t.Run("POST /tasks", func(t *testing.T) {
		req := newJSONRequest(
			t,
			http.MethodPost,
			"/tasks",
			`{"title":"Router Test"}`,
		)

		recorder := httptest.NewRecorder()

		mux.ServeHTTP(
			recorder,
			req,
		)

		assertStatus(
			t,
			recorder,
			http.StatusCreated,
		)

		task := decodeTask(
			t,
			recorder,
		)

		if task.Title != "Router Test" {
			t.Fatalf(
				"expected title %q, got %q",
				"Router Test",
				task.Title,
			)
		}
	})

	t.Run("GET /tasks/1", func(t *testing.T) {
		req := httptest.NewRequest(
			http.MethodGet,
			"/tasks/1",
			nil,
		)

		recorder := httptest.NewRecorder()

		mux.ServeHTTP(
			recorder,
			req,
		)

		assertStatus(
			t,
			recorder,
			http.StatusOK,
		)

		task := decodeTask(
			t,
			recorder,
		)

		if task.ID != 1 {
			t.Fatalf(
				"expected ID 1, got %d",
				task.ID,
			)
		}
	})
}
