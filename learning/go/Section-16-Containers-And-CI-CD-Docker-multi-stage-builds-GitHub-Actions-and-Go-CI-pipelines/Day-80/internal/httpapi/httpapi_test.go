package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/onehundredday/Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-80/internal/httpapi"
	"example.com/onehundredday/Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-80/internal/notes"
)

func newHandler(t *testing.T) (http.Handler, *httpapi.Handler) {
	t.Helper()

	handler := httpapi.New(nil, notes.NewService())

	return handler.Routes(), handler
}

func call(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	return recorder
}

func TestNotesLifecycle(t *testing.T) {
	t.Parallel()

	routes, _ := newHandler(t)

	created := call(t, routes, http.MethodPost, "/notes", `{"title":"ship it","body":"today"}`)

	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d (%s)", created.Code, created.Body)
	}

	var note notes.Note

	if err := json.Unmarshal(created.Body.Bytes(), &note); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if response := call(t, routes, http.MethodGet, "/notes/1", ""); response.Code != http.StatusOK {
		t.Fatalf("get = %d", response.Code)
	}

	if response := call(t, routes, http.MethodDelete, "/notes/1", ""); response.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", response.Code)
	}

	if response := call(t, routes, http.MethodGet, "/notes/1", ""); response.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", response.Code)
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()

	routes, _ := newHandler(t)

	tests := map[string]struct {
		body string
		want int
	}{
		"empty title":   {`{"title":"  "}`, http.StatusUnprocessableEntity},
		"bad json":      {`{`, http.StatusBadRequest},
		"unknown field": {`{"title":"x","secret":"y"}`, http.StatusBadRequest},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if response := call(t, routes, http.MethodPost, "/notes", test.body); response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
		})
	}
}

// TestDrainSequence is the deploy-safety test: readiness fails first so the
// load balancer stops routing, while liveness stays green so the orchestrator
// does not kill the container mid-drain.
func TestDrainSequence(t *testing.T) {
	t.Parallel()

	routes, handler := newHandler(t)

	if response := call(t, routes, http.MethodGet, "/readyz", ""); response.Code != http.StatusOK {
		t.Fatalf("readyz before drain = %d", response.Code)
	}

	handler.Drain()

	if response := call(t, routes, http.MethodGet, "/readyz", ""); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz while draining = %d, want 503", response.Code)
	}

	if response := call(t, routes, http.MethodGet, "/healthz", ""); response.Code != http.StatusOK {
		t.Fatalf("healthz while draining = %d, want 200", response.Code)
	}

	// The service must keep serving real traffic during the drain window.
	if response := call(t, routes, http.MethodPost, "/notes", `{"title":"in flight"}`); response.Code != http.StatusCreated {
		t.Fatalf("request during drain = %d, want it served", response.Code)
	}
}

func TestVersionAndMetrics(t *testing.T) {
	t.Parallel()

	routes, _ := newHandler(t)

	version := call(t, routes, http.MethodGet, "/version", "")

	var info struct {
		Version   string `json:"version"`
		GoVersion string `json:"go_version"`
		Platform  string `json:"platform"`
	}

	if err := json.Unmarshal(version.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if info.Version == "" || info.GoVersion == "" || info.Platform == "" {
		t.Fatalf("version payload = %+v", info)
	}

	metrics := call(t, routes, http.MethodGet, "/metrics", "")

	body := metrics.Body.String()

	// app_build_info is what a dashboard uses to show which version each
	// instance is running during a rolling deploy.
	for _, expected := range []string{
		"app_requests_total",
		"app_errors_total",
		"app_notes_stored",
		`app_build_info{version=`,
		"# TYPE app_requests_total counter",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("/metrics is missing %q", expected)
		}
	}
}
