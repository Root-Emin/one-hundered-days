package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-98/internal/metrics"
)

// The cardinality guard. Without it, a million short links would create a
// million series and the monitoring system falls over before the service does.
func TestRouteTemplateBoundsCardinality(t *testing.T) {
	cases := map[string]string{
		"/":                       "/",
		"/healthz":                "/healthz",
		"/readyz":                 "/readyz",
		"/metrics":                "/metrics",
		"/api/links":              "/api/links",
		"/api/links/golang":       "/api/links/{code}",
		"/api/links/golang/stats": "/api/links/{code}",
		"/abc1234":                "/{code}",
		"/xyz9876":                "/{code}",
	}

	for path, want := range cases {
		if got := metrics.RouteTemplate(path); got != want {
			t.Errorf("RouteTemplate(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestMiddlewareRecordsRequests(t *testing.T) {
	m := metrics.New()

	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/boom" {
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		w.WriteHeader(http.StatusFound)
	}))

	for _, path := range []string{"/abc1234", "/xyz9876", "/boom"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	body := scrape(t, m)

	// Two different codes share ONE series.
	if !strings.Contains(body, `linkr_http_requests_total{class="3xx",method="GET",route="/{code}",status="302"} 2`) {
		t.Errorf("the two redirects were not aggregated into one series:\n%s", body)
	}

	if !strings.Contains(body, `class="5xx"`) {
		t.Error("the 500 was not recorded with its class")
	}

	// The status class is what an alert uses: "5xx rate above 1%" is a rule
	// you write once.
	if strings.Contains(body, "abc1234") || strings.Contains(body, "xyz9876") {
		t.Error("a link code leaked into a label")
	}
}

// A dashboard querying a metric that has never been incremented shows "no
// data", which looks the same as "the service is down".
func TestSeriesAreInitialisedAtZero(t *testing.T) {
	body := scrape(t, metrics.New())

	for _, want := range []string{
		`linkr_redirects_total{outcome="found"} 0`,
		`linkr_redirects_total{outcome="gone"} 0`,
		`linkr_redirect_cache_total{result="hit"} 0`,
		`linkr_redirect_cache_total{result="miss"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%q is missing from a fresh registry", want)
		}
	}
}

func TestRedirectAndCacheCounters(t *testing.T) {
	m := metrics.New()

	m.RecordRedirect("found")
	m.RecordRedirect("found")
	m.RecordRedirect("gone")
	m.RecordCacheLookup("hit")
	m.RecordClickDropped()
	m.SetOutboxPending(42)

	body := scrape(t, m)

	for _, want := range []string{
		`linkr_redirects_total{outcome="found"} 2`,
		`linkr_redirects_total{outcome="gone"} 1`,
		`linkr_redirect_cache_total{result="hit"} 1`,
		"linkr_clicks_dropped_total 1",
		"linkr_outbox_pending 42",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%q missing:\n%s", want, body)
		}
	}
}

// The SLO is a p95 under 5ms, so the histogram needs buckets below it - the
// Prometheus defaults start at 5ms and would make it unmeasurable.
func TestDurationBucketsCoverTheSLO(t *testing.T) {
	m := metrics.New()

	m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/abc1234", nil))

	body := scrape(t, m)

	for _, bucket := range []string{"0.0005", "0.001", "0.0025"} {
		if !strings.Contains(body, "le=\""+bucket+"\"") {
			t.Errorf("no %s bucket: a sub-millisecond p95 would be unmeasurable", bucket)
		}
	}
}

func scrape(t *testing.T, m *metrics.Metrics) string {
	t.Helper()

	recorder := httptest.NewRecorder()

	m.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("scrape status = %d", recorder.Code)
	}

	return recorder.Body.String()
}
