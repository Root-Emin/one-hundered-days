package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/onehundredday/Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-76/internal/api"
)

func newHandler(t *testing.T) (http.Handler, *api.API) {
	t.Helper()

	service := api.New(nil, "test-version")

	return service.Routes(), service
}

func call(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	return recorder
}

func TestHealthAndReadiness(t *testing.T) {
	t.Parallel()

	handler, service := newHandler(t)

	if response := call(t, handler, http.MethodGet, "/healthz", ""); response.Code != http.StatusOK {
		t.Fatalf("healthz = %d", response.Code)
	}

	if response := call(t, handler, http.MethodGet, "/readyz", ""); response.Code != http.StatusOK {
		t.Fatalf("readyz = %d", response.Code)
	}

	// The drain sequence: readiness fails first, so the load balancer stops
	// sending traffic while in-flight requests finish.
	service.NotReady()

	if response := call(t, handler, http.MethodGet, "/readyz", ""); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz during shutdown = %d, want 503", response.Code)
	}

	// Liveness must stay green, or the orchestrator kills the container in the
	// middle of its graceful shutdown.
	if response := call(t, handler, http.MethodGet, "/healthz", ""); response.Code != http.StatusOK {
		t.Fatalf("healthz during shutdown = %d, want 200", response.Code)
	}
}

func TestVersionEndpointReportsBuildInfo(t *testing.T) {
	t.Parallel()

	handler, _ := newHandler(t)

	response := call(t, handler, http.MethodGet, "/version", "")

	var payload struct {
		Version   string `json:"version"`
		GoVersion string `json:"go_version"`
	}

	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// The version comes from -ldflags at build time: without it, "which build
	// is running?" has no answer in production.
	if payload.Version != "test-version" || payload.GoVersion == "" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestNotes(t *testing.T) {
	t.Parallel()

	handler, _ := newHandler(t)

	if response := call(t, handler, http.MethodPost, "/notes", `{"text":"hello"}`); response.Code != http.StatusCreated {
		t.Fatalf("create = %d (%s)", response.Code, response.Body)
	}

	if response := call(t, handler, http.MethodPost, "/notes", `{"text":""}`); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty text = %d, want 422", response.Code)
	}

	if response := call(t, handler, http.MethodPost, "/notes", `not json`); response.Code != http.StatusBadRequest {
		t.Fatalf("bad json = %d, want 400", response.Code)
	}

	response := call(t, handler, http.MethodGet, "/notes", "")

	var listed struct {
		Count int `json:"count"`
	}

	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if listed.Count != 1 {
		t.Fatalf("count = %d, want 1", listed.Count)
	}
}
