package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-56/internal/repository"
	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-56/internal/service"
)

// The whole stack, wired the same way main wires it - minus the network.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	handler := NewHandler(service.NewTaskService(repository.NewMemoryTaskRepository(), service.SystemClock{}))

	server := httptest.NewServer(handler.Routes())

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

func TestTaskLifecycle(t *testing.T) {
	t.Parallel()

	server := newTestServer(t)

	status, body := call(t, server, http.MethodPost, "/tasks", map[string]string{
		"reference": "task-1",
		"title":     "Write the layout README",
		"assignee":  "ada",
		"due_at":    time.Now().Add(48 * time.Hour).Format(time.RFC3339),
	})

	if status != http.StatusCreated {
		t.Fatalf("create = %d (%s)", status, body)
	}

	var task taskResponse

	if err := json.Unmarshal(body, &task); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if task.Reference != "TASK-1" || task.Status != "todo" {
		t.Fatalf("task = %+v", task)
	}

	// The workflow rule lives in the service, and the handler reports it as
	// 422 - not as a 500 and not as a silent success.
	if status, _ := call(t, server, http.MethodPost, "/tasks/1/status",
		map[string]string{"status": "done"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("todo -> done = %d, want 422", status)
	}

	if status, body := call(t, server, http.MethodPost, "/tasks/1/status",
		map[string]string{"status": "doing"}); status != http.StatusOK {
		t.Fatalf("todo -> doing = %d (%s)", status, body)
	}

	if status, _ := call(t, server, http.MethodGet, "/tasks?status=doing", nil); status != http.StatusOK {
		t.Fatalf("list = %d", status)
	}

	if status, _ := call(t, server, http.MethodDelete, "/tasks/1", nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}

	if status, _ := call(t, server, http.MethodGet, "/tasks/1", nil); status != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", status)
	}
}

func TestErrorMapping(t *testing.T) {
	t.Parallel()

	server := newTestServer(t)

	if status, body := call(t, server, http.MethodPost, "/tasks",
		map[string]string{"reference": "dup", "title": "first"}); status != http.StatusCreated {
		t.Fatalf("seed = %d (%s)", status, body)
	}

	tests := []struct {
		name string
		body map[string]string
		want int
	}{
		{"duplicate reference", map[string]string{"reference": "DUP", "title": "second"}, http.StatusConflict},
		{"missing title", map[string]string{"reference": "x1"}, http.StatusUnprocessableEntity},
		{"missing reference", map[string]string{"title": "no reference"}, http.StatusUnprocessableEntity},
		{"bad due date format", map[string]string{"reference": "x2", "title": "t", "due_at": "tomorrow"}, http.StatusBadRequest},
		{"due date in the past", map[string]string{"reference": "x3", "title": "t", "due_at": "2000-01-01T00:00:00Z"}, http.StatusUnprocessableEntity},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, body := call(t, server, http.MethodPost, "/tasks", test.body)

			if status != test.want {
				t.Fatalf("status = %d, want %d (%s)", status, test.want, body)
			}
		})
	}

	if status, _ := call(t, server, http.MethodGet, "/tasks/9999", nil); status != http.StatusNotFound {
		t.Fatal("missing task did not return 404")
	}
}
