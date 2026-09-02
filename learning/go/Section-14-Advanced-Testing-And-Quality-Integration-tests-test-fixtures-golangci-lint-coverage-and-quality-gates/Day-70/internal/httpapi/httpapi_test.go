package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-70/internal/httpapi"
	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-70/internal/testsupport"
	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-70/internal/todo"
)

// Auth and CRUD over HTTP. These are the tests that would have caught every
// production incident this service could plausibly have.

type harness struct {
	handler http.Handler
	service *todo.Service
	clock   *testsupport.FixedClock
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	service, clock := testsupport.NewService(t)

	return &harness{handler: httpapi.New(service).Routes(), service: service, clock: clock}
}

func (h *harness) call(t *testing.T, method, path, token string, body any) (int, []byte) {
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

	request := httptest.NewRequestWithContext(t.Context(), method, path, reader)

	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	recorder := httptest.NewRecorder()

	h.handler.ServeHTTP(recorder, request)

	return recorder.Code, recorder.Body.Bytes()
}

//
// AUTH
//

func TestAuthenticationIsRequired(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	protected := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/tasks"},
		{http.MethodGet, "/tasks"},
		{http.MethodGet, "/tasks/1"},
		{http.MethodGet, "/tasks/overdue"},
		{http.MethodPost, "/tasks/1/complete"},
		{http.MethodDelete, "/tasks/1"},
	}

	for _, route := range protected {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			status, _ := h.call(t, route.method, route.path, "", map[string]string{"title": "x"})

			if status != http.StatusUnauthorized {
				t.Fatalf("without a token: status = %d, want 401", status)
			}

			status, _ = h.call(t, route.method, route.path, "made-up-token", map[string]string{"title": "x"})

			if status != http.StatusUnauthorized {
				t.Fatalf("with a bad token: status = %d, want 401", status)
			}
		})
	}

	// The health check must stay public, or the load balancer cannot use it.
	if status, _ := h.call(t, http.MethodGet, "/healthz", "", nil); status != http.StatusOK {
		t.Fatalf("healthz = %d, want 200 without a token", status)
	}
}

func TestMalformedAuthorizationHeader(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	for name, header := range map[string]string{
		"no scheme":    testsupport.AdaToken,
		"wrong scheme": "Basic " + testsupport.AdaToken,
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/tasks", nil)
			request.Header.Set("Authorization", header)

			recorder := httptest.NewRecorder()

			h.handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", recorder.Code)
			}
		})
	}
}

//
// CRUD
//

func TestCRUDLifecycle(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	status, body := h.call(t, http.MethodPost, "/tasks", testsupport.AdaToken,
		map[string]any{
			"title":  "write the README",
			"due_at": testsupport.Reference.Add(24 * time.Hour).Format(time.RFC3339),
		})

	if status != http.StatusCreated {
		t.Fatalf("create = %d (%s)", status, body)
	}

	var created struct {
		ID    int64  `json:"id"`
		Owner string `json:"owner"`
		Done  bool   `json:"done"`
	}

	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if created.Owner != "ada" {
		t.Fatalf("owner = %q, want it taken from the token", created.Owner)
	}

	path := fmt.Sprintf("/tasks/%d", created.ID)

	getStatus, getBody := h.call(t, http.MethodGet, path, testsupport.AdaToken, nil)

	if getStatus != http.StatusOK {
		t.Fatalf("get = %d (%s)", getStatus, getBody)
	}

	status, body = h.call(t, http.MethodPost, path+"/complete", testsupport.AdaToken, nil)

	if status != http.StatusOK {
		t.Fatalf("complete = %d (%s)", status, body)
	}

	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !created.Done {
		t.Fatal("task was not marked done")
	}

	if status, _ := h.call(t, http.MethodDelete, path, testsupport.AdaToken, nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}

	if status, _ := h.call(t, http.MethodGet, path, testsupport.AdaToken, nil); status != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", status)
	}
}

func TestOwnershipOverHTTP(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	tasks := testsupport.SeedTasks(t, h.service)

	alansTask := fmt.Sprintf("/tasks/%d", tasks[2].ID)

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		status, _ := h.call(t, method, alansTask, testsupport.AdaToken, nil)

		if status != http.StatusForbidden {
			t.Fatalf("%s another user's task: status = %d, want 403", method, status)
		}
	}

	// And a list only ever shows the caller's own tasks.
	status, body := h.call(t, http.MethodGet, "/tasks", testsupport.AdaToken, nil)

	if status != http.StatusOK {
		t.Fatalf("list = %d (%s)", status, body)
	}

	var listed struct {
		Tasks []struct {
			Owner string `json:"owner"`
		} `json:"tasks"`
	}

	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, task := range listed.Tasks {
		if task.Owner != "ada" {
			t.Fatalf("ada's list contains a task owned by %q", task.Owner)
		}
	}
}

func TestValidationOverHTTP(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	tests := []struct {
		name string
		body any
		want int
	}{
		{"empty title", map[string]any{"title": "  "}, http.StatusUnprocessableEntity},
		{"past due date", map[string]any{
			"title":  "late",
			"due_at": testsupport.Reference.Add(-time.Hour).Format(time.RFC3339),
		}, http.StatusUnprocessableEntity},
		{"bad date format", map[string]any{"title": "x", "due_at": "tomorrow"}, http.StatusBadRequest},
		{"unknown field", map[string]any{"title": "x", "priority": "high"}, http.StatusBadRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, body := h.call(t, http.MethodPost, "/tasks", testsupport.AdaToken, test.body)

			if status != test.want {
				t.Fatalf("status = %d, want %d (%s)", status, test.want, body)
			}
		})
	}

	if status, _ := h.call(t, http.MethodGet, "/tasks/abc", testsupport.AdaToken, nil); status != http.StatusBadRequest {
		t.Fatalf("invalid id: status = %d, want 400", status)
	}
}
