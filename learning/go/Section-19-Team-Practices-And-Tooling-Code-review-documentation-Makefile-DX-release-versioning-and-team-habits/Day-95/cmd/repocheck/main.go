// Command repocheck audits this repository the way a newcomer would experience
// it, then reviews its source against the mechanical half of the review
// checklist.
//
//	make audit          # both
//	make review         # the source only
//	go run ./cmd/repocheck -strict   # nonzero exit on any finding (CI)
//
// It answers one question: can a competent stranger clone this, understand it,
// build it, test it and open a pull request without asking anyone anything?
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-95/internal/repoaudit"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-95/internal/selfreview"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		root       = flag.String("root", defaultRoot(), "repository root to audit")
		strict     = flag.Bool("strict", false, "exit nonzero on any finding, not just required ones")
		reviewOnly = flag.Bool("review-only", false, "skip the file audit and only review the source")
	)

	flag.Parse()

	findings := 0

	if !*reviewOnly {
		checks := repoaudit.DefaultChecks()

		report, err := repoaudit.Audit(*root, checks)
		if err != nil {
			return err
		}

		fmt.Printf("repository audit: %d/%d checks, score %d%%\n",
			len(checks)-len(missing(report)), len(checks), report.Score(checks))

		for _, present := range report.Present {
			fmt.Printf("  ok           %s\n", present)
		}

		for _, finding := range report.Findings {
			fmt.Println("  " + finding.String())
		}

		findings += len(report.Findings)

		if report.Blocked() {
			return errors.New("a required file is missing; a newcomer is blocked")
		}
	}

	comments, err := selfreview.Review(*root, selfreview.DefaultOptions())
	if err != nil {
		return err
	}

	fmt.Printf("\nself review: %d comment(s)\n", len(comments))

	for _, comment := range comments {
		fmt.Println("  " + comment.String())
	}

	if blocking := selfreview.Blocking(comments); len(blocking) > 0 {
		return fmt.Errorf("%d blocking review comment(s)", len(blocking))
	}

	findings += len(comments)

	if *strict && findings > 0 {
		return fmt.Errorf("%d finding(s) and -strict is set", findings)
	}

	return nil
}

func missing(report repoaudit.Report) []repoaudit.Finding {
	var absent []repoaudit.Finding

	for _, finding := range report.Findings {
		if finding.Missing {
			absent = append(absent, finding)
		}
	}

	return absent
}

// defaultRoot is this day's directory when run from the module root, and "."
// when run from inside it.
func defaultRoot() string {
	path := filepath.Join(
		"Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits",
		"Day-95")

	if _, err := os.Stat(path); err != nil {
		return "."
	}

	return path
}
