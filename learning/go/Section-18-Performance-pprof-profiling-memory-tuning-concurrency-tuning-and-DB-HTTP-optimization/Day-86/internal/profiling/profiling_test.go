package profiling_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-86/internal/profiling"
)

// The endpoints have to be registered explicitly, because we deliberately do
// not import net/http/pprof for its side effect on DefaultServeMux.
func TestDebugMuxServesTheProfileEndpoints(t *testing.T) {
	server := httptest.NewServer(profiling.DebugMux())
	t.Cleanup(server.Close)

	paths := []string{
		"/debug/pprof/",
		"/debug/pprof/heap",
		"/debug/pprof/goroutine",
		"/debug/pprof/allocs",
		"/debug/pprof/block",
		"/debug/pprof/mutex",
		"/debug/pprof/cmdline",
	}

	for _, path := range paths {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}

		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}

		if err := response.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}

		if response.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, response.StatusCode)
		}
	}
}

// A pprof file starts with a gzip magic number; anything else means we wrote
// an error page into a .pprof and 'go tool pprof' will fail later with
// something far less obvious.
func requireProfileFile(t *testing.T, path string) {
	t.Helper()

	content, err := os.ReadFile(path) //nolint:gosec // path is built by the test
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	if len(content) < 2 || content[0] != 0x1f || content[1] != 0x8b {
		t.Fatalf("%s is not a gzipped pprof profile (%d bytes)", path, len(content))
	}
}

func TestCaptureCPUWritesAProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cpu.pprof")

	ran := false

	err := profiling.CaptureCPU(path, func() {
		ran = true

		// Something for the sampler to see. Without work, a short profile can
		// legitimately contain zero samples.
		sum := 0

		for i := 0; i < 5_000_000; i++ {
			sum += i % 7
		}

		if sum == 0 {
			t.Error("the compiler optimised the workload away")
		}
	})
	if err != nil {
		t.Fatalf("capture cpu: %v", err)
	}

	if !ran {
		t.Fatal("the work function never ran")
	}

	requireProfileFile(t, path)
}

func TestCaptureHeapWritesAProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "heap.pprof")

	// Retain something so the profile is not empty.
	retained := make([][]byte, 0, 64)

	for i := 0; i < 64; i++ {
		retained = append(retained, make([]byte, 4096))
	}

	if err := profiling.CaptureHeap(path); err != nil {
		t.Fatalf("capture heap: %v", err)
	}

	if len(retained) != 64 {
		t.Fatal("retained slice was clobbered")
	}

	requireProfileFile(t, path)
}

func TestFetchProfileDownloadsFromARunningServer(t *testing.T) {
	debug, err := profiling.StartDebugServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("start debug server: %v", err)
	}

	t.Cleanup(func() {
		if err := debug.Close(); err != nil {
			t.Errorf("close debug server: %v", err)
		}
	})

	path := filepath.Join(t.TempDir(), "heap.pprof")

	if err := profiling.FetchProfile(t.Context(), debug.URL(), "heap", path, 0); err != nil {
		t.Fatalf("fetch heap: %v", err)
	}

	requireProfileFile(t, path)

	// And it reads back through the real tool.
	report, err := profiling.Top(t.Context(), path, 5, true, "")
	if err != nil {
		t.Fatalf("go tool pprof: %v\n%s", err, report)
	}

	if !strings.Contains(report, "Type: inuse_space") {
		t.Errorf("report does not look like a heap profile:\n%s", report)
	}
}

func TestFetchProfileReportsABadURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cpu.pprof")

	err := profiling.FetchProfile(t.Context(), "http://127.0.0.1:1", "profile", path, 1)
	if err == nil {
		t.Fatal("expected an error fetching from a closed port")
	}
}

func TestIndentPrefixesEveryLine(t *testing.T) {
	got := profiling.Indent("a\nb\n", ">> ")

	if got != ">> a\n>> b" {
		t.Errorf("Indent = %q", got)
	}
}
