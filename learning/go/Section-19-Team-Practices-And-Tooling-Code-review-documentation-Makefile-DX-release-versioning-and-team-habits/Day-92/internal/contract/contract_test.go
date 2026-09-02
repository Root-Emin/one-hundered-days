package contract_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-92/internal/catalog"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-92/internal/contract"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-92/internal/httpapi"
)

// specPath finds api/openapi.yaml whether the test runs from the package
// directory (go test ./...) or the module root.
func specPath(t *testing.T) string {
	t.Helper()

	path := filepath.Join("..", "..", "api", "openapi.yaml")

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("openapi.yaml not found at %s: %v", path, err)
	}

	return path
}

func endpoints() []contract.Endpoint {
	api := httpapi.New(catalog.New(), nil)

	routes := api.Routes()

	result := make([]contract.Endpoint, 0, len(routes))

	for _, route := range routes {
		result = append(result, contract.Endpoint{
			Method:   route.Method,
			Pattern:  route.Pattern,
			Summary:  route.Summary,
			Statuses: route.Statuses,
		})
	}

	return result
}

// THE test of this package: the shipped spec and the shipped server agree.
// Adding a route without documenting it fails here, in CI, in milliseconds -
// rather than in a client integration six weeks later.
func TestSpecMatchesTheServer(t *testing.T) {
	spec, err := contract.Load(specPath(t))
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}

	differences := contract.Compare(spec, endpoints())

	for _, difference := range differences {
		t.Errorf("%s", difference)
	}
}

func TestSpecIsWellDocumented(t *testing.T) {
	spec, err := contract.Load(specPath(t))
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}

	for _, difference := range contract.CheckDocumentation(spec) {
		t.Errorf("%s", difference)
	}
}

func TestUndocumentedRouteIsReported(t *testing.T) {
	spec, err := contract.Load(specPath(t))
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}

	drifted := append(endpoints(), contract.Endpoint{
		Method:   http.MethodPost,
		Pattern:  "/products/{sku}/discounts",
		Summary:  "Apply a discount",
		Statuses: []int{http.StatusOK},
	})

	differences := contract.Compare(spec, drifted)

	if !containsKind(differences, "missing_in_spec") {
		t.Errorf("an undocumented route was not reported: %v", differences)
	}
}

func TestRouteMissingFromTheServerIsReported(t *testing.T) {
	spec, err := contract.Load(specPath(t))
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}

	// Drop the reservations endpoint from the server, as a removal would.
	var reduced []contract.Endpoint

	for _, endpoint := range endpoints() {
		if strings.Contains(endpoint.Pattern, "reservations") {
			continue
		}

		reduced = append(reduced, endpoint)
	}

	differences := contract.Compare(spec, reduced)

	if !containsKind(differences, "missing_in_server") {
		t.Errorf("a route promised by the spec but not served was not reported: %v", differences)
	}
}

func TestUndocumentedStatusIsReported(t *testing.T) {
	spec, err := contract.Load(specPath(t))
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}

	drifted := endpoints()

	for i := range drifted {
		if drifted[i].Method == http.MethodGet && drifted[i].Pattern == "/products" {
			drifted[i].Statuses = append(drifted[i].Statuses, http.StatusBadRequest)
		}
	}

	if !containsKind(contract.Compare(spec, drifted), "undocumented_status") {
		t.Error("a new status code that the spec does not document was not reported")
	}
}

func TestSummaryMismatchIsReported(t *testing.T) {
	spec, err := contract.Load(specPath(t))
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}

	drifted := endpoints()
	drifted[0].Summary = "Something else entirely"

	if !containsKind(contract.Compare(spec, drifted), "summary_mismatch") {
		t.Error("a summary that drifted from the spec was not reported")
	}
}

// A 500 is always possible and rarely declared per route, so the spec
// documenting one must not be reported as a broken promise.
func TestDocumentedInternalErrorIsNotAnUnimplementedStatus(t *testing.T) {
	spec := contract.Spec{
		OpenAPI: "3.0.3",
		Paths: map[string]map[string]contract.Operation{
			"/things": {"get": {
				Summary: "List things",
				Responses: map[string]contract.Response{
					"200": {Description: "ok"},
					"500": {Description: "internal"},
				},
			}},
		},
	}

	differences := contract.Compare(spec, []contract.Endpoint{{
		Method: http.MethodGet, Pattern: "/things", Summary: "List things",
		Statuses: []int{http.StatusOK},
	}})

	if len(differences) != 0 {
		t.Errorf("expected no differences, got %v", differences)
	}
}

// "default" and "4XX" are legal OpenAPI response keys that name a range, not a
// code; parsing them as integers would be a false positive.
func TestNonNumericResponseKeysAreIgnored(t *testing.T) {
	spec := contract.Spec{
		OpenAPI: "3.0.3",
		Paths: map[string]map[string]contract.Operation{
			"/things": {"get": {
				Summary:   "List things",
				Responses: map[string]contract.Response{"200": {Description: "ok"}, "default": {Description: "error"}},
			}},
		},
	}

	differences := contract.Compare(spec, []contract.Endpoint{{
		Method: http.MethodGet, Pattern: "/things", Summary: "List things",
		Statuses: []int{http.StatusOK},
	}})

	if len(differences) != 0 {
		t.Errorf("expected no differences, got %v", differences)
	}
}

func TestNormalisePattern(t *testing.T) {
	cases := map[string]string{
		"/products":                    "/products",
		"/products/{sku}":              "/products/{sku}",
		"/files/{path...}":             "/files/{path}",
		"/a/{one}/b/{two}":             "/a/{one}/b/{two}",
		"/products/{sku}/reservations": "/products/{sku}/reservations",
	}

	for pattern, want := range cases {
		if got := contract.NormalisePattern(pattern); got != want {
			t.Errorf("NormalisePattern(%q) = %q, want %q", pattern, got, want)
		}
	}
}

func TestLoadRejectsSomethingThatIsNotASpec(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-spec.yaml")

	if err := os.WriteFile(path, []byte("hello: world\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := contract.Load(path); err == nil {
		t.Error("expected an error for a document with no openapi version")
	}

	if _, err := contract.Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Error("expected an error for a missing file")
	}
}

func containsKind(differences []contract.Difference, kind string) bool {
	for _, difference := range differences {
		if difference.Kind == kind {
			return true
		}
	}

	return false
}
