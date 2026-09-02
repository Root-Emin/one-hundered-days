package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

/*
Day 68 - Advanced Testing & Quality: Linting with golangci-lint

Tasks covered:

 1. golangci-lint installed and runnable locally and in CI
 2. Core linters enabled: govet, staticcheck, errcheck, ineffassign - plus a
    small high-signal extension set (.golangci.yml)
 3. Findings fixed rather than suppressed: examples/before -> examples/after
 4. .golangci.yml checked in, so every machine and CI apply the same rules

Install:

	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest

Run:

	golangci-lint run              # uses .golangci.yml from this directory
	golangci-lint run --fix        # applies what it can fix on its own
	golangci-lint fmt              # formatting only (gofmt + goimports)
	go run .                       # this program: runs both examples and
	                               # shows the difference

In CI (Day 79 wires this into a workflow):

	golangci-lint run --timeout=5m
*/

func main() {
	// The linter is run against paths relative to this directory, so refuse
	// to guess when started from somewhere else.
	if _, err := os.Stat("examples/before"); err != nil {
		fmt.Println("\nRun this from the Day-68 directory:")
		fmt.Println("\n    cd " + dayDirectory() + " && go run .")

		printCatalogue()

		return
	}

	binary, err := exec.LookPath("golangci-lint")
	if err != nil {
		if home, homeErr := os.UserHomeDir(); homeErr == nil {
			candidate := home + "/go/bin/golangci-lint"

			if _, statErr := os.Stat(candidate); statErr == nil {
				binary = candidate
				err = nil
			}
		}
	}

	if err != nil {
		fmt.Println("\ngolangci-lint is not installed. Install it with:")
		fmt.Println("\n    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest")
		fmt.Println("\nThen run this program again, or just:")
		fmt.Println("\n    golangci-lint run")

		printCatalogue()

		return
	}

	fmt.Println("\n1) examples/before - written to fail (built with -tags lintdemo)")
	fmt.Println(strings.Repeat("-", 78))

	// The config excludes examples/before, so the flags below re-enable the
	// linters explicitly for this one directory.
	output, _ := run(binary,
		"run", "--no-config", "--default=none", "--build-tags=lintdemo",
		"-E", "errcheck", "-E", "staticcheck", "-E", "govet", "-E", "ineffassign",
		"-E", "bodyclose", "-E", "errorlint", "-E", "unused", "-E", "misspell",
		"./examples/before/")

	fmt.Println(indent(output))

	fmt.Println("2) examples/after - the same code, fixed")
	fmt.Println(strings.Repeat("-", 78))

	output, err = run(binary, "run", "./examples/after/")

	fmt.Println(indent(output))

	if err != nil {
		fmt.Println("  (unexpected: the fixed example should be clean)")
	}

	printCatalogue()
}

// dayDirectory is only used for the error message above.
func dayDirectory() string {
	return "learning/go/Section-14-Advanced-Testing-And-Quality-" +
		"Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-68"
}

func run(binary string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	command := exec.CommandContext(ctx, binary, args...)

	output, err := command.CombinedOutput()

	return strings.TrimRight(string(output), "\n"), err
}

func indent(text string) string {
	if strings.TrimSpace(text) == "" {
		return "  (no output)"
	}

	lines := strings.Split(text, "\n")

	for i, line := range lines {
		lines[i] = "  " + line
	}

	return strings.Join(lines, "\n")
}

func printCatalogue() {
	fmt.Println("\nWhat each enabled linter is for")
	fmt.Println(strings.Repeat("-", 78))

	linters := []struct {
		name string
		what string
	}{
		{"govet", "the standard vet suite: wrong printf verbs, copied locks, unreachable code"},
		{"staticcheck", "bugs, dead code, deprecated APIs, and simplifications that are actually simpler"},
		{"errcheck", "errors returned and ignored - the single most common Go bug"},
		{"ineffassign", "a value assigned and never read, usually the wrong variable name"},
		{"unused", "private identifiers nobody calls"},
		{"bodyclose", "an HTTP response body left open, which leaks a connection per call"},
		{"rowserrcheck", "rows.Err() forgotten, so a truncated result set looks like a short one"},
		{"noctx", "HTTP requests built without a context: uncancellable, no deadline"},
		{"errorlint", "err == ErrX instead of errors.Is, and %v where %w was meant"},
		{"copyloopvar", "the old loop-variable capture idiom, now redundant"},
		{"misspell", "typos in comments and strings"},
	}

	for _, linter := range linters {
		fmt.Printf("  %-14s %s\n", linter.name, linter.what)
	}

	fmt.Println("\nRules of the road")
	fmt.Println(strings.Repeat("-", 78))
	fmt.Println("  * Fix the finding, do not silence it. //nolint is for the rare case")
	fmt.Println("    where the linter is wrong, and it needs a reason on the same line:")
	fmt.Println("        //nolint:errcheck // the write target is a bytes.Buffer, which never fails")
	fmt.Println("  * Disable a check in .golangci.yml only with a written reason, as this")
	fmt.Println("    repository does for ST1000 and fieldalignment.")
	fmt.Println("  * Run it before pushing, not just in CI: a red pipeline that everyone")
	fmt.Println("    expects to be red stops being a signal.")
	fmt.Println("  * Adopting it on an existing codebase: enable one linter at a time and")
	fmt.Println("    fix its findings, rather than turning everything on and giving up.")
	fmt.Println()
}
