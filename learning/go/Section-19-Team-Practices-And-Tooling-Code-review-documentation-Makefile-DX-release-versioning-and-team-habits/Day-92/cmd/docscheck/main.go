// Command docscheck verifies that the documentation still matches the code.
//
//	go run ./Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-92/cmd/docscheck
//	go run ./Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-92/cmd/docscheck -dir . -spec api/openapi.yaml -changelog CHANGELOG.md
//
// It is a CI step. Every check here answers a question that otherwise gets
// answered by a client integrator, six weeks later, in a support ticket.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

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
	var (
		dir           = flag.String("dir", dayDir(), "directory to check for godoc issues")
		specPath      = flag.String("spec", filepath.Join(dayDir(), "api", "openapi.yaml"), "OpenAPI document")
		changelogPath = flag.String("changelog", filepath.Join(dayDir(), "CHANGELOG.md"), "changelog file")
	)

	flag.Parse()

	failures := 0

	//
	// 1. godoc
	//

	issues, err := docslint.CheckDirectory(*dir, docslint.DefaultOptions())
	if err != nil {
		return err
	}

	fmt.Printf("godoc:     %d issue(s) under %s\n", len(issues), *dir)

	for _, issue := range issues {
		fmt.Println("  " + issue.String())
	}

	failures += len(issues)

	//
	// 2. the OpenAPI contract
	//

	spec, err := contract.Load(*specPath)
	if err != nil {
		return err
	}

	endpoints := endpointsFrom(httpapi.New(catalog.New(), nil))

	differences := contract.Compare(spec, endpoints)

	fmt.Printf("\ncontract:  %s %s, %d route(s), %d difference(s)\n",
		spec.Info.Title, spec.Info.Version, len(endpoints), len(differences))

	for _, difference := range differences {
		fmt.Println("  " + difference.String())
	}

	failures += len(differences)

	documentation := contract.CheckDocumentation(spec)

	if len(documentation) > 0 {
		fmt.Printf("\nspec docs: %d issue(s)\n", len(documentation))

		for _, difference := range documentation {
			fmt.Println("  " + difference.String())
		}
	}

	failures += len(documentation)

	//
	// 3. the changelog
	//

	entries, err := changelog.Load(*changelogPath)
	if err != nil {
		return err
	}

	problems := entries.Validate()

	latest, _ := entries.Latest()

	fmt.Printf("\nchangelog: %d release(s), latest %s, %d problem(s)\n",
		len(entries.Releases), latest.Version, len(problems))

	for _, problem := range problems {
		fmt.Println("  " + problem.String())
	}

	failures += len(problems)

	if failures > 0 {
		return fmt.Errorf("%d documentation issue(s)", failures)
	}

	fmt.Println("\ndocumentation matches the code")

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

var errNoDay = errors.New("day directory not found")

// dayDir is this day's directory relative to the module root.
func dayDir() string {
	path := filepath.Join(
		"Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits",
		"Day-92")

	if _, err := os.Stat(path); err != nil {
		// Started from inside the day's own directory.
		return "."
	}

	_ = errNoDay

	return path
}
