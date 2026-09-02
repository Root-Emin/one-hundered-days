package main

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ============================================================
// TEST REQUEST ID MIDDLEWARE
// ============================================================
//
// Task:
// Test Middleware
//
// httptest.NewRecorder:
// Response'u gerçek network bağlantısı açmadan test etmemizi
// sağlar.
//
// httptest.NewRequest:
// Test HTTP request oluşturur.
// ============================================================

func TestRequestIDMiddleware(t *testing.T) {

	handler := requestIDMiddleware(
		http.HandlerFunc(
			func(
				w http.ResponseWriter,
				r *http.Request,
			) {

				requestID := requestIDFromContext(
					r.Context(),
				)

				if requestID != "test-request-123" {

					t.Fatalf(
						"expected request ID in context to be %q, got %q",
						"test-request-123",
						requestID,
					)
				}

				w.WriteHeader(
					http.StatusAccepted,
				)
			},
		),
	)

	req := httptest.NewRequest(
		http.MethodGet,
		"/test",
		nil,
	)

	req.Header.Set(
		"X-Request-ID",
		"test-request-123",
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		req,
	)

	// --------------------------------------------------------
	// Assert status
	// --------------------------------------------------------

	if recorder.Code != http.StatusAccepted {

		t.Fatalf(
			"expected status %d, got %d",
			http.StatusAccepted,
			recorder.Code,
		)
	}

	// --------------------------------------------------------
	// Assert response header
	// --------------------------------------------------------

	gotRequestID := recorder.Header().Get(
		"X-Request-ID",
	)

	if gotRequestID != "test-request-123" {

		t.Fatalf(
			"expected X-Request-ID %q, got %q",
			"test-request-123",
			gotRequestID,
		)
	}
}

// ============================================================
// TEST GENERATED REQUEST ID
// ============================================================

func TestRequestIDMiddlewareGeneratesID(t *testing.T) {

	handler := requestIDMiddleware(
		http.HandlerFunc(
			func(
				w http.ResponseWriter,
				r *http.Request,
			) {

				requestID := requestIDFromContext(
					r.Context(),
				)

				if requestID == "" {

					t.Fatal(
						"expected generated request ID",
					)
				}

				w.WriteHeader(
					http.StatusOK,
				)
			},
		),
	)

	req := httptest.NewRequest(
		http.MethodGet,
		"/test",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		req,
	)

	requestID := recorder.Header().Get(
		"X-Request-ID",
	)

	if requestID == "" {

		t.Fatal(
			"expected X-Request-ID response header",
		)
	}
}

// ============================================================
// TEST CONFIG MIDDLEWARE
// ============================================================

func TestConfigMiddleware(t *testing.T) {

	config := Config{
		AppName: "test-api",
		Env:     "test",
		Port:    "9999",
	}

	handler := configMiddleware(
		config,
	)(
		http.HandlerFunc(
			func(
				w http.ResponseWriter,
				r *http.Request,
			) {

				got, ok := configFromContext(
					r.Context(),
				)

				if !ok {
					t.Fatal(
						"expected config in context",
					)
				}

				if got.AppName != "test-api" {

					t.Fatalf(
						"expected app name %q, got %q",
						"test-api",
						got.AppName,
					)
				}

				if got.Env != "test" {

					t.Fatalf(
						"expected env %q, got %q",
						"test",
						got.Env,
					)
				}

				w.WriteHeader(
					http.StatusNoContent,
				)
			},
		),
	)

	req := httptest.NewRequest(
		http.MethodGet,
		"/test",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		req,
	)

	if recorder.Code != http.StatusNoContent {

		t.Fatalf(
			"expected status %d, got %d",
			http.StatusNoContent,
			recorder.Code,
		)
	}
}

// ============================================================
// TEST STORE MIDDLEWARE
// ============================================================

func TestStoreMiddleware(t *testing.T) {

	store := NewInMemoryStore()

	handler := storeMiddleware(
		store,
	)(
		http.HandlerFunc(
			func(
				w http.ResponseWriter,
				r *http.Request,
			) {

				gotStore, ok := storeFromContext(
					r.Context(),
				)

				if !ok {
					t.Fatal(
						"expected store in context",
					)
				}

				if gotStore != store {

					t.Fatal(
						"expected exact store dependency",
					)
				}

				_, err := gotStore.Create(
					Task{
						Title: "middleware test",
					},
				)

				if err != nil {

					t.Fatalf(
						"unexpected create error: %v",
						err,
					)
				}

				w.WriteHeader(
					http.StatusCreated,
				)
			},
		),
	)

	req := httptest.NewRequest(
		http.MethodPost,
		"/tasks",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		req,
	)

	if recorder.Code != http.StatusCreated {

		t.Fatalf(
			"expected status %d, got %d",
			http.StatusCreated,
			recorder.Code,
		)
	}

	if len(store.List()) != 1 {

		t.Fatalf(
			"expected store side effect to create 1 task, got %d",
			len(store.List()),
		)
	}
}

// ============================================================
// TEST LOGGER MIDDLEWARE
// ============================================================
//
// Side effect:
// Logger gerçekten request bilgilerini yazmış mı?
// ============================================================

func TestLoggerMiddleware(t *testing.T) {

	var buffer bytes.Buffer

	logger := log.New(
		&buffer,
		"",
		0,
	)

	handler := Chain(
		http.HandlerFunc(
			func(
				w http.ResponseWriter,
				r *http.Request,
			) {

				w.WriteHeader(
					http.StatusAccepted,
				)
			},
		),

		requestIDMiddleware,

		loggerMiddleware(logger),
	)

	req := httptest.NewRequest(
		http.MethodGet,
		"/hello",
		nil,
	)

	req.Header.Set(
		"X-Request-ID",
		"logger-test-123",
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		req,
	)

	if recorder.Code != http.StatusAccepted {

		t.Fatalf(
			"expected status %d, got %d",
			http.StatusAccepted,
			recorder.Code,
		)
	}

	logOutput := buffer.String()

	if !strings.Contains(
		logOutput,
		"logger-test-123",
	) {

		t.Fatalf(
			"expected log to contain request ID, got %q",
			logOutput,
		)
	}

	if !strings.Contains(
		logOutput,
		"request started",
	) {

		t.Fatalf(
			"expected start log, got %q",
			logOutput,
		)
	}

	if !strings.Contains(
		logOutput,
		"request completed",
	) {

		t.Fatalf(
			"expected completion log, got %q",
			logOutput,
		)
	}
}

// ============================================================
// TEST MIDDLEWARE ORDER
// ============================================================
//
// Middleware order bizim Chain helper'ın en kritik
// davranışlarından biri.
//
// Beklenen:
//
// recovery
//   ↓
// request-id
//   ↓
// logger
//   ↓
// config
//   ↓
// store
//   ↓
// handler
// ============================================================

func TestMiddlewareOrder(t *testing.T) {

	var execution []string

	appendExecution := func(name string) {

		execution = append(
			execution,
			name,
		)
	}

	makeMiddleware := func(
		name string,
	) Middleware {

		return func(next http.Handler) http.Handler {

			return http.HandlerFunc(
				func(
					w http.ResponseWriter,
					r *http.Request,
				) {

					appendExecution(
						name + ":before",
					)

					next.ServeHTTP(
						w,
						r,
					)

					appendExecution(
						name + ":after",
					)
				},
			)
		}
	}

	handler := Chain(
		http.HandlerFunc(
			func(
				w http.ResponseWriter,
				r *http.Request,
			) {

				appendExecution(
					"handler",
				)

				w.WriteHeader(
					http.StatusOK,
				)
			},
		),

		makeMiddleware("A"),
		makeMiddleware("B"),
		makeMiddleware("C"),
	)

	req := httptest.NewRequest(
		http.MethodGet,
		"/",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		req,
	)

	expected := []string{
		"A:before",
		"B:before",
		"C:before",
		"handler",
		"C:after",
		"B:after",
		"A:after",
	}

	if len(execution) != len(expected) {

		t.Fatalf(
			"expected execution length %d, got %d: %v",
			len(expected),
			len(execution),
			execution,
		)
	}

	for i := range expected {

		if execution[i] != expected[i] {

			t.Fatalf(
				"at position %d expected %q, got %q\nfull execution: %v",
				i,
				expected[i],
				execution[i],
				execution,
			)
		}
	}
}

// ============================================================
// FULL INTEGRATION TEST
// ============================================================
//
// Bütün middleware zinciri + handler birlikte test ediliyor.
//
// Burada:
//
// Request ID
// Logger
// Config
// Store
// Handler
//
// aynı request üzerinde çalışıyor.
// ============================================================

func TestCreateTaskWithFullMiddlewareChain(t *testing.T) {

	var logBuffer bytes.Buffer

	logger := log.New(
		&logBuffer,
		"",
		0,
	)

	config := Config{
		AppName: "integration-api",
		Env:     "test",
		Port:    "8080",
	}

	store := NewInMemoryStore()

	handler := buildRouter(
		config,
		store,
		logger,
	)

	req := httptest.NewRequest(
		http.MethodPost,
		"/tasks?title=Learn%20Middleware",
		nil,
	)

	req.Header.Set(
		"X-Request-ID",
		"integration-123",
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		req,
	)

	// --------------------------------------------------------
	// Status
	// --------------------------------------------------------

	if recorder.Code != http.StatusCreated {

		t.Fatalf(
			"expected status %d, got %d",
			http.StatusCreated,
			recorder.Code,
		)
	}

	// --------------------------------------------------------
	// Request ID
	// --------------------------------------------------------

	if got := recorder.Header().Get(
		"X-Request-ID",
	); got != "integration-123" {

		t.Fatalf(
			"expected request ID %q, got %q",
			"integration-123",
			got,
		)
	}

	// --------------------------------------------------------
	// Config headers
	// --------------------------------------------------------

	if got := recorder.Header().Get(
		"X-App-Name",
	); got != "integration-api" {

		t.Fatalf(
			"expected X-App-Name %q, got %q",
			"integration-api",
			got,
		)
	}

	if got := recorder.Header().Get(
		"X-Environment",
	); got != "test" {

		t.Fatalf(
			"expected X-Environment %q, got %q",
			"test",
			got,
		)
	}

	// --------------------------------------------------------
	// Body
	// --------------------------------------------------------

	body := recorder.Body.String()

	if !strings.Contains(
		body,
		"Learn Middleware",
	) {

		t.Fatalf(
			"expected response body to contain task title, got %q",
			body,
		)
	}

	// --------------------------------------------------------
	// Store side effect
	// --------------------------------------------------------

	tasks := store.List()

	if len(tasks) != 1 {

		t.Fatalf(
			"expected 1 stored task, got %d",
			len(tasks),
		)
	}

	if tasks[0].Title != "Learn Middleware" {

		t.Fatalf(
			"expected stored title %q, got %q",
			"Learn Middleware",
			tasks[0].Title,
		)
	}

	// --------------------------------------------------------
	// Logger side effect
	// --------------------------------------------------------

	logOutput := logBuffer.String()

	if !strings.Contains(
		logOutput,
		"integration-123",
	) {

		t.Fatalf(
			"expected logs to contain request ID, got %q",
			logOutput,
		)
	}

	if !strings.Contains(
		logOutput,
		"request started",
	) {

		t.Fatalf(
			"expected request started log, got %q",
			logOutput,
		)
	}

	if !strings.Contains(
		logOutput,
		"request completed",
	) {

		t.Fatalf(
			"expected request completed log, got %q",
			logOutput,
		)
	}
}
