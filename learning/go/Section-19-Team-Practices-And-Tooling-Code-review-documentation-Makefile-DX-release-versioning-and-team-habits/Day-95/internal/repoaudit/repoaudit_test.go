package repoaudit_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-95/internal/repoaudit"
)

func newRepo(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()

	for path, content := range files {
		full := filepath.Join(root, path)

		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}

		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	return root
}

func audit(t *testing.T, root string) repoaudit.Report {
	t.Helper()

	report, err := repoaudit.Audit(root, repoaudit.DefaultChecks())
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}

	return report
}

// This day's own repository has to pass its own audit.
func TestThisRepositoryPassesItsRequiredChecks(t *testing.T) {
	report := audit(t, filepath.Join("..", ".."))

	for _, finding := range report.Findings {
		if finding.Severity == repoaudit.Required {
			t.Errorf("%s", finding)
		}
	}

	if report.Blocked() {
		t.Error("a required file is missing")
	}
}

func TestEmptyRepositoryIsBlocked(t *testing.T) {
	report := audit(t, newRepo(t, map[string]string{"main.go": "package main\n"}))

	if !report.Blocked() {
		t.Error("a repository with no README or Makefile should be blocked")
	}

	if score := report.Score(repoaudit.DefaultChecks()); score != 0 {
		t.Errorf("score = %d, want 0", score)
	}

	// Every finding names the question a newcomer cannot answer.
	for _, finding := range report.Findings {
		if finding.Check.Question == "" {
			t.Errorf("%s has no question attached", finding.Check.Name)
		}
	}
}

// A file that exists but does not answer its question is a weaker finding than
// a missing one: it is at least a place to put the answer.
func TestFileMissingSectionsIsNotRequired(t *testing.T) {
	root := newRepo(t, map[string]string{
		"README.md":            "# Thing\n\nIt does things.\n",
		"Makefile":             "all:\n\techo hi\n",
		"docs/CONTRIBUTING.md": "# Contributing\n\nSetup: run make. Tests: make test.\n",
	})

	report := audit(t, root)

	for _, finding := range report.Findings {
		if finding.Check.Name != "README" {
			continue
		}

		if finding.Missing {
			t.Error("the README exists but was reported as missing")
		}

		if finding.Severity == repoaudit.Required {
			t.Error("an incomplete README should not block; it is a place to put the answer")
		}

		if len(finding.Absent) == 0 {
			t.Error("no missing sections reported for a README with none of them")
		}
	}
}

func TestAlternativePathsAreAccepted(t *testing.T) {
	// ARCHITECTURE.md at the root rather than under docs/.
	root := newRepo(t, map[string]string{
		"ARCHITECTURE.md": "# Architecture\n\n## Decisions\n\nWe chose X because Y.\n",
	})

	report := audit(t, root)

	for _, finding := range report.Findings {
		if finding.Check.Name == "architecture" && finding.Missing {
			t.Error("ARCHITECTURE.md at the root was not accepted")
		}
	}

	found := false

	for _, present := range report.Present {
		if present == "ARCHITECTURE.md" {
			found = true
		}
	}

	if !found {
		t.Errorf("present = %v, want ARCHITECTURE.md", report.Present)
	}
}

func TestScoreCountsOnlyMissingFiles(t *testing.T) {
	checks := []repoaudit.Check{
		{Name: "readme", Paths: []string{"README.md"}, Severity: repoaudit.Required},
		{Name: "licence", Paths: []string{"LICENSE"}, Severity: repoaudit.Nice},
	}

	root := newRepo(t, map[string]string{"README.md": "# Thing\n"})

	report, err := repoaudit.Audit(root, checks)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}

	if score := report.Score(checks); score != 50 {
		t.Errorf("score = %d, want 50", score)
	}
}

func TestFindingsAreOrderedBySeverity(t *testing.T) {
	report := audit(t, newRepo(t, map[string]string{"main.go": "package main\n"}))

	for i := 1; i < len(report.Findings); i++ {
		if report.Findings[i].Severity > report.Findings[i-1].Severity {
			t.Fatalf("findings are not ordered by severity: %s before %s",
				report.Findings[i-1].Severity, report.Findings[i].Severity)
		}
	}
}

func TestFindingStringNamesTheGap(t *testing.T) {
	report := audit(t, newRepo(t, map[string]string{"main.go": "package main\n"}))

	if len(report.Findings) == 0 {
		t.Fatal("no findings for an empty repository")
	}

	rendered := report.Findings[0].String()

	if !strings.Contains(rendered, "missing") {
		t.Errorf("finding = %q", rendered)
	}
}

func TestCounts(t *testing.T) {
	report := audit(t, newRepo(t, map[string]string{"main.go": "package main\n"}))

	counts := report.Counts()

	if counts[repoaudit.Required] == 0 {
		t.Errorf("counts = %v, want at least one required finding", counts)
	}
}
