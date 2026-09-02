// Package contract compares an OpenAPI document with the routes a server
// actually serves.
//
// A specification nobody verifies drifts from the implementation within about
// a month, and a drifted spec is worse than no spec at all: clients trust it,
// generate code from it, and discover the difference in production.
//
// So the spec is a test fixture. Add a route without documenting it and the
// test fails; document a status code the handler cannot return and the test
// fails; change a summary in one place and the test fails.
//
// What this does NOT do is validate the OpenAPI document against the OpenAPI
// meta-schema, or check that response bodies match their schemas. Those need a
// full OpenAPI toolchain. The checks here are the cheap ones that catch the
// drift that actually happens - a route, a verb, a status code.
package contract

import (
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Spec is the subset of OpenAPI this package reads.
type Spec struct {
	OpenAPI string `yaml:"openapi"`
	Info    Info   `yaml:"info"`
	// Paths maps a path template to its operations, keyed by lower-case verb.
	Paths map[string]map[string]Operation `yaml:"paths"`
}

// Info is the document header.
type Info struct {
	Title       string `yaml:"title"`
	Version     string `yaml:"version"`
	Description string `yaml:"description"`
}

// Operation is one method on one path.
type Operation struct {
	Summary     string              `yaml:"summary"`
	OperationID string              `yaml:"operationId"`
	Description string              `yaml:"description"`
	Responses   map[string]Response `yaml:"responses"`
}

// Response is one documented status code.
type Response struct {
	Description string `yaml:"description"`
	Ref         string `yaml:"$ref"`
}

// Load reads and parses an OpenAPI document.
func Load(path string) (Spec, error) {
	content, err := os.ReadFile(path) //nolint:gosec // the path comes from the caller
	if err != nil {
		return Spec{}, fmt.Errorf("read %s: %w", path, err)
	}

	var spec Spec

	if err := yaml.Unmarshal(content, &spec); err != nil {
		return Spec{}, fmt.Errorf("parse %s: %w", path, err)
	}

	if spec.OpenAPI == "" {
		return Spec{}, fmt.Errorf("%s: no openapi version - is this an OpenAPI document?", path)
	}

	if len(spec.Paths) == 0 {
		return Spec{}, fmt.Errorf("%s: no paths", path)
	}

	return spec, nil
}

// Endpoint is one route as the server implements it.
//
// The caller builds these from its own route table, which keeps this package
// free of any dependency on the server.
type Endpoint struct {
	Method   string
	Pattern  string
	Summary  string
	Statuses []int
}

// Difference is one disagreement between the spec and the server.
type Difference struct {
	// Kind is machine-readable: missing_in_spec, missing_in_server,
	// summary_mismatch, undocumented_status, unimplemented_status.
	Kind    string
	Path    string
	Method  string
	Message string
}

// String renders a difference for a terminal report.
func (d Difference) String() string {
	if d.Method == "" {
		return fmt.Sprintf("%-22s %s: %s", d.Kind, d.Path, d.Message)
	}

	return fmt.Sprintf("%-22s %s %s: %s", d.Kind, d.Method, d.Path, d.Message)
}

// pathParameter matches a Go 1.22 ServeMux wildcard: {sku}, {id...}.
var pathParameter = regexp.MustCompile(`\{([a-zA-Z0-9_]+)(\.\.\.)?\}`)

// NormalisePattern converts a ServeMux pattern to an OpenAPI path template.
//
// The two notations are nearly identical, which is exactly why the difference
// bites: {id...} in Go is a catch-all, and OpenAPI writes it {id}.
func NormalisePattern(pattern string) string {
	return pathParameter.ReplaceAllString(pattern, "{$1}")
}

// Compare reports every disagreement between the spec and the endpoints.
//
// The comparison runs in both directions on purpose. A route missing from the
// spec is an undocumented feature; a spec entry with no route is a promise the
// server does not keep, and clients have already been written against it.
func Compare(spec Spec, endpoints []Endpoint) []Difference {
	var differences []Difference

	implemented := make(map[string]Endpoint, len(endpoints))

	for _, endpoint := range endpoints {
		key := strings.ToLower(endpoint.Method) + " " + NormalisePattern(endpoint.Pattern)
		implemented[key] = endpoint
	}

	documented := make(map[string]Operation)

	for path, operations := range spec.Paths {
		for method, operation := range operations {
			documented[strings.ToLower(method)+" "+path] = operation
		}
	}

	// Server -> spec.
	for key, endpoint := range implemented {
		operation, found := documented[key]

		if !found {
			differences = append(differences, Difference{
				Kind:    "missing_in_spec",
				Method:  endpoint.Method,
				Path:    NormalisePattern(endpoint.Pattern),
				Message: "the server serves this route, the spec does not mention it",
			})

			continue
		}

		if endpoint.Summary != "" && operation.Summary != endpoint.Summary {
			differences = append(differences, Difference{
				Kind:   "summary_mismatch",
				Method: endpoint.Method,
				Path:   NormalisePattern(endpoint.Pattern),
				Message: fmt.Sprintf("spec says %q, the route table says %q",
					operation.Summary, endpoint.Summary),
			})
		}

		documentedStatuses := make(map[int]bool, len(operation.Responses))

		for code := range operation.Responses {
			status, err := strconv.Atoi(code)
			if err != nil {
				// "default" and "4XX" are legal in OpenAPI; they document a
				// range rather than a code, so they are skipped here.
				continue
			}

			documentedStatuses[status] = true
		}

		for _, status := range endpoint.Statuses {
			if !documentedStatuses[status] {
				differences = append(differences, Difference{
					Kind:   "undocumented_status",
					Method: endpoint.Method,
					Path:   NormalisePattern(endpoint.Pattern),
					Message: fmt.Sprintf("the handler can return %d %s, the spec does not document it",
						status, http.StatusText(status)),
				})
			}
		}

		serverStatuses := make(map[int]bool, len(endpoint.Statuses))

		for _, status := range endpoint.Statuses {
			serverStatuses[status] = true
		}

		for status := range documentedStatuses {
			// A 500 is always possible and rarely worth declaring per route,
			// so it is not treated as a promise the server broke.
			if status == http.StatusInternalServerError || serverStatuses[status] {
				continue
			}

			differences = append(differences, Difference{
				Kind:   "unimplemented_status",
				Method: endpoint.Method,
				Path:   NormalisePattern(endpoint.Pattern),
				Message: fmt.Sprintf("the spec documents %d %s, the handler never returns it",
					status, http.StatusText(status)),
			})
		}
	}

	// Spec -> server.
	for key := range documented {
		if _, found := implemented[key]; found {
			continue
		}

		method, path, _ := strings.Cut(key, " ")

		differences = append(differences, Difference{
			Kind:    "missing_in_server",
			Method:  strings.ToUpper(method),
			Path:    path,
			Message: "the spec promises this route, the server does not serve it",
		})
	}

	sort.Slice(differences, func(i, j int) bool {
		if differences[i].Path != differences[j].Path {
			return differences[i].Path < differences[j].Path
		}

		if differences[i].Method != differences[j].Method {
			return differences[i].Method < differences[j].Method
		}

		return differences[i].Kind < differences[j].Kind
	})

	return differences
}

// CheckDocumentation reports weaknesses in the document itself: the things a
// client integrator notices immediately and the author never does.
func CheckDocumentation(spec Spec) []Difference {
	var differences []Difference

	if spec.Info.Title == "" {
		differences = append(differences, Difference{
			Kind: "missing_info", Path: "info.title", Message: "the API has no title",
		})
	}

	if spec.Info.Version == "" {
		differences = append(differences, Difference{
			Kind: "missing_info", Path: "info.version",
			Message: "no version: clients cannot tell which contract they are reading",
		})
	}

	paths := make([]string, 0, len(spec.Paths))

	for path := range spec.Paths {
		paths = append(paths, path)
	}

	sort.Strings(paths)

	for _, path := range paths {
		methods := make([]string, 0, len(spec.Paths[path]))

		for method := range spec.Paths[path] {
			methods = append(methods, method)
		}

		sort.Strings(methods)

		for _, method := range methods {
			operation := spec.Paths[path][method]

			if operation.Summary == "" {
				differences = append(differences, Difference{
					Kind: "missing_summary", Method: strings.ToUpper(method), Path: path,
					Message: "no summary: this is the line every generated client and doc page shows",
				})
			}

			if operation.OperationID == "" {
				differences = append(differences, Difference{
					Kind: "missing_operation_id", Method: strings.ToUpper(method), Path: path,
					Message: "no operationId: generated clients fall back to a name derived from the path",
				})
			}

			for code, response := range operation.Responses {
				if response.Description == "" && response.Ref == "" {
					differences = append(differences, Difference{
						Kind: "missing_description", Method: strings.ToUpper(method), Path: path,
						Message: fmt.Sprintf("response %s has no description", code),
					})
				}
			}
		}
	}

	return differences
}
