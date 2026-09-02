// Package repoaudit checks whether a repository is ready for someone else to
// work in.
//
// The question it answers is narrow and useful: can a competent stranger clone
// this, understand what it is, build it, run the tests, make a change and open
// a pull request - without asking anyone anything?
//
// Every check below corresponds to a question that otherwise costs an
// interruption. None of them is about code quality; a repository can be
// beautifully written and still be unusable by anyone but its author.
package repoaudit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Severity separates what blocks a newcomer from what merely slows them down.
type Severity int

const (
	// Nice is a polish item.
	Nice Severity = iota
	// Recommended is expected in a team repository.
	Recommended
	// Required means a newcomer is blocked without it.
	Required
)

// String names the severity.
func (s Severity) String() string {
	return [...]string{"nice", "recommended", "required"}[s]
}

// Check is one thing to look for.
type Check struct {
	// Name is what is being checked.
	Name string
	// Paths are the accepted locations, in order of preference.
	Paths []string
	// Sections are headings the file must contain, lower-cased.
	Sections []string
	// Severity says how much its absence costs.
	Severity Severity
	// Question is what a newcomer cannot answer without it.
	Question string
}

// Finding is one gap.
type Finding struct {
	Check    Check
	Missing  bool
	Path     string
	Absent   []string // sections that are missing from an existing file
	Severity Severity
}

// String renders a finding for a terminal report.
func (f Finding) String() string {
	if f.Missing {
		return fmt.Sprintf("%-13s %-22s missing (%s) - %s",
			f.Severity, f.Check.Name, strings.Join(f.Check.Paths, " or "), f.Check.Question)
	}

	return fmt.Sprintf("%-13s %-22s %s is missing section(s): %s",
		f.Severity, f.Check.Name, f.Path, strings.Join(f.Absent, ", "))
}

// DefaultChecks is what a team repository is expected to carry.
func DefaultChecks() []Check {
	return []Check{
		{
			Name:     "README",
			Paths:    []string{"README.md"},
			Sections: []string{"what", "quick start", "how it works"},
			Severity: Required,
			Question: "what is this, and how do I run it?",
		},
		{
			Name:     "architecture",
			Paths:    []string{"docs/ARCHITECTURE.md", "ARCHITECTURE.md"},
			Sections: []string{"decisions"},
			Severity: Recommended,
			Question: "why is it built this way, and what was deliberately left out?",
		},
		{
			Name:     "contributing",
			Paths:    []string{"docs/CONTRIBUTING.md", "CONTRIBUTING.md"},
			Sections: []string{"setup", "tests"},
			Severity: Required,
			Question: "how do I set up, and what must pass before I push?",
		},
		{
			Name:     "changelog",
			Paths:    []string{"CHANGELOG.md"},
			Sections: []string{"unreleased"},
			Severity: Recommended,
			Question: "what changed between the version I have and this one?",
		},
		{
			Name:     "makefile",
			Paths:    []string{"Makefile"},
			Severity: Required,
			Question: "what are the commands? (see makefilelint for the targets themselves)",
		},
		{
			Name:     "pull request template",
			Paths:    []string{".github/pull_request_template.md", ".github/PULL_REQUEST_TEMPLATE.md"},
			Severity: Recommended,
			Question: "what does this team expect in a pull request description?",
		},
		{
			Name:     "CI workflow",
			Paths:    []string{".github/workflows/ci.yml", ".github/workflows/ci.yaml"},
			Severity: Recommended,
			Question: "what runs on my pull request, and can I run it locally?",
		},
		{
			Name:     "git hooks",
			Paths:    []string{".githooks/pre-commit"},
			Severity: Nice,
			Question: "are there checks I can run before committing?",
		},
		{
			Name:     "licence",
			Paths:    []string{"LICENSE", "LICENSE.md", "LICENCE"},
			Severity: Nice,
			Question: "may I use this, and on what terms?",
		},
	}
}

// Report is the outcome of an audit.
type Report struct {
	Root     string
	Findings []Finding
	Present  []string
}

// Audit runs every check against a directory.
func Audit(root string, checks []Check) (Report, error) {
	report := Report{Root: root}

	for _, check := range checks {
		path, found := locate(root, check.Paths)

		if !found {
			report.Findings = append(report.Findings, Finding{
				Check: check, Missing: true, Severity: check.Severity,
			})

			continue
		}

		report.Present = append(report.Present, filepath.ToSlash(path))

		if len(check.Sections) == 0 {
			continue
		}

		content, err := os.ReadFile(filepath.Join(root, path)) //nolint:gosec // path comes from the check list
		if err != nil {
			return Report{}, fmt.Errorf("read %s: %w", path, err)
		}

		lowered := strings.ToLower(string(content))

		var absent []string

		for _, section := range check.Sections {
			if !strings.Contains(lowered, section) {
				absent = append(absent, section)
			}
		}

		if len(absent) > 0 {
			// A file that exists but does not answer the question is a
			// weaker finding than a missing file: it is at least a place to
			// put the answer.
			severity := check.Severity

			if severity > Recommended {
				severity = Recommended
			}

			report.Findings = append(report.Findings, Finding{
				Check: check, Path: filepath.ToSlash(path), Absent: absent, Severity: severity,
			})
		}
	}

	sort.Slice(report.Findings, func(i, j int) bool {
		if report.Findings[i].Severity != report.Findings[j].Severity {
			return report.Findings[i].Severity > report.Findings[j].Severity
		}

		return report.Findings[i].Check.Name < report.Findings[j].Check.Name
	})

	sort.Strings(report.Present)

	return report, nil
}

func locate(root string, paths []string) (string, bool) {
	for _, path := range paths {
		if _, err := os.Stat(filepath.Join(root, path)); err == nil {
			return path, true
		}
	}

	return "", false
}

// Blocked reports whether anything required is missing.
func (r Report) Blocked() bool {
	for _, finding := range r.Findings {
		if finding.Severity == Required {
			return true
		}
	}

	return false
}

// Score is the share of checks that passed, as a percentage.
//
// It is a headline number, not a target. A repository can score 100 and still
// be impenetrable - the score says the files exist, not that they are true.
func (r Report) Score(checks []Check) int {
	if len(checks) == 0 {
		return 100
	}

	failed := 0

	for _, finding := range r.Findings {
		if finding.Missing {
			failed++
		}
	}

	return (len(checks) - failed) * 100 / len(checks)
}

// Counts returns findings by severity.
func (r Report) Counts() map[Severity]int {
	counts := make(map[Severity]int)

	for _, finding := range r.Findings {
		counts[finding.Severity]++
	}

	return counts
}
