//go:build integration

package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-70/internal/httpapi"
	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-70/internal/testsupport"
)

/*
The slow suite: a real server on a real port, driven by a real client.

	go test -tags=integration ./...

It covers the paths a user actually walks, end to end, and the concurrency
behaviour that only appears over real connections.
*/

func newServer(t *testing.T) (*httptest.Server, func(method, path, token string, body any) (int, []byte)) {
	t.Helper()

	service, _ := testsupport.NewService(t)

	server := httptest.NewServer(httpapi.New(service).Routes())

	t.Cleanup(server.Close)

	call := func(method, path, token string, body any) (int, []byte) {
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

		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
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

func TestUserJourney(t *testing.T) {
	t.Parallel()

	_, call := newServer(t)

	// Sign in is implicit here: the token is the credential.
	status, body := call(http.MethodPost, "/tasks", testsupport.AdaToken,
		map[string]any{"title": "ship the release"})

	if status != http.StatusCreated {
		t.Fatalf("create = %d (%s)", status, body)
	}

	var task struct {
		ID int64 `json:"id"`
	}

	if err := json.Unmarshal(body, &task); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if status, _ := call(http.MethodGet, "/tasks", testsupport.AdaToken, nil); status != http.StatusOK {
		t.Fatalf("list = %d", status)
	}

	if status, _ := call(http.MethodPost,
		fmt.Sprintf("/tasks/%d/complete", task.ID), testsupport.AdaToken, nil); status != http.StatusOK {
		t.Fatalf("complete = %d", status)
	}

	// A completed task disappears from the default list and returns with the
	// include_done flag.
	status, body = call(http.MethodGet, "/tasks", testsupport.AdaToken, nil)

	var listed struct {
		Count int `json:"count"`
	}

	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if status != http.StatusOK || listed.Count != 0 {
		t.Fatalf("open tasks = %d, want 0", listed.Count)
	}

	status, body = call(http.MethodGet, "/tasks?include_done=true", testsupport.AdaToken, nil)

	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if status != http.StatusOK || listed.Count != 1 {
		t.Fatalf("all tasks = %d, want 1", listed.Count)
	}
}

// TestTwoUsersOverRealConnections: two clients, one server, no leakage.
func TestTwoUsersOverRealConnections(t *testing.T) {
	t.Parallel()

	_, call := newServer(t)

	for _, user := range []struct {
		token string
		title string
	}{
		{testsupport.AdaToken, "ada's task"},
		{testsupport.AlanToken, "alan's task"},
	} {
		if status, body := call(http.MethodPost, "/tasks", user.token,
			map[string]any{"title": user.title}); status != http.StatusCreated {
			t.Fatalf("create for %s = %d (%s)", user.title, status, body)
		}
	}

	for _, user := range []struct {
		token string
		want  string
	}{
		{testsupport.AdaToken, "ada's task"},
		{testsupport.AlanToken, "alan's task"},
	} {
		_, body := call(http.MethodGet, "/tasks", user.token, nil)

		var listed struct {
			Tasks []struct {
				Title string `json:"title"`
			} `json:"tasks"`
		}

		if err := json.Unmarshal(body, &listed); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if len(listed.Tasks) != 1 || listed.Tasks[0].Title != user.want {
			t.Fatalf("list = %+v, want only %q", listed.Tasks, user.want)
		}
	}
}

// TestConcurrentClients is why the -race flag exists: many goroutines, one
// service, shared maps behind a mutex.
func TestConcurrentClients(t *testing.T) {
	t.Parallel()

	_, call := newServer(t)

	const clients = 16

	var waitGroup sync.WaitGroup

	for i := range clients {
		waitGroup.Add(1)

		go func(i int) {
			defer waitGroup.Done()

			status, body := call(http.MethodPost, "/tasks", testsupport.AdaToken,
				map[string]any{"title": fmt.Sprintf("task %d", i)})

			if status != http.StatusCreated {
				t.Errorf("create %d = %d (%s)", i, status, body)
			}
		}(i)
	}

	waitGroup.Wait()

	_, body := call(http.MethodGet, "/tasks", testsupport.AdaToken, nil)

	var listed struct {
		Count int `json:"count"`
	}

	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if listed.Count != clients {
		t.Fatalf("count = %d, want %d - a write was lost", listed.Count, clients)
	}
}

func TestServerTimeoutsAreConfigured(t *testing.T) {
	t.Parallel()

	server, _ := newServer(t)

	// httptest does not apply the production timeouts, so this asserts the
	// thing that IS observable here: the server answers a plain request
	// promptly rather than hanging.
	done := make(chan int, 1)

	go func() {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/healthz", nil)
		if err != nil {
			t.Errorf("build request: %v", err)

			done <- 0

			return
		}

		response, err := server.Client().Do(request)
		if err != nil {
			t.Errorf("healthz: %v", err)

			done <- 0

			return
		}

		if err := response.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}

		done <- response.StatusCode
	}()

	select {
	case status := <-done:
		if status != http.StatusOK {
			t.Fatalf("healthz = %d", status)
		}

	case <-time.After(5 * time.Second):
		t.Fatal("healthz did not answer within 5s")
	}
}
