// Command prcheck runs the mechanical half of code review against the current
// branch, before a human ever looks at it.
//
//	go run ./Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-91/cmd/prcheck
//	go run ./Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-91/cmd/prcheck -desc pr.md -base main
//	go run ./Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-91/cmd/prcheck -strict          # nonzero exit on warnings too
//
// It is a pre-push hook and a CI step, not a gate on taste. Everything it
// reports is something the author can fix in a minute; everything it cannot
// check is what the human review is for.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-91/internal/gitinfo"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-91/internal/prlint"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
}

func run() error {
	var (
		repo        = flag.String("repo", ".", "repository path")
		base        = flag.String("base", "", "base branch (default: origin's HEAD, else main)")
		description = flag.String("desc", "", "path to a file holding the pull request description")
		title       = flag.String("title", "", "pull request title (default: the first commit subject)")
		strict      = flag.Bool("strict", false, "exit nonzero on warnings as well as blocking findings")
	)

	flag.Parse()

	ctx := context.Background()

	if !gitinfo.IsRepository(ctx, *repo) {
		return fmt.Errorf("%s is not a git repository", *repo)
	}

	if *base == "" {
		*base = gitinfo.DefaultBranch(ctx, *repo)
	}

	branch, err := gitinfo.Branch(ctx, *repo)
	if err != nil {
		return err
	}

	fmt.Printf("branch %s, compared against %s\n", branch, *base)

	var findings prlint.Findings

	commits, err := gitinfo.Commits(ctx, *repo, *base)
	if err != nil {
		return err
	}

	fmt.Printf("\ncommits: %d\n", len(commits))

	if branch == *base {
		fmt.Println("  you are on the trunk. Short-lived branches are the whole point:")
		fmt.Println("  they keep the diff small and let CI run before anyone else is affected.")
	}

	for _, commit := range commits {
		commitFindings := prlint.CheckCommit(commit)

		short := commit.Hash

		if len(short) > 8 {
			short = short[:8]
		}

		marker := "ok"

		if len(commitFindings) > 0 {
			marker = fmt.Sprintf("%d finding(s)", len(commitFindings))
		}

		fmt.Printf("  %s  %-60s %s\n", short, truncate(commit.Subject, 60), marker)

		findings = append(findings, commitFindings...)
	}

	diff, err := gitinfo.Diff(ctx, *repo, *base)
	if err != nil {
		return err
	}

	if len(diff.Files) == 0 {
		// Nothing committed on this branch yet: fall back to the working
		// tree, which is what a pre-push hook usually wants anyway.
		if diff, err = gitinfo.UncommittedDiff(ctx, *repo); err != nil {
			return err
		}
	}

	verdict := diff.Review()

	fmt.Printf("\ndiff: %s  %d file(s), +%d/-%d (%d reviewable lines)\n",
		verdict.Stats.Label(), verdict.Stats.Files, verdict.Stats.Added,
		verdict.Stats.Removed, verdict.Stats.Total)

	for _, finding := range verdict.Findings {
		fmt.Printf("  [blocking] %s\n", finding)

		findings = append(findings, prlint.Finding{
			Severity: prlint.Blocking, Rule: "diff", Message: finding,
		})
	}

	for _, warning := range verdict.Warnings {
		fmt.Printf("  [warning]  %s\n", warning)

		findings = append(findings, prlint.Finding{
			Severity: prlint.Warning, Rule: "diff", Message: warning,
		})
	}

	if *description != "" {
		body, err := os.ReadFile(*description) //nolint:gosec // the path comes from the operator
		if err != nil {
			return fmt.Errorf("read description: %w", err)
		}

		pullRequest := prlint.PullRequest{Title: *title, Description: string(body)}

		if pullRequest.Title == "" && len(commits) > 0 {
			// GitHub does the same thing: a single-commit PR takes its title
			// from the commit.
			pullRequest.Title = commits[len(commits)-1].Subject
		}

		descriptionFindings := prlint.CheckDescription(pullRequest)

		fmt.Printf("\ndescription: %s (%d finding(s))\n", *description, len(descriptionFindings))

		findings = append(findings, descriptionFindings...)
	} else {
		fmt.Println("\ndescription: not checked (pass -desc pr.md)")
	}

	fmt.Printf("\n%d finding(s): %d blocking, %d warning, %d nit\n",
		len(findings), findings.Count(prlint.Blocking),
		findings.Count(prlint.Warning), findings.Count(prlint.Nit))

	for _, finding := range findings {
		if finding.Rule == "diff" {
			continue // already printed above, with its own formatting
		}

		fmt.Println("  " + strings.ReplaceAll(finding.String(), "\n", "\n  "))
	}

	if findings.Blocking() {
		return errors.New("blocking findings must be fixed before review")
	}

	if *strict && findings.Count(prlint.Warning) > 0 {
		return errors.New("warnings present and -strict is set")
	}

	return nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}

	return value[:limit-3] + "..."
}
