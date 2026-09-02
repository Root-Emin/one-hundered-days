package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-60/internal/service"
	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-60/internal/storage/sqlite"
)

// End to end through the real stack: HTTP -> service -> SQLite. The wiring is
// the same three lines as cmd/api, which is the point of a thin main.

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	db, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	repository := sqlite.New(db)
	library := service.NewLibrary(repository, repository, service.SystemClock{})

	server := httptest.NewServer(New(library).Routes())

	t.Cleanup(server.Close)

	return server
}

func call(t *testing.T, server *httptest.Server, method, path string, body any) (int, []byte) {
	t.Helper()

	payload := bytes.NewReader(nil)

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, payload)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
	}()

	var buffer bytes.Buffer

	if _, err := buffer.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode, buffer.Bytes()
}

func TestReadingFlowOverHTTP(t *testing.T) {
	t.Parallel()

	server := newTestServer(t)

	status, body := call(t, server, http.MethodPost, "/books", map[string]any{
		"isbn": "978-0-13-419044-0", "title": "The Go Programming Language",
		"author": "Donovan", "pages": 380,
	})

	if status != http.StatusCreated {
		t.Fatalf("create = %d (%s)", status, body)
	}

	var book bookResponse

	if err := json.Unmarshal(body, &book); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if book.Status != "wishlist" || book.ISBN != "9780134190440" {
		t.Fatalf("book = %+v", book)
	}

	if status, body := call(t, server, http.MethodPost, "/books/1/start", nil); status != http.StatusOK {
		t.Fatalf("start = %d (%s)", status, body)
	}

	status, body = call(t, server, http.MethodPost, "/books/1/progress", map[string]any{"page": 190})

	if status != http.StatusOK {
		t.Fatalf("progress = %d (%s)", status, body)
	}

	if err := json.Unmarshal(body, &book); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if book.PercentRead != 50 {
		t.Fatalf("percent = %d, want 50", book.PercentRead)
	}

	status, body = call(t, server, http.MethodGet, "/stats", nil)

	if status != http.StatusOK {
		t.Fatalf("stats = %d (%s)", status, body)
	}

	var stats struct {
		Total       int `json:"total"`
		Reading     int `json:"reading"`
		PercentRead int `json:"percent_read"`
	}

	if err := json.Unmarshal(body, &stats); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if stats.Total != 1 || stats.Reading != 1 || stats.PercentRead != 50 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestErrorMapping(t *testing.T) {
	t.Parallel()

	server := newTestServer(t)

	if status, _ := call(t, server, http.MethodPost, "/books", map[string]any{
		"isbn": "9780134190440", "title": "Title", "author": "Author", "pages": 100,
	}); status != http.StatusCreated {
		t.Fatal("seed failed")
	}

	tests := []struct {
		name   string
		method string
		path   string
		body   any
		want   int
	}{
		{"short isbn", http.MethodPost, "/books",
			map[string]any{"isbn": "123", "title": "T", "author": "A", "pages": 10},
			http.StatusUnprocessableEntity},
		{"duplicate isbn", http.MethodPost, "/books",
			map[string]any{"isbn": "9780134190440", "title": "T", "author": "A", "pages": 10},
			http.StatusConflict},
		{"unknown field", http.MethodPost, "/books",
			map[string]any{"isbn": "9781617291784", "title": "T", "author": "A", "pages": 10, "rating": 5},
			http.StatusBadRequest},
		{"missing book", http.MethodGet, "/books/999", nil, http.StatusNotFound},
		{"invalid id", http.MethodGet, "/books/abc", nil, http.StatusBadRequest},
		{"progress before start", http.MethodPost, "/books/1/progress",
			map[string]any{"page": 5}, http.StatusConflict},
		{"bad status filter", http.MethodGet, "/books?status=nope", nil, http.StatusUnprocessableEntity},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, body := call(t, server, test.method, test.path, test.body)

			if status != test.want {
				t.Fatalf("status = %d, want %d (%s)", status, test.want, body)
			}
		})
	}
}
