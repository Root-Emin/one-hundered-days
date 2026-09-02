// Command coveragegate reads a coverage profile and fails when a package is
// below its threshold.
//
// This is the piece CI needs: `go test` already fails on a broken test, and
// this makes a silent drop in coverage fail too.
//
//	go test -coverprofile=coverage.out ./...
//	go run ./cmd/coveragegate -profile coverage.out -policy coverage-policy.json
//
// Exit code 0 means every gate passed; 1 means at least one did not, and every
// violation is printed - not just the first.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-69/internal/coverage"
)

func main() {
	profilePath := flag.String("profile", "coverage.out", "coverage profile produced by go test -coverprofile")
	policyPath := flag.String("policy", "coverage-policy.json", "thresholds to enforce")
	verbose := flag.Bool("v", false, "list the files with uncovered statements")

	flag.Parse()

	profile, err := coverage.ParseFile(*profilePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "coveragegate: %v\n", err)
		os.Exit(2)
	}

	policy, err := loadPolicy(*policyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "coveragegate: %v\n", err)
		os.Exit(2)
	}

	packages := profile.ByPackage()

	// The headline number counts only the packages the policy gates; the raw
	// profile total (which includes ignored packages) is printed beside it so
	// nothing is hidden.
	covered, total, totalPercent := policy.TotalFor(packages)
	rawCovered, rawTotal, rawPercent := profile.Total()

	fmt.Printf("\n%-52s %8s %10s\n", "PACKAGE", "COVERAGE", "STATEMENTS")
	fmt.Println(strings.Repeat("-", 74))

	for _, entry := range packages {
		fmt.Printf("%-52s %7.1f%% %6d/%-4d\n",
			shorten(entry.Package), entry.Percent, entry.Covered, entry.Total)

		if *verbose && len(entry.MissedFile) > 0 {
			files := make([]string, 0, len(entry.MissedFile))

			for file := range entry.MissedFile {
				files = append(files, file)
			}

			sort.Strings(files)

			for _, file := range files {
				fmt.Printf("    %-48s %d uncovered statement(s)\n",
					file[strings.LastIndex(file, "/")+1:], entry.MissedFile[file])
			}
		}
	}

	fmt.Println(strings.Repeat("-", 74))
	fmt.Printf("%-52s %7.1f%% %6d/%-4d\n", "TOTAL (gated packages)", totalPercent, covered, total)
	fmt.Printf("%-52s %7.1f%% %6d/%-4d\n", "TOTAL (including ignored)", rawPercent, rawCovered, rawTotal)

	violations := policy.Check(packages, totalPercent)

	if len(violations) == 0 {
		fmt.Printf("\nAll gates passed.\n\n")

		return
	}

	fmt.Printf("\n%d gate(s) failed:\n", len(violations))

	for _, violation := range violations {
		fmt.Printf("  %s\n", violation)
	}

	fmt.Println("\nAdd tests for the uncovered branches that matter, or change the")
	fmt.Println("threshold in the policy file - deliberately, in a reviewed commit.")
	fmt.Println()

	os.Exit(1)
}

func loadPolicy(path string) (coverage.Policy, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return coverage.Policy{}, fmt.Errorf("read policy: %w", err)
	}

	var policy coverage.Policy

	if err := json.Unmarshal(raw, &policy); err != nil {
		return coverage.Policy{}, fmt.Errorf("parse policy: %w", err)
	}

	return policy, nil
}

// shorten trims everything up to and including the day directory, so the
// table fits on a terminal whichever day's profile it is reading.
func shorten(packageName string) string {
	segments := strings.Split(packageName, "/")

	for i, segment := range segments {
		if !strings.HasPrefix(segment, "Day-") {
			continue
		}

		rest := strings.Join(segments[i+1:], "/")

		if rest == "" {
			return "(" + segment + " root)"
		}

		return rest
	}

	return packageName
}
