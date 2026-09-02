package prlint_test

import (
	"strings"
	"testing"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-91/internal/prlint"
)

func hasRule(findings prlint.Findings, rule string) bool {
	for _, finding := range findings {
		if finding.Rule == rule {
			return true
		}
	}

	return false
}

func TestGoodCommitPassesCleanly(t *testing.T) {
	commit := prlint.Commit{
		Subject: "fix(store): close rows on the error path",
		Body:    "Scan failing left the connection open, so a burst of\nerrors exhausted the pool.",
	}

	if findings := prlint.CheckCommit(commit); len(findings) != 0 {
		t.Errorf("expected no findings, got %v", findings)
	}
}

func TestCommitRules(t *testing.T) {
	cases := []struct {
		name   string
		commit prlint.Commit
		rule   string
	}{
		{"empty subject", prlint.Commit{Subject: "   "}, "commit/empty"},
		{"not conventional", prlint.Commit{Subject: "wip"}, "commit/conventional"},
		{
			"too long",
			prlint.Commit{Subject: "feat(api): " + strings.Repeat("x", 80)},
			"commit/subject-length",
		},
		{"trailing period", prlint.Commit{Subject: "fix(api): close the body."}, "commit/trailing-period"},
		{"past tense", prlint.Commit{Subject: "fix(api): added a retry"}, "commit/imperative"},
		{
			"long body line",
			prlint.Commit{
				Subject: "fix(api): close the body",
				Body:    strings.Repeat("x", 100),
			},
			"commit/body-wrap",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			findings := prlint.CheckCommit(testCase.commit)

			if !hasRule(findings, testCase.rule) {
				t.Errorf("expected rule %s, got %v", testCase.rule, findings)
			}
		})
	}
}

// The imperative check must look past the "type(scope): " prefix, or every
// conventional commit starting with "feat" would be inspected for the wrong
// word.
func TestImperativeCheckLooksAfterThePrefix(t *testing.T) {
	if findings := prlint.CheckCommit(prlint.Commit{Subject: "feat(api): add a retry"}); len(findings) != 0 {
		t.Errorf("expected no findings for an imperative subject, got %v", findings)
	}
}

// A URL in a commit body cannot be wrapped, so it must not be flagged - a rule
// that fires on something unfixable trains people to ignore the tool.
func TestLongURLsInABodyAreNotFlagged(t *testing.T) {
	commit := prlint.Commit{
		Subject: "fix(api): pin the action",
		Body:    "See https://" + strings.Repeat("a", 90) + "/issues/1",
	}

	if hasRule(prlint.CheckCommit(commit), "commit/body-wrap") {
		t.Error("a URL that cannot be wrapped should not be flagged")
	}
}

func TestEmptySubjectIsBlockingAndStopsThere(t *testing.T) {
	findings := prlint.CheckCommit(prlint.Commit{Subject: ""})

	if len(findings) != 1 {
		t.Fatalf("expected exactly one finding for an empty subject, got %v", findings)
	}

	if !findings.Blocking() {
		t.Error("an empty commit subject should block")
	}
}

//
// DESCRIPTIONS
//

const completeDescription = `## Why

The dedupe check ran in its own transaction, so two concurrent deliveries
could both pass it.

## What

Move the claim into the handler's transaction.

## Test plan

go test -race ./internal/idempotency/... - TestConcurrentDeliveriesProcessOnce
fails on the parent commit.

## Risk

Low; rollback is a revert, no migration involved.
`

func TestCompleteDescriptionPasses(t *testing.T) {
	pullRequest := prlint.PullRequest{
		Title:       "fix(worker): claim events inside the transaction",
		Description: completeDescription,
	}

	if findings := prlint.CheckDescription(pullRequest); len(findings) != 0 {
		t.Errorf("expected no findings, got %v", findings)
	}
}

func TestMissingSectionBlocks(t *testing.T) {
	pullRequest := prlint.PullRequest{
		Title:       "fix(worker): claim events inside the transaction",
		Description: "## Why\n\nBecause the old one raced.\n\n## What\n\nMoved the claim.\n",
	}

	findings := prlint.CheckDescription(pullRequest)

	if !findings.Blocking() {
		t.Fatalf("a missing Test plan should block, got %v", findings)
	}

	if count := findings.Count(prlint.Blocking); count != 2 {
		t.Errorf("blocking findings = %d, want 2 (Test plan and Risk)", count)
	}
}

// "## Test plan\nn/a" satisfies a naive "does the heading exist" check and
// tells the reviewer nothing, which is why the content length is checked too.
func TestPlaceholderSectionIsFlagged(t *testing.T) {
	pullRequest := prlint.PullRequest{
		Title:       "fix(worker): claim events inside the transaction",
		Description: "## Why\n\nRaces.\n\n## What\n\nFix.\n\n## Test plan\n\nn/a\n\n## Risk\n\nnone\n",
	}

	findings := prlint.CheckDescription(pullRequest)

	if findings.Count(prlint.Warning) == 0 {
		t.Errorf("expected placeholder sections to be flagged, got %v", findings)
	}
}

func TestHeadingMatchingIsCaseInsensitive(t *testing.T) {
	description := strings.ReplaceAll(completeDescription, "## Test plan", "### TEST PLAN")

	pullRequest := prlint.PullRequest{
		Title:       "fix(worker): claim events inside the transaction",
		Description: description,
	}

	if findings := prlint.CheckDescription(pullRequest); len(findings) != 0 {
		t.Errorf("headings should match case-insensitively at any level, got %v", findings)
	}
}

func TestVagueTitlesAreFlagged(t *testing.T) {
	for _, title := range []string{"", "wip", "fixes", "stuff"} {
		pullRequest := prlint.PullRequest{Title: title, Description: completeDescription}

		findings := prlint.CheckDescription(pullRequest)

		if len(findings) == 0 {
			t.Errorf("title %q should be flagged", title)
		}
	}
}

func TestParseSectionsSplitsOnHeadings(t *testing.T) {
	sections := prlint.ParseSections(completeDescription)

	for _, want := range []string{"why", "what", "test plan", "risk"} {
		if _, found := sections[want]; !found {
			t.Errorf("section %q not parsed; got %v", want, keys(sections))
		}
	}

	if !strings.Contains(sections["risk"], "rollback is a revert") {
		t.Errorf("risk section = %q", sections["risk"])
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))

	for key := range m {
		out = append(out, key)
	}

	return out
}

func TestFindingsSummary(t *testing.T) {
	findings := prlint.Findings{
		{Severity: prlint.Nit},
		{Severity: prlint.Warning},
		{Severity: prlint.Warning},
	}

	if findings.Blocking() {
		t.Error("no blocking findings, but Blocking() reported true")
	}

	if got := findings.Count(prlint.Warning); got != 2 {
		t.Errorf("warnings = %d, want 2", got)
	}

	findings = append(findings, prlint.Finding{Severity: prlint.Blocking})

	if !findings.Blocking() {
		t.Error("Blocking() should report true once a blocking finding is present")
	}
}
