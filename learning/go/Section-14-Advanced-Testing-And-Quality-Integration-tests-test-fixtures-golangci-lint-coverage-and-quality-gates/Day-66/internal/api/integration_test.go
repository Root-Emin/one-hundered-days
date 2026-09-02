//go:build integration

// The build tag is the whole point of this file: it is excluded from
//
//	go test ./...
//
// and included by
//
//	go test -tags=integration ./...
//
// so a developer running the fast suite on every save is not waiting for the
// slow one, while CI runs both. The tag must be the first line, followed by a
// blank line, or the compiler ignores it.

package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-66/internal/api"
	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-66/internal/testsupport"
)

/*
The slow suite: a real HTTP server on a real port, driven by a real client.

What this catches that the fast tests cannot:

  - middleware, timeouts and header handling that only exist on a real
    net/http server
  - concurrency behaviour under a real connection pool
  - anything that depends on the request actually being serialized and parsed

What it costs: a socket per test, and a few hundred milliseconds. Worth it for
the critical paths, not for every validation rule - that is the test pyramid.
*/

func newServer(t *testing.T) (*httptest.Server, func(method, path, owner string, body any) (int, []byte)) {
	t.Helper()

	bookmarks := testsupport.NewStore(t)

	server := httptest.NewServer(api.New(bookmarks).Routes())

	t.Cleanup(server.Close)

	testsupport.Seed(t, bookmarks)

	call := func(method, path, owner string, body any) (int, []byte) {
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

		request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, reader)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}

		request.Header.Set("Content-Type", "application/json")

		if owner != "" {
			request.Header.Set("X-Owner", owner)
		}

		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}

		defer func() {
			if err := response.Body.Close(); err != nil {
				t.Errorf("close body: %v", err)
			}
		}()

		var buffer bytes.Buffer

		if _, err := buffer.ReadFrom(response.Body); err != nil {
			t.Fatalf("read body: %v", err)
		}

		return response.StatusCode, buffer.Bytes()
	}

	return server, call
}

func TestFullLifecycleOverHTTP(t *testing.T) {
	t.Parallel()

	_, call := newServer(t)

	status, body := call(http.MethodPost, "/bookmarks", "ada",
		map[string]any{"url": "https://blog.golang.org", "title": "The Go Blog", "tags": []string{"go", "blog"}})

	if status != http.StatusCreated {
		t.Fatalf("create = %d (%s)", status, body)
	}

	var created struct {
		ID int64 `json:"id"`
	}

	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if status, body := call(http.MethodGet, fmt.Sprintf("/bookmarks/%d", created.ID), "ada", nil); status != http.StatusOK {
		t.Fatalf("get = %d (%s)", status, body)
	}

	status, body = call(http.MethodGet, "/bookmarks", "ada", nil)

	if status != http.StatusOK {
		t.Fatalf("list = %d (%s)", status, body)
	}

	var listed struct {
		Count int `json:"count"`
	}

	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Two fixtures plus the one just created.
	if listed.Count != 3 {
		t.Fatalf("count = %d, want 3", listed.Count)
	}

	if status, _ := call(http.MethodDelete, fmt.Sprintf("/bookmarks/%d", created.ID), "ada", nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}

	if status, _ := call(http.MethodGet, fmt.Sprintf("/bookmarks/%d", created.ID), "ada", nil); status != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", status)
	}
}

func TestHealthEndpointTouchesTheDatabase(t *testing.T) {
	t.Parallel()

	_, call := newServer(t)

	status, body := call(http.MethodGet, "/healthz", "", nil)

	if status != http.StatusOK {
		t.Fatalf("healthz = %d (%s)", status, body)
	}
}

// TestConcurrentWritesOverHTTP is the case that needs a real server: many
// clients, one database, and the unique index deciding who wins.
func TestConcurrentWritesOverHTTP(t *testing.T) {
	t.Parallel()

	_, call := newServer(t)

	const attempts = 12

	var (
		waitGroup sync.WaitGroup
		mu        sync.Mutex
		created   int
		conflicts int
	)

	for i := range attempts {
		waitGroup.Add(1)

		go func(i int) {
			defer waitGroup.Done()

			// Half the goroutines race for the same url, half use their own.
			url := "https://example.org/shared"

			if i%2 == 1 {
				url = fmt.Sprintf("https://example.org/unique-%d", i)
			}

			status, _ := call(http.MethodPost, "/bookmarks", "ada",
				map[string]any{"url": url, "title": "Concurrent"})

			mu.Lock()
			defer mu.Unlock()

			switch status {
			case http.StatusCreated:
				created++
			case http.StatusConflict:
				conflicts++
			default:
				t.Errorf("unexpected status %d", status)
			}
		}(i)
	}

	waitGroup.Wait()

	// Exactly one of the racing writers may create the shared url; the six
	// unique ones all succeed.
	if created != attempts/2+1 {
		t.Fatalf("created = %d, want %d", created, attempts/2+1)
	}

	if conflicts != attempts/2-1 {
		t.Fatalf("conflicts = %d, want %d", conflicts, attempts/2-1)
	}
}

// TestSlowClientDoesNotBlockOthers is a smoke test for the server's timeouts;
// it is the kind of case that only makes sense over a real connection.
func TestSlowClientDoesNotBlockOthers(t *testing.T) {
	t.Parallel()

	server, call := newServer(t)

	// Open a connection and leave it idle while another request is served.
	idle, err := server.Client().Transport.(*http.Transport).DialContext(t.Context(), "tcp",
		server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	t.Cleanup(func() {
		if err := idle.Close(); err != nil {
			t.Logf("close idle connection: %v", err)
		}
	})

	done := make(chan int, 1)

	go func() {
		status, _ := call(http.MethodGet, "/bookmarks", "ada", nil)
		done <- status
	}()

	select {
	case status := <-done:
		if status != http.StatusOK {
			t.Fatalf("status = %d", status)
		}

	case <-time.After(5 * time.Second):
		t.Fatal("an idle connection blocked another request")
	}
}
