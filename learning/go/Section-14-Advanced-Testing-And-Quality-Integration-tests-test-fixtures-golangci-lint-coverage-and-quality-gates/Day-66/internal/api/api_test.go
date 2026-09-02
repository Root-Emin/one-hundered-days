package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-66/internal/api"
	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-66/internal/store"
	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-66/internal/testsupport"
)

/*
The fast suite: no build tag, so it runs on every `go test ./...`.

These are still integration tests in the sense that they exercise handler +
store + database together - the "unit" boundary in a service this size is not
worth mocking. What makes them fast is that the database is an in-process file
and the HTTP layer is an httptest handler rather than a real socket.

The slow, socket-level suite is in integration_test.go behind a build tag.
*/

func newHandler(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()

	bookmarks := testsupport.NewStore(t)

	return api.New(bookmarks).Routes(), bookmarks
}

func request(t *testing.T, handler http.Handler, method, path, owner string, body any) (int, []byte) {
	t.Helper()

	var reader *bytes.Reader

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}

	request := httptest.NewRequest(method, path, reader)

	if owner != "" {
		request.Header.Set("X-Owner", owner)
	}

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	return recorder.Code, recorder.Body.Bytes()
}

func TestCreateAndRead(t *testing.T) {
	t.Parallel()

	handler, _ := newHandler(t)

	status, body := request(t, handler, http.MethodPost, "/bookmarks", "ada",
		map[string]any{"url": "https://go.dev", "title": "Go", "tags": []string{"go"}})

	if status != http.StatusCreated {
		t.Fatalf("create = %d (%s)", status, body)
	}

	var created struct {
		ID   int64    `json:"id"`
		Tags []string `json:"tags"`
	}

	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if created.ID == 0 || len(created.Tags) != 1 {
		t.Fatalf("created = %+v", created)
	}

	if status, body := request(t, handler, http.MethodGet, "/bookmarks/1", "ada", nil); status != http.StatusOK {
		t.Fatalf("get = %d (%s)", status, body)
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()

	handler, _ := newHandler(t)

	tests := []struct {
		name  string
		owner string
		body  any
		want  int
	}{
		{"missing owner header", "", map[string]any{"url": "https://go.dev", "title": "Go"}, http.StatusUnauthorized},
		{"missing url", "ada", map[string]any{"title": "Go"}, http.StatusUnprocessableEntity},
		{"scheme-less url", "ada", map[string]any{"url": "go.dev", "title": "Go"}, http.StatusUnprocessableEntity},
		{"missing title", "ada", map[string]any{"url": "https://go.dev"}, http.StatusUnprocessableEntity},
		{"comma in tag", "ada", map[string]any{"url": "https://go.dev", "title": "Go", "tags": []string{"a,b"}}, http.StatusUnprocessableEntity},
		{"unknown field", "ada", map[string]any{"url": "https://go.dev", "title": "Go", "colour": "red"}, http.StatusBadRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, body := request(t, handler, http.MethodPost, "/bookmarks", test.owner, test.body)

			if status != test.want {
				t.Fatalf("status = %d, want %d (%s)", status, test.want, body)
			}
		})
	}
}

// TestFixturesAreIsolated shows the helper doing its job: each test gets the
// same known state, and one test's rows never appear in another's.
func TestFixturesAreIsolated(t *testing.T) {
	t.Parallel()

	handler, bookmarks := newHandler(t)

	testsupport.Seed(t, bookmarks)

	status, body := request(t, handler, http.MethodGet, "/bookmarks", "ada", nil)

	if status != http.StatusOK {
		t.Fatalf("list = %d (%s)", status, body)
	}

	var listed struct {
		Count int `json:"count"`
	}

	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Two of the three fixtures belong to ada.
	if listed.Count != 2 {
		t.Fatalf("count = %d, want 2", listed.Count)
	}
}

// TestOwnerIsolation is the wiring bug an integration test exists to catch: a
// handler that queries by id and forgets whose row it is.
func TestOwnerIsolation(t *testing.T) {
	t.Parallel()

	handler, bookmarks := newHandler(t)

	created := testsupport.Seed(t, bookmarks)

	alansBookmark := created[2]

	status, _ := request(t, handler, http.MethodGet, "/bookmarks/"+itoa(alansBookmark.ID), "ada", nil)

	if status != http.StatusNotFound {
		t.Fatalf("ada read alan's bookmark: status = %d", status)
	}

	status, _ = request(t, handler, http.MethodDelete, "/bookmarks/"+itoa(alansBookmark.ID), "ada", nil)

	if status != http.StatusNotFound {
		t.Fatalf("ada deleted alan's bookmark: status = %d", status)
	}
}

func TestDuplicateURL(t *testing.T) {
	t.Parallel()

	handler, bookmarks := newHandler(t)

	testsupport.Seed(t, bookmarks)

	// Same owner, same url: the unique index rejects it, and the handler
	// reports 409 rather than 500.
	status, body := request(t, handler, http.MethodPost, "/bookmarks", "ada",
		map[string]any{"url": "https://go.dev", "title": "Go again"})

	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", status, body)
	}

	// A different owner may save the same url.
	if status, _ := request(t, handler, http.MethodPost, "/bookmarks", "alan",
		map[string]any{"url": "https://go.dev", "title": "Go"}); status != http.StatusCreated {
		t.Fatalf("second owner status = %d, want 201", status)
	}
}

func TestTagFilter(t *testing.T) {
	t.Parallel()

	handler, bookmarks := newHandler(t)

	testsupport.Seed(t, bookmarks)

	status, body := request(t, handler, http.MethodGet, "/bookmarks?tag=docs", "ada", nil)

	if status != http.StatusOK {
		t.Fatalf("list = %d (%s)", status, body)
	}

	var listed struct {
		Count int `json:"count"`
	}

	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if listed.Count != 1 {
		t.Fatalf("count = %d, want 1", listed.Count)
	}
}

func itoa(value int64) string {
	if value == 0 {
		return "0"
	}

	digits := ""

	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}

	return digits
}
