// Day 91 - Team practices: code review and git workflow.
//
// The four habits this day is about are usually taught as advice, which is
// unfalsifiable and therefore easy to ignore. So each one is built as
// something a machine can check:
//
//	small, short-lived branches -> internal/changeset measures the diff
//	good PR descriptions        -> internal/prlint checks the template
//	a review checklist          -> internal/review, ordered by cost
//	constructive feedback       -> internal/review's tone check + rewrites
//
// The point is not that a tool can review code. It is that the mechanical half
// of review - is there a test plan, is this 2,000 lines, does this commit
// message say anything - can be answered before a human spends attention, and
// human attention is the scarce resource in a review.
//
// cmd/prcheck runs all of it against a real branch.
//
// Run: go run ./Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-91
package main

import (
	"fmt"
	"os"
	"strings"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-91/internal/changeset"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-91/internal/prlint"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-91/internal/review"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	demoCommits()
	demoSize()
	demoDescription()
	demoChecklist()
	demoFeedback()

	section("6. What the tool cannot do")

	for _, line := range []string{
		"decide whether the code is CORRECT - that is the whole point of review",
		"tell a blunt-but-kind comment from a cruel one",
		"know whether the test plan is honest, or whether the tests test anything",
		"judge whether a design will survive the next feature",
	} {
		fmt.Println("  - " + line)
	}

	fmt.Println()
	fmt.Println("  which is exactly why the mechanical half is worth automating: every")
	fmt.Println("  minute a reviewer spends noting a missing test plan is a minute not")
	fmt.Println("  spent on the concurrency bug only a human will find.")

	fmt.Println("\n  the full guide is in docs/CODE_REVIEW.md")

	return nil
}

//
// 1. COMMITS
//

func demoCommits() {
	section("1. Commit messages")

	commits := []prlint.Commit{
		{Hash: "a1b2c3d4", Subject: "fix(store): close rows on the error path"},
		{Hash: "b2c3d4e5", Subject: "wip"},
		{Hash: "c3d4e5f6", Subject: "Added retry logic to the client."},
		{
			Hash:    "d4e5f6a7",
			Subject: "feat(api): return 202 from POST /orders because the receipt is written asynchronously by the worker",
		},
		{
			Hash:    "e5f6a7b8",
			Subject: "refactor(worker): claim events inside the transaction",
			Body:    "The previous check-then-act version had a window where two deliveries could both pass the check before either inserted its claim.",
		},
	}

	for _, commit := range commits {
		findings := prlint.CheckCommit(commit)

		fmt.Printf("\n  %q\n", truncate(commit.Subject, 76))

		if len(findings) == 0 {
			fmt.Println("    ok")

			continue
		}

		for _, finding := range findings {
			fmt.Printf("    [%s] %s\n", finding.Severity, finding.Message)
			fmt.Printf("      -> %s\n", finding.Fix)
		}
	}

	fmt.Println()
	fmt.Println("  the imperative rule is not pedantry: the subject completes the")
	fmt.Println("  sentence \"if applied, this commit will ___\", which is how it reads in")
	fmt.Println("  a revert, a cherry-pick and a generated changelog.")
}

//
// 2. SIZE
//

func demoSize() {
	section("2. Pull request size")

	cases := []struct {
		name    string
		numstat string
	}{
		{
			name: "a focused fix",
			numstat: "12\t4\tinternal/store/store.go\n" +
				"48\t0\tinternal/store/store_test.go\n",
		},
		{
			name: "code with no test",
			numstat: "86\t14\tinternal/auth/token.go\n" +
				"9\t2\tinternal/auth/session.go\n",
		},
		{
			name: "the Friday afternoon special",
			numstat: "820\t310\tinternal/api/api.go\n" +
				"240\t95\tinternal/store/store.go\n" +
				"310\t12\tinternal/api/api_test.go\n" +
				"4\t1\tgo.mod\n" +
				"18\t0\tmigrations/003_add_index.sql\n" +
				"9000\t0\tgen/orders/v1/orders.pb.go\n",
		},
	}

	for _, testCase := range cases {
		diff, err := changeset.ParseNumstat(testCase.numstat)
		if err != nil {
			fmt.Println("  parse error:", err)

			continue
		}

		verdict := diff.Review()

		fmt.Printf("\n  %s: %s, %d file(s), +%d/-%d (%d reviewable lines)\n",
			testCase.name, verdict.Stats.Label(), verdict.Stats.Files,
			verdict.Stats.Added, verdict.Stats.Removed, verdict.Stats.Total)

		for _, finding := range verdict.Findings {
			fmt.Printf("    [blocking] %s\n", wrap(finding, 70, 16))
		}

		for _, warning := range verdict.Warnings {
			fmt.Printf("    [warning]  %s\n", wrap(warning, 70, 16))
		}

		if len(verdict.Findings) == 0 && len(verdict.Warnings) == 0 {
			fmt.Println("    ok - reviewable in one sitting")
		}
	}

	fmt.Println()
	fmt.Printf("  the thresholds (%d lines fine, %d getting large, %d skimmed) are not\n",
		changeset.SmallChange, changeset.LargeChange, changeset.HugeChange)
	fmt.Println("  taste. Review effectiveness falls off a cliff somewhere past 400")
	fmt.Println("  lines: a 900-line change does not get twice the scrutiny of a 450-line")
	fmt.Println("  one, it gets less. Generated files are excluded, because counting a")
	fmt.Println("  protobuf regeneration as a large review teaches people to ignore the")
	fmt.Println("  warning.")
}

//
// 3. DESCRIPTION
//

func demoDescription() {
	section("3. Pull request descriptions")

	bad := prlint.PullRequest{
		Title:       "fixes",
		Description: "## What\n\nFixed the bug.\n",
	}

	good := prlint.PullRequest{
		Title: "fix(worker): claim events inside the transaction",
		Description: `## Why

Duplicate deliveries were writing two receipts for one order. The claim was
inserted in its own transaction, so two concurrent deliveries could both pass
the check before either committed.

## What

Move the claim INSERT into the same transaction as the handler's work, so the
unique index decides the winner. Adds Store.ProcessOnce; the old CheckSeen is
deleted.

## Test plan

- TestConcurrentDeliveriesProcessOnce: 8 goroutines, one event, asserts exactly
  one succeeds and seven get ErrAlreadyProcessed. Fails on the parent commit.
- go test -race ./internal/idempotency/...
- Ran the demo with -duplicate: 1 receipt for 2 deliveries.

## Risk

Low. The dedupe table gains rows at the same rate as before. If the claim ever
fails to roll back, an event would be silently skipped - covered by
TestFailedHandlerReleasesTheClaim. Rollback is a revert; no migration.
`,
	}

	for _, pullRequest := range []prlint.PullRequest{bad, good} {
		findings := prlint.CheckDescription(pullRequest)

		fmt.Printf("\n  %q -> %d finding(s)\n", truncate(pullRequest.Title, 60), len(findings))

		for _, finding := range findings {
			fmt.Printf("    [%s] %s\n", finding.Severity, finding.Message)
		}

		if len(findings) == 0 {
			fmt.Println("    ok - a reviewer can start reading the code immediately")
		}
	}

	fmt.Println()
	fmt.Println("  each required section answers a question the reviewer would otherwise")
	fmt.Println("  have to ask - and asking costs a round trip measured in hours, while")
	fmt.Println("  answering costs five minutes while the change is still in your head.")
}

//
// 4. CHECKLIST
//

func demoChecklist() {
	section("4. The review checklist, in cost order")

	current := review.Category(-1)

	for _, item := range review.Checklist() {
		if item.Category != current {
			current = item.Category

			fmt.Printf("\n  %s\n", strings.ToUpper(item.Category.String()))
		}

		fmt.Printf("    - %s\n", item.Question)
		fmt.Printf("      %s\n", item.Why)
	}

	fmt.Println()
	fmt.Println("  the ORDER is the useful part. A reviewer who starts with naming runs")
	fmt.Println("  out of attention before reaching the concurrency bug, and the author")
	fmt.Println("  gets three rounds of style comments on code that does not work.")
}

//
// 5. FEEDBACK
//

func demoFeedback() {
	section("5. Feedback: the same point, said twice")

	for _, rewrite := range review.Rewrites() {
		fmt.Printf("\n  before: %s\n", rewrite.Before)

		findings := review.CheckTone(rewrite.Before)

		for _, finding := range findings {
			fmt.Printf("          ^ %q %s\n", finding.Phrase, finding.Why)
		}

		fmt.Printf("  after:  %s\n", wrap(rewrite.After, 68, 10))
		fmt.Printf("  why:    %s\n", wrap(rewrite.Note, 68, 10))
	}

	fmt.Println()

	comment := review.Comment{
		File:     "internal/store/store.go",
		Line:     142,
		Category: review.Correctness,
		Blocking: true,
		Body:     "rows is not closed when Scan fails, so this leaks a connection per error.",
	}

	nit := review.Comment{
		File:     "internal/store/store.go",
		Line:     201,
		Category: review.Readability,
		Body:     "customerIDs reads better than ids here, since orders have ids too.",
	}

	fmt.Println("  and mark severity explicitly, every time:")
	fmt.Println("    " + comment.Format())
	fmt.Println("    " + nit.Format())
	fmt.Println()
	fmt.Println("  \"nit:\" is a protocol, not decoration: it tells the author this one is")
	fmt.Println("  optional and they may merge without it. Teams that use it consistently")
	fmt.Println("  argue less, because the author knows which comments are negotiable.")
}

func wrap(text string, width, indent int) string {
	words := strings.Fields(text)

	if len(words) == 0 {
		return ""
	}

	var (
		builder strings.Builder
		line    = words[0]
	)

	for _, word := range words[1:] {
		if len(line)+1+len(word) > width {
			builder.WriteString(line + "\n" + strings.Repeat(" ", indent))

			line = word

			continue
		}

		line += " " + word
	}

	builder.WriteString(line)

	return builder.String()
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}

	return value[:limit-3] + "..."
}

func section(title string) {
	underline := make([]byte, len(title))

	for i := range underline {
		underline[i] = '='
	}

	fmt.Printf("\n%s\n%s\n", title, underline)
}
