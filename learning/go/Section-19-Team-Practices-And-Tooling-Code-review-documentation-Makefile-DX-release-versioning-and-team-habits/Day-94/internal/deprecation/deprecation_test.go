package deprecation_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-94/internal/deprecation"
)

func scan(t *testing.T) []deprecation.Notice {
	t.Helper()

	notices, err := deprecation.Scan(filepath.Join("..", "legacy"))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	return notices
}

func TestScanFindsEveryMarker(t *testing.T) {
	notices := scan(t)

	if len(notices) != 4 {
		t.Fatalf("notices = %d, want 4: %+v", len(notices), notices)
	}

	byName := make(map[string]deprecation.Notice, len(notices))

	for _, notice := range notices {
		byName[notice.Symbol] = notice
	}

	// The one that is done properly.
	good := byName["FindProduct"]

	if good.Replacement == "" {
		t.Error("FindProduct: no replacement parsed")
	}

	if !good.HasDate || good.RemoveAfter.Format("2006-01-02") != "2026-12-01" {
		t.Errorf("FindProduct: removal date = %+v", good)
	}

	// The ones that are not.
	if byName["LookupProduct"].HasDate {
		t.Error("LookupProduct should have no removal date")
	}

	if byName["MaxResults"].Replacement != "" {
		t.Errorf("MaxResults should name no replacement, got %q", byName["MaxResults"].Replacement)
	}
}

func TestCheckReportsIncompletePromises(t *testing.T) {
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)

	issues := deprecation.Check(scan(t), now)

	rules := make(map[string]int)

	for _, issue := range issues {
		rules[issue.Rule]++
	}

	for rule, want := range map[string]int{
		"no_replacement":  3, // LookupProduct, ProductName, MaxResults
		"no_removal_date": 2, // LookupProduct, MaxResults
		"overdue":         1, // ProductName, dated 2020
	} {
		if rules[rule] != want {
			t.Errorf("%s = %d, want %d (issues: %v)", rule, rules[rule], want, issues)
		}
	}
}

// The properly written deprecation must not be reported, or the lint is noise.
func TestCompleteDeprecationIsNotReported(t *testing.T) {
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)

	for _, issue := range deprecation.Check(scan(t), now) {
		if issue.Symbol == "FindProduct" {
			t.Errorf("a complete deprecation was reported: %s", issue)
		}
	}
}

// Before its date, a dated deprecation is fine; after it, it needs a decision.
func TestOverdueDependsOnTheDate(t *testing.T) {
	notices := []deprecation.Notice{{
		Symbol:      "Thing",
		Replacement: "NewThing",
		HasDate:     true,
		RemoveAfter: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
	}}

	before := deprecation.Check(notices, time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC))

	if len(before) != 0 {
		t.Errorf("reported before the removal date: %v", before)
	}

	after := deprecation.Check(notices, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))

	if len(after) != 1 || after[0].Rule != "overdue" {
		t.Errorf("after the date = %v, want one overdue issue", after)
	}
}

//
// THE RUNTIME SIDE
//

func TestMiddlewareSetsRFC8594Headers(t *testing.T) {
	policy := deprecation.Policy{
		Endpoint:    "GET /product",
		Replacement: "https://api.example.com/products/{sku}",
		SunsetAt:    time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		Docs:        "https://docs.example.com/migrate",
	}

	handler := policy.Middleware(quiet(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("do: %v", err)
	}

	t.Cleanup(func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	if response.Header.Get("Deprecation") != "true" {
		t.Error("no Deprecation header")
	}

	if sunset := response.Header.Get("Sunset"); !strings.Contains(sunset, "2026") {
		t.Errorf("Sunset = %q", sunset)
	}

	links := strings.Join(response.Header.Values("Link"), " ")

	for _, want := range []string{"successor-version", "rel=\"deprecation\""} {
		if !strings.Contains(links, want) {
			t.Errorf("Link headers %q are missing %q", links, want)
		}
	}
}

// The log fires once per process. Under load, a line per request costs money
// and gets the warning filtered out.
func TestMiddlewareLogsOnlyOnce(t *testing.T) {
	var recorded strings.Builder

	logger := slog.New(slog.NewTextHandler(&recorded, &slog.HandlerOptions{Level: slog.LevelWarn}))

	policy := deprecation.Policy{Endpoint: "GET /product", Replacement: "/products/{sku}"}

	handler := policy.Middleware(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	for i := 0; i < 5; i++ {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatalf("request: %v", err)
		}

		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatalf("do: %v", err)
		}

		if err := response.Body.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}

	if count := strings.Count(recorded.String(), "deprecated endpoint in use"); count != 1 {
		t.Errorf("logged %d times for 5 requests, want 1", count)
	}
}

func TestExpiredAndTimeline(t *testing.T) {
	policy := deprecation.Policy{
		Endpoint:    "GET /product",
		Replacement: "/products/{sku}",
		SunsetAt:    time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		Docs:        "https://docs.example.com/migrate",
	}

	if policy.Expired(time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)) {
		t.Error("reported as expired before its sunset")
	}

	if !policy.Expired(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Error("not reported as expired after its sunset")
	}

	timeline := policy.Timeline()

	for _, want := range []string{"GET /product", "/products/{sku}", "2026-12-01", "docs.example.com"} {
		if !strings.Contains(timeline, want) {
			t.Errorf("timeline %q is missing %q", timeline, want)
		}
	}
}

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
