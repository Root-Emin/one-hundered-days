package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

/*
Day 79 - Containers & CI/CD: CI Pipelines for Go

Tasks covered:

 1. A lint job: gofmt, go vet, golangci-lint and govulncheck, failing the build
 2. Vet and build across a version and platform matrix
 3. An integration job with Postgres and Redis service containers
 4. Coverage published as a job summary and as a downloadable artifact

Files:

	.github/workflows/ci.yml  the pipeline
	workflow.go               the reader (from Day 78)
	main.go                   prints the pipeline's shape and timings
	pipeline_test.go          asserts the properties that matter

Run:

	go run .

Locally, the same checks in one line each:

	gofmt -l .
	go vet ./...
	golangci-lint run
	go test -race -shuffle=on ./...
	go test -tags=integration ./...
	go test -covermode=atomic -coverprofile=coverage.out ./...
*/

func main() {
	if _, err := os.Stat(".github/workflows/ci.yml"); err != nil {
		fmt.Println("Run this from the Day-79 directory.")

		return
	}

	workflows, err := LoadWorkflows(".github/workflows")
	if err != nil {
		fmt.Printf("%v\n", err)
		os.Exit(1)
	}

	workflow := workflows["ci.yml"]

	fmt.Println("\n1) The pipeline")
	fmt.Println(strings.Repeat("-", 82))

	printJobs(workflow)

	fmt.Println("\n2) The dependency graph")
	fmt.Println(strings.Repeat("-", 82))

	printGraph(workflow)

	fmt.Println("\n3) What each job protects against")
	fmt.Println(strings.Repeat("-", 82))

	printPurpose()

	fmt.Println("\n4) Keeping it fast")
	fmt.Println(strings.Repeat("-", 82))

	printSpeed()

	if findings := workflow.Findings(); len(findings) > 0 {
		fmt.Println("\nFindings")
		fmt.Println(strings.Repeat("-", 82))

		for _, finding := range findings {
			fmt.Printf("  %s\n", finding)
		}

		fmt.Println()
	}
}

func printJobs(workflow Workflow) {
	names := make([]string, 0, len(workflow.Jobs))

	for name := range workflow.Jobs {
		names = append(names, name)
	}

	sort.Strings(names)

	fmt.Printf("  %-14s %-10s %-9s %-30s %s\n", "JOB", "TIMEOUT", "STEPS", "NEEDS", "EXTRAS")

	for _, name := range names {
		job := workflow.Jobs[name]

		needs := "-"

		switch value := job.Needs.(type) {
		case string:
			needs = value
		case []any:
			parts := make([]string, 0, len(value))

			for _, item := range value {
				parts = append(parts, fmt.Sprint(item))
			}

			needs = strings.Join(parts, ", ")
		}

		extras := make([]string, 0, 3)

		if len(job.Strategy.Matrix) > 0 {
			cells := 1

			for _, values := range job.Strategy.Matrix {
				if list, ok := values.([]any); ok {
					cells *= len(list)
				}
			}

			extras = append(extras, fmt.Sprintf("matrix (~%d cells)", cells))
		}

		if len(job.Services) > 0 {
			services := make([]string, 0, len(job.Services))

			for service := range job.Services {
				services = append(services, service)
			}

			sort.Strings(services)

			extras = append(extras, "services: "+strings.Join(services, "+"))
		}

		if job.Permissions != nil {
			extras = append(extras, "scoped permissions")
		}

		fmt.Printf("  %-14s %-10s %-9d %-30s %s\n",
			name,
			fmt.Sprintf("%dm", job.TimeoutMinutes),
			len(job.Steps),
			needs,
			strings.Join(extras, ", "))
	}
}

func printGraph(workflow Workflow) {
	fmt.Println(`
    lint ────────┐
    test  ───────┼──► build ──► image
    integration ─┘

  lint, test and integration have no 'needs', so they start together. build
  waits for all three; image waits for build.

  The shape matters: putting them in one sequential job would turn a ~4 minute
  pipeline into ~12, and a developer who waits twelve minutes stops waiting.`)
}

func printPurpose() {
	rows := []struct {
		job     string
		catches string
	}{
		{"gofmt", "formatting arguments in code review, forever"},
		{"go vet", "printf mistakes, copied locks, unreachable code"},
		{"golangci-lint", "unchecked errors, leaked response bodies, dead code (Day 68)"},
		{"govulncheck", "known CVEs in code this binary can actually reach (Day 54)"},
		{"go build ./...", "a package that compiles nowhere but the author's machine"},
		{"go test -race", "data races - the bug class that only appears under production load"},
		{"-shuffle=on", "tests that pass only in the order they were written"},
		{"matrix", "\"works on my Go version\" and \"works on my OS\""},
		{"integration", "wiring, SQL and migrations that unit tests cannot reach (Day 66)"},
		{"coverage gate", "a slow, silent decline in tested code (Day 69)"},
		{"image build on PRs", "a Dockerfile that rots because nobody builds it until release"},
		{"trivy", "a base image with a known critical CVE"},
	}

	fmt.Printf("  %-22s %s\n", "STEP", "CATCHES")

	for _, row := range rows {
		fmt.Printf("  %-22s %s\n", row.job, row.catches)
	}
}

func printSpeed() {
	fmt.Println(`  A pipeline people wait for is a pipeline that finds problems. One that
  takes twenty minutes gets bypassed with "just merge it, CI is slow".

    * Parallel jobs, not one long one. Independent work should be independent.
    * Cache the module and build caches (setup-go cache: true). The build
      cache is the bigger win once the repository has many packages.
    * Put the fastest check first: lint fails in a minute, so a formatting
      mistake does not wait for the test matrix.
    * fail-fast: false on the matrix - knowing that ONLY macOS fails is worth
      more than the minutes saved.
    * Keep the matrix small. Two Go versions and two platforms is four cells;
      four and four is sixteen, and nobody reads sixteen results.
    * Slow suites (integration, end-to-end) belong behind a build tag and in
      their own job, not in the inner loop.
    * concurrency + cancel-in-progress so a rapid series of pushes does not
      keep three runners busy on obsolete commits.

  Budgets worth defending: PR feedback under 5 minutes, the full pipeline
  under 15.`)

	fmt.Println()
}
