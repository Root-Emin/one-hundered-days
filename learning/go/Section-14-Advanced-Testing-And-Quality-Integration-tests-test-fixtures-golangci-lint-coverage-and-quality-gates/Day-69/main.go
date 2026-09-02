package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

/*
Day 69 - Advanced Testing & Quality: Code Coverage and Quality Gates

Tasks covered:

 1. Coverage measured with -coverprofile and read per package and per function
 2. Modest, targeted goals: 80% on the pricing package (money), 60% on the
    gate itself, 55% overall - see coverage-policy.json
 3. A CI gate: cmd/coveragegate exits non-zero when a threshold is missed
 4. Gaps reviewed, and the ones that mattered turned into tests

Run:

	go run .                       # measure, report, and evaluate the gates

By hand:

	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out         # per function
	go tool cover -html=coverage.out         # the visual report
	go run ./cmd/coveragegate -profile coverage.out -policy coverage-policy.json -v

In CI:

	go test -race -coverprofile=coverage.out ./... \
	  && go run ./cmd/coveragegate -profile coverage.out
*/

func main() {
	if _, err := os.Stat("coverage-policy.json"); err != nil {
		fmt.Println("\nRun this from the Day-69 directory:")
		fmt.Println("\n    cd learning/go/Section-14-.../Day-69 && go run .")

		return
	}

	profile := filepath.Join(os.TempDir(), fmt.Sprintf("day69-coverage-%d.out", time.Now().UnixNano()))

	defer func() {
		if err := os.Remove(profile); err != nil && !os.IsNotExist(err) {
			fmt.Printf("remove profile: %v\n", err)
		}
	}()

	fmt.Println("\n1) Measuring")
	fmt.Println(strings.Repeat("-", 78))
	fmt.Printf("  go test -coverprofile=%s ./...\n", filepath.Base(profile))

	if output, err := run("go", "test", "-coverprofile="+profile, "-covermode=atomic", "./..."); err != nil {
		fmt.Println(indent(output))
		fmt.Println("  tests failed - a coverage number from a red suite means nothing")

		os.Exit(1)
	}

	fmt.Println("\n2) Per function (go tool cover -func)")
	fmt.Println(strings.Repeat("-", 78))

	output, err := run("go", "tool", "cover", "-func="+profile)
	if err != nil {
		fmt.Printf("  cover: %v\n", err)
		os.Exit(1)
	}

	// Only the interesting rows: everything below 100%, plus the total.
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "100.0%") && !strings.HasPrefix(line, "total:") {
			continue
		}

		if strings.TrimSpace(line) == "" {
			continue
		}

		fmt.Println("  " + trimModulePath(line))
	}

	fmt.Println("\n3) Gates")
	fmt.Println(strings.Repeat("-", 78))

	output, gateErr := run("go", "run", "./cmd/coveragegate",
		"-profile", profile, "-policy", "coverage-policy.json")

	fmt.Println(indent(output))

	printInterpretation()

	if gateErr != nil {
		os.Exit(1)
	}
}

func run(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	command := exec.CommandContext(ctx, name, args...)

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

func trimModulePath(line string) string {
	const marker = "Day-69/"

	if index := strings.Index(line, marker); index >= 0 {
		return line[index+len(marker):]
	}

	return line
}

func printInterpretation() {
	fmt.Println("\nHow to read a coverage number")
	fmt.Println(strings.Repeat("-", 78))

	points := []string{
		"Go measures STATEMENTS, not branches. An if whose else never runs still",
		"counts every statement inside it - so 100% statement coverage can still",
		"miss half the decisions.",
		"",
		"Coverage finds untested code. It cannot tell you whether the tests that",
		"do run assert anything: a test that calls a function and ignores the",
		"result moves the number and protects nothing.",
		"",
		"Rank by blast radius, not by percentage. In this day's policy the pricing",
		"package must reach 80% because a mistake there charges the wrong amount;",
		"the day's own runner program is excluded entirely, with the reason",
		"written into coverage-policy.json.",
		"",
		"Two things coverage is genuinely good at:",
		"  1. finding a branch nobody tested (the negative-discount case here,",
		"     which was a real gap - a negative percent could have increased a",
		"     price)",
		"  2. finding dead code (LegacyQuote in this package was at 0% because",
		"     nothing called it; it was deleted rather than tested)",
		"",
		"Set the threshold slightly below where you are and ratchet it upward.",
		"A gate at 90% on a codebase at 60% gets disabled within a week, and a",
		"disabled gate protects nothing.",
	}

	for _, point := range points {
		if point == "" {
			fmt.Println()
			continue
		}

		fmt.Println("  " + point)
	}

	fmt.Println()
}
