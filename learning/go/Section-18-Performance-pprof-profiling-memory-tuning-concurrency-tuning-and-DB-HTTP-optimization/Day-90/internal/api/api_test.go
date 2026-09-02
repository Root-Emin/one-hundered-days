package api_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-90/internal/api"
	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-90/internal/store"
)

func newServer(t testing.TB, latency time.Duration) (*httptest.Server, *api.API) {
	t.Helper()

	handle, err := sql.Open("sqlite",
		"file:"+filepath.Join(t.TempDir(), "test.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() {
		if err := handle.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	dataStore := store.New(handle, latency)

	if err := dataStore.Exec(t.Context(), store.Schema); err != nil {
		t.Fatalf("schema: %v", err)
	}

	if err := dataStore.Exec(t.Context(), store.IndexSQL); err != nil {
		t.Fatalf("index: %v", err)
	}

	if err := store.Seed(t.Context(), dataStore, 90, 6); err != nil {
		t.Fatalf("seed: %v", err)
	}

	service := api.New(dataStore, slog.New(slog.NewTextHandler(io.Discard, nil)))

	server := httptest.NewServer(service.Routes())

	t.Cleanup(server.Close)

	return server, service
}

func get(t testing.TB, server *httptest.Server, path string) ([]store.Row, *http.Response) {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+path, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}

	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
	}()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)

		t.Fatalf("GET %s = %d: %s", path, response.StatusCode, body)
	}

	var rows []store.Row

	if err := json.NewDecoder(response.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}

	return rows, response
}

// The optimization is only valid if every version returns the same page.
// "Faster" and "wrong" are indistinguishable without this.
func TestAllVersionsReturnTheSamePage(t *testing.T) {
	server, _ := newServer(t, 0)

	baseline, _ := get(t, server, "/dashboard?v=1&limit=20&tier=pro")

	if len(baseline) == 0 {
		t.Fatal("the fixture produced an empty page")
	}

	for version := 2; version <= 4; version++ {
		rows, _ := get(t, server, fmt.Sprintf("/dashboard?v=%d&limit=20&tier=pro", version))

		if len(rows) != len(baseline) {
			t.Fatalf("v%d returned %d rows, want %d", version, len(rows), len(baseline))
		}

		for i := range baseline {
			if rows[i].Customer.ID != baseline[i].Customer.ID {
				t.Fatalf("v%d row %d: customer %d, want %d",
					version, i, rows[i].Customer.ID, baseline[i].Customer.ID)
			}

			if rows[i].TotalCent != baseline[i].TotalCent {
				t.Errorf("v%d customer %d: total %d, want %d",
					version, rows[i].Customer.ID, rows[i].TotalCent, baseline[i].TotalCent)
			}

			if len(rows[i].Orders) != len(baseline[i].Orders) {
				t.Errorf("v%d customer %d: %d orders, want %d",
					version, rows[i].Customer.ID, len(rows[i].Orders), len(baseline[i].Orders))
			}
		}
	}
}

// The regression guard with the best signal-to-noise ratio on the whole page:
// the query count is exact. It does not vary with the machine, the load or the
// scheduler, so it can be asserted tightly - and an N+1 sneaking back fails
// here, in CI, in milliseconds.
func TestHotPathIssuesOneQueryPerRequest(t *testing.T) {
	server, service := newServer(t, 0)

	for _, testCase := range []struct {
		version api.Version
		want    float64
	}{
		{api.V3, 1},
		{api.V4, 1},
	} {
		t.Run(testCase.version.String(), func(t *testing.T) {
			service.Reset()

			for i := 0; i < 5; i++ {
				get(t, server, fmt.Sprintf("/dashboard?v=%d&limit=20&tier=pro", testCase.version))
			}

			samples := service.Stats(testCase.version)

			if samples.Requests != 5 {
				t.Fatalf("requests = %d, want 5", samples.Requests)
			}

			perRequest := float64(samples.Queries) / float64(samples.Requests)

			if perRequest != testCase.want {
				t.Errorf("%.1f queries per request, want %.0f", perRequest, testCase.want)
			}
		})
	}
}

// And the counter-assertion: the old version really did issue limit+1 queries,
// so the guard above is testing something real.
func TestBaselineIssuesNPlusOneQueries(t *testing.T) {
	server, service := newServer(t, 0)

	service.Reset()

	rows, _ := get(t, server, "/dashboard?v=1&limit=20&tier=pro")

	samples := service.Stats(api.V1)

	if samples.Queries != len(rows)+1 {
		t.Errorf("queries = %d for %d rows, want %d", samples.Queries, len(rows), len(rows)+1)
	}
}

func TestInvalidParametersAreRejected(t *testing.T) {
	server, _ := newServer(t, 0)

	for _, path := range []string{
		"/dashboard?v=9",
		"/dashboard?v=abc",
		"/dashboard?limit=0",
		"/dashboard?limit=100000",
	} {
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

		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, response.StatusCode)
		}
	}
}

// A pooled response buffer must never leak one response into the next.
func TestPooledResponseBufferIsReset(t *testing.T) {
	server, _ := newServer(t, 0)

	first, _ := get(t, server, "/dashboard?v=4&limit=20&tier=pro")

	for i := 0; i < 20; i++ {
		rows, _ := get(t, server, "/dashboard?v=4&limit=3&tier=free")

		if len(rows) > 3 {
			t.Fatalf("iteration %d returned %d rows for limit=3 - the buffer leaked", i, len(rows))
		}
	}

	if len(first) == 0 {
		t.Fatal("the first response was empty")
	}
}

//
// BENCHMARKS - the resolution a load test does not have
//

func benchmarkVersion(b *testing.B, version api.Version) {
	server, _ := newServer(b, 0)

	url := fmt.Sprintf("%s/dashboard?v=%d&limit=20&tier=pro", server.URL, version)

	client := server.Client()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		request, err := http.NewRequestWithContext(b.Context(), http.MethodGet, url, nil)
		if err != nil {
			b.Fatal(err)
		}

		response, err := client.Do(request)
		if err != nil {
			b.Fatal(err)
		}

		if _, err := io.Copy(io.Discard, response.Body); err != nil {
			b.Fatal(err)
		}

		if err := response.Body.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDashboardV1(b *testing.B) { benchmarkVersion(b, api.V1) }
func BenchmarkDashboardV3(b *testing.B) { benchmarkVersion(b, api.V3) }
func BenchmarkDashboardV4(b *testing.B) { benchmarkVersion(b, api.V4) }
