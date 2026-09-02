// Day 80 - Containers & CI/CD: the deployable artefacts, checked.
//
// This day's deliverable is not a program - it is the Dockerfile, the compose
// stack, the two GitHub Actions workflows and the Makefile that ship the
// service in cmd/api. None of those run here; they run on someone else's
// machine, weeks from now, and the first sign that one is wrong is usually an
// outage.
//
// So this demo reads them the way the tests do (deploy_test.go asserts the
// same properties) and prints what an operator would want to confirm before a
// release.
//
// Run:  go run ./Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-80
// Serve: go run ./Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-80/cmd/api
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	root := dayDir()

	section("1. The image")

	dockerfile, err := os.ReadFile(filepath.Join(root, "Dockerfile")) //nolint:gosec // path is built by this program
	if err != nil {
		return fmt.Errorf("read Dockerfile: %w", err)
	}

	reportDockerfile(string(dockerfile))

	section("2. The workflows")

	workflows, err := LoadWorkflows(filepath.Join(root, ".github", "workflows"))
	if err != nil {
		return err
	}

	names := make([]string, 0, len(workflows))

	for name := range workflows {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		workflow := workflows[name]

		fmt.Printf("  %s (%s)\n", name, workflow.Name)
		fmt.Printf("    triggers: %s\n", strings.Join(workflow.Triggers(), ", "))
		fmt.Printf("    jobs:     %d\n", len(workflow.Jobs))
		fmt.Printf("    actions:  %s\n", strings.Join(workflow.UsedActions(), ", "))

		findings := workflow.Findings()

		if len(findings) == 0 {
			fmt.Println("    findings: none")

			continue
		}

		fmt.Printf("    findings: %d\n", len(findings))

		for _, finding := range findings {
			fmt.Printf("      - %s\n", finding)
		}
	}

	section("3. What is actually checked, and by what")

	for _, line := range []string{
		"deploy_test.go   the Dockerfile runs as a non-root user, on a pinned base",
		"deploy_test.go   the stop grace period outlives the connection drain",
		"deploy_test.go   the compose stack has every service the app needs",
		"deploy_test.go   the release workflow tests and vets BEFORE it publishes",
		"deploy_test.go   the Makefile exposes the same release flow CI uses",
		"deploy_test.go   docs/DEPLOY.md answers the operator's questions",
		"workflow.go      no un-pinned actions, no missing timeouts, no",
		"                 github.event interpolated into a run block",
	} {
		fmt.Println("  " + line)
	}

	fmt.Println()
	fmt.Println("  none of this needs a running Docker daemon: the artefacts are text,")
	fmt.Println("  and text can be parsed and asserted. That is what makes deployment")
	fmt.Println("  configuration testable at all.")

	fmt.Println("\n  the operator's guide is in docs/DEPLOY.md")

	return nil
}

func reportDockerfile(content string) {
	var (
		stages []string
		user   string
		base   string
	)

	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "#") {
			continue
		}

		upper := strings.ToUpper(trimmed)

		switch {
		case strings.HasPrefix(upper, "FROM "):
			stages = append(stages, strings.TrimSpace(trimmed[len("FROM "):]))

			if base == "" {
				base = strings.TrimSpace(trimmed[len("FROM "):])
			}

		case strings.HasPrefix(upper, "USER "):
			user = strings.TrimSpace(trimmed[len("USER "):])
		}
	}

	fmt.Printf("  build stages: %d\n", len(stages))

	for i, stage := range stages {
		fmt.Printf("    %d. %s\n", i+1, stage)
	}

	fmt.Printf("  runs as user: %s\n", orNone(user))
	fmt.Println("  a multi-stage build keeps the compiler, the module cache and the")
	fmt.Println("  source out of the shipped image - smaller, and a smaller attack")
	fmt.Println("  surface. USER is what stops a container escape from being root.")
}

func orNone(value string) string {
	if value == "" {
		return "root (nothing set USER)"
	}

	return value
}

// dayDir is this day's directory relative to the module root, so the demo can
// read its own artefacts however it was started.
func dayDir() string {
	return filepath.Join(
		"Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines",
		"Day-80")
}

func section(title string) {
	underline := make([]byte, len(title))

	for i := range underline {
		underline[i] = '='
	}

	fmt.Printf("\n%s\n%s\n", title, underline)
}
