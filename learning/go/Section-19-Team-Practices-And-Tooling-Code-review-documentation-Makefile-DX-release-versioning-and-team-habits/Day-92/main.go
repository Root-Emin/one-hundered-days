// Day 92 - Team practices: documentation and API contracts.
//
// Documentation is part of the deliverable, and the reason it rots is that
// nothing fails when it does. So each artefact here is checked by code:
//
//	godoc comments   -> internal/docslint parses the AST
//	api/openapi.yaml -> internal/contract compares it with the route table
//	CHANGELOG.md     -> internal/changelog parses and validates it
//	ARCHITECTURE.md  -> read by humans; nothing can check whether a decision
//	                    is still true, which is why it records WHY
//
// cmd/docscheck runs the first three as a CI step.
//
// Run: go run ./Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-92
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-92/internal/catalog"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-92/internal/changelog"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-92/internal/contract"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-92/internal/docslint"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-92/internal/httpapi"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	if err := demoGodoc(); err != nil {
		return err
	}

	if err := demoContract(); err != nil {
		return err
	}

	if err := demoChangelog(); err != nil {
		return err
	}

	demoArchitecture()

	return nil
}

//
// 1. GODOC
//

func demoGodoc() error {
	section("1. Package documentation")

	fmt.Println("  three conventions, and what each one buys:")
	fmt.Println()
	fmt.Println("  - a package comment: the first thing on the godoc page. Without it,")
	fmt.Println("    the reader's first question - what is this for? - is answered by a")
	fmt.Println("    list of function names.")
	fmt.Println("  - a doc comment starts with the symbol's name: godoc renders the")
	fmt.Println("    first sentence as the summary, so \"Returns the user\" next to")
	fmt.Println("    \"GetUser\" reads as a fragment where \"GetUser returns the user\"")
	fmt.Println("    reads as documentation.")
	fmt.Println("  - every exported symbol is documented: an exported name is a promise")
	fmt.Println("    to someone who cannot read the body.")

	// A fixture under testdata, so the demo has something to report.
	fixture := filepath.Join(dayDir(), "internal", "docslint", "testdata", "badpackage")

	issues, err := docslint.CheckPackage(fixture, docslint.DefaultOptions())
	if err != nil {
		return err
	}

	fmt.Printf("\n  linting a deliberately undocumented package (%d issue(s)):\n", len(issues))

	for _, issue := range issues {
		fmt.Printf("    %-20s %s\n", issue.Rule, issue.Message)
	}

	counts := docslint.Summary(issues)

	fmt.Printf("\n  by rule: %s\n", formatCounts(counts))

	// And the real packages, which pass.
	real, err := docslint.CheckDirectory(filepath.Join(dayDir(), "internal", "catalog"), docslint.DefaultOptions())
	if err != nil {
		return err
	}

	fmt.Printf("  internal/catalog: %d issue(s)\n", len(real))

	return nil
}

//
// 2. THE CONTRACT
//

func demoContract() error {
	section("2. The OpenAPI contract, checked against the server")

	spec, err := contract.Load(filepath.Join(dayDir(), "api", "openapi.yaml"))
	if err != nil {
		return err
	}

	api := httpapi.New(catalog.New(), nil)

	endpoints := endpointsFrom(api)

	fmt.Printf("  %s %s: %d path(s) documented, %d route(s) served\n",
		spec.Info.Title, spec.Info.Version, len(spec.Paths), len(endpoints))

	differences := contract.Compare(spec, endpoints)

	if len(differences) == 0 {
		fmt.Println("  no differences: every route is documented, every documented route exists,")
		fmt.Println("  and every status code the handlers return appears in the spec")
	}

	for _, difference := range differences {
		fmt.Println("  " + difference.String())
	}

	// Now show what drift looks like, by pretending someone shipped a route
	// and a status code without touching the spec.
	fmt.Println("\n  what happens when someone adds a route and forgets the spec:")

	drifted := append(endpointsFrom(api), contract.Endpoint{
		Method:   "POST",
		Pattern:  "/products/{sku}/discounts",
		Summary:  "Apply a discount",
		Statuses: []int{200, 404},
	})

	for i := range drifted {
		if drifted[i].Pattern == "/products" && drifted[i].Method == "GET" {
			// Someone also taught the list endpoint to paginate, and 400 is
			// now possible for a bad cursor.
			drifted[i].Statuses = append(drifted[i].Statuses, 400)
		}
	}

	for _, difference := range contract.Compare(spec, drifted) {
		fmt.Println("    " + difference.String())
	}

	fmt.Println()
	fmt.Println("  the comparison runs both ways on purpose: a route missing from the")
	fmt.Println("  spec is an undocumented feature, and a spec entry with no route is a")
	fmt.Println("  promise the server does not keep - and clients have already been")
	fmt.Println("  written against it.")

	fmt.Println()
	fmt.Println("  a drifted spec is WORSE than no spec: clients trust it, generate code")
	fmt.Println("  from it, and find out the difference in production.")

	return nil
}

func endpointsFrom(api *httpapi.API) []contract.Endpoint {
	routes := api.Routes()

	endpoints := make([]contract.Endpoint, 0, len(routes))

	for _, route := range routes {
		endpoints = append(endpoints, contract.Endpoint{
			Method:   route.Method,
			Pattern:  route.Pattern,
			Summary:  route.Summary,
			Statuses: route.Statuses,
		})
	}

	return endpoints
}

//
// 3. THE CHANGELOG
//

func demoChangelog() error {
	section("3. The changelog")

	entries, err := changelog.Load(filepath.Join(dayDir(), "CHANGELOG.md"))
	if err != nil {
		return err
	}

	fmt.Printf("  %q: %d release(s)\n\n", entries.Title, len(entries.Releases))

	for _, release := range entries.Releases {
		date := release.Date

		if date == "" {
			date = "unreleased"
		}

		fmt.Printf("  %-12s %-12s %d entries", release.Version, date, release.Total())

		var categories []string

		for _, category := range changelog.Categories {
			if count := len(release.Entries[category]); count > 0 {
				categories = append(categories, fmt.Sprintf("%s=%d", category, count))
			}
		}

		if len(categories) > 0 {
			fmt.Printf("  (%s)", strings.Join(categories, " "))
		}

		fmt.Println()
	}

	problems := entries.Validate()

	fmt.Printf("\n  validation: %d problem(s)\n", len(problems))

	for _, problem := range problems {
		fmt.Println("    " + problem.String())
	}

	// And a broken one, so the rules are visible.
	broken := changelog.Parse(`# Changelog

## [1.0.0] - 2026-06-02

### Added

- The first release.

## [2.0.0]

### Misc

- Some things changed.
`)

	fmt.Println("\n  the same validation against a broken file:")

	for _, problem := range broken.Validate() {
		fmt.Println("    " + problem.String())
	}

	fmt.Println()
	fmt.Println("  a changelog is not the git history. The history is every change,")
	fmt.Println("  ordered by when it landed, written for the team; the changelog is the")
	fmt.Println("  user-visible ones, ordered by release, written for the person deciding")
	fmt.Println("  whether to upgrade.")
	fmt.Println()
	fmt.Println("  \"refactor the retry loop\" belongs in the history and nowhere else.")
	fmt.Println("  \"retries now stop after 30s instead of retrying forever\" belongs in")
	fmt.Println("  the changelog, because somebody's timeout budget depends on it.")

	return nil
}

//
// 4. ARCHITECTURE
//

func demoArchitecture() {
	section("4. ARCHITECTURE.md")

	fmt.Println("  the one document nothing can verify - which is exactly why it records")
	fmt.Println("  decisions and their reasons rather than structure a reader could get")
	fmt.Println("  from the directory listing.")
	fmt.Println()
	fmt.Println("  what docs/ARCHITECTURE.md holds:")

	for _, line := range []string{
		"the layer diagram and the one dependency rule (domain never imports transport)",
		"why money is an int64 count of cents and never a float",
		"why insufficient stock is 409 and not 400",
		"why the route table is DATA, which is what makes the contract check possible",
		"what is deliberately absent - auth, pagination, caching - and the trigger to revisit",
	} {
		fmt.Println("    - " + line)
	}

	fmt.Println()
	fmt.Println("  the last one is what saves the most time. \"We chose not to paginate,")
	fmt.Println("  revisit at ~10,000 products\" ends an argument that otherwise restarts")
	fmt.Println("  every six months with a new engineer.")

	section("5. Why any of this is checked by code")

	fmt.Println("  documentation rots because nothing fails when it does. A test that")
	fmt.Println("  fails on drift converts a slow, invisible decay into a red build -")
	fmt.Println("  which is the only mechanism that has ever kept docs current.")
	fmt.Println()
	fmt.Println("  run it in CI:  go run ./cmd/docscheck")

	fmt.Println("\n  the architecture write-up is in docs/ARCHITECTURE.md")
}

func formatCounts(counts map[string]int) string {
	parts := make([]string, 0, len(counts))

	for _, rule := range []string{"missing_package_doc", "missing_doc", "bad_prefix"} {
		if count, found := counts[rule]; found {
			parts = append(parts, fmt.Sprintf("%s=%d", rule, count))
		}
	}

	return strings.Join(parts, " ")
}

func dayDir() string {
	path := filepath.Join(
		"Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits",
		"Day-92")

	if _, err := os.Stat(path); err != nil {
		return "."
	}

	return path
}

func section(title string) {
	underline := make([]byte, len(title))

	for i := range underline {
		underline[i] = '='
	}

	fmt.Printf("\n%s\n%s\n", title, underline)
}
