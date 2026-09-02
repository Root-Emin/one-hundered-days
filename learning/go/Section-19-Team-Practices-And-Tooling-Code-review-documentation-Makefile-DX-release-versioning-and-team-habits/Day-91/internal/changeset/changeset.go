// Package changeset measures the diff itself.
//
// "Keep pull requests small" is the most repeated review advice there is, and
// the least actionable, because nobody knows how big theirs is until a
// reviewer complains. A number fixes that.
//
// The size thresholds here are not arbitrary. Review effectiveness falls off a
// cliff somewhere around 400 changed lines: past that, reviewers skim, defect
// detection drops, and the review becomes a rubber stamp that costs two days.
// A 900-line pull request does not get twice the scrutiny of a 450-line one -
// it gets less.
package changeset

import (
	"bufio"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
)

// FileChange is one file in the diff.
type FileChange struct {
	Path      string
	Added     int
	Removed   int
	Binary    bool
	Generated bool
}

func (f FileChange) Total() int {
	return f.Added + f.Removed
}

// IsTest reports whether this file is a Go test.
func (f FileChange) IsTest() bool {
	return strings.HasSuffix(f.Path, "_test.go")
}

// IsGo reports whether this is Go source that is not a test.
func (f FileChange) IsGo() bool {
	return strings.HasSuffix(f.Path, ".go") && !f.IsTest()
}

// Changeset is the whole diff.
type Changeset struct {
	Files []FileChange
}

// ParseNumstat reads the output of `git diff --numstat`.
//
// Each line is: added<TAB>removed<TAB>path. A binary file reports "-" for both
// counts, which is why the numbers are parsed rather than assumed.
func ParseNumstat(output string) (Changeset, error) {
	var changeset Changeset

	scanner := bufio.NewScanner(strings.NewReader(output))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			return Changeset{}, fmt.Errorf("malformed numstat line %q", line)
		}

		change := FileChange{Path: fields[2]}

		if fields[0] == "-" && fields[1] == "-" {
			change.Binary = true
		} else {
			added, err := strconv.Atoi(fields[0])
			if err != nil {
				return Changeset{}, fmt.Errorf("added count in %q: %w", line, err)
			}

			removed, err := strconv.Atoi(fields[1])
			if err != nil {
				return Changeset{}, fmt.Errorf("removed count in %q: %w", line, err)
			}

			change.Added = added
			change.Removed = removed
		}

		change.Generated = isGenerated(change.Path)

		changeset.Files = append(changeset.Files, change)
	}

	if err := scanner.Err(); err != nil {
		return Changeset{}, fmt.Errorf("read numstat: %w", err)
	}

	return changeset, nil
}

// isGenerated recognises files a human did not write.
//
// They are excluded from the size count on purpose: a 30,000-line protobuf
// regeneration is not a large review, and counting it as one teaches people to
// ignore the size warning.
func isGenerated(filePath string) bool {
	base := path.Base(filePath)

	switch {
	case strings.HasSuffix(base, ".pb.go"), strings.HasSuffix(base, "_grpc.pb.go"):
		return true
	case base == "go.sum", base == "package-lock.json", base == "yarn.lock":
		return true
	case strings.Contains(filePath, "/gen/"), strings.HasPrefix(filePath, "gen/"):
		return true
	case strings.Contains(filePath, "/vendor/"), strings.HasPrefix(filePath, "vendor/"):
		return true
	default:
		return false
	}
}

// Stats summarises a changeset.
type Stats struct {
	Files          int
	Added          int
	Removed        int
	Total          int
	GeneratedFiles int
	GeneratedLines int
	TestFiles      int
	GoFiles        int
}

func (c Changeset) Stats() Stats {
	stats := Stats{}

	for _, file := range c.Files {
		if file.Generated {
			stats.GeneratedFiles++
			stats.GeneratedLines += file.Total()

			continue
		}

		stats.Files++
		stats.Added += file.Added
		stats.Removed += file.Removed
		stats.Total += file.Total()

		if file.IsTest() {
			stats.TestFiles++
		}

		if file.IsGo() {
			stats.GoFiles++
		}
	}

	return stats
}

// Size thresholds, in reviewable (non-generated) lines.
const (
	// SmallChange reviews in one sitting with full attention.
	SmallChange = 200
	// LargeChange is where review quality starts falling off.
	LargeChange = 400
	// HugeChange will be skimmed, whatever anyone claims.
	HugeChange = 1000
)

// Verdict is the review-effort assessment.
type Verdict struct {
	Stats    Stats
	Findings []string
	Warnings []string
}

// riskyPaths are files where a mistake is expensive and a reviewer should be
// deliberately chosen rather than assigned round-robin.
var riskyPaths = []struct {
	match  func(string) bool
	reason string
}{
	{
		match:  func(p string) bool { return strings.Contains(p, "migration") },
		reason: "touches migrations: they run once, against production data, and rolling one back is not automatic",
	},
	{
		match:  func(p string) bool { return path.Base(p) == "go.mod" },
		reason: "changes dependencies: every new module is code you now ship and must patch",
	},
	{
		match: func(p string) bool {
			return strings.Contains(p, "auth") || strings.Contains(p, "token") ||
				strings.Contains(p, "password") || strings.Contains(p, "crypto")
		},
		reason: "touches authentication or crypto: needs a reviewer who knows the threat model",
	},
	{
		match: func(p string) bool {
			return strings.HasPrefix(p, ".github/workflows/") || path.Base(p) == "Dockerfile"
		},
		reason: "changes the build or deploy pipeline: it runs with credentials nobody else has",
	},
}

// Review assesses a changeset for reviewability.
func (c Changeset) Review() Verdict {
	verdict := Verdict{Stats: c.Stats()}

	switch {
	case verdict.Stats.Total > HugeChange:
		verdict.Findings = append(verdict.Findings, fmt.Sprintf(
			"%d reviewable lines: this will be skimmed, not reviewed. Split it - "+
				"a refactor commit, then the behaviour change", verdict.Stats.Total))

	case verdict.Stats.Total > LargeChange:
		verdict.Warnings = append(verdict.Warnings, fmt.Sprintf(
			"%d reviewable lines: past ~%d, defect detection drops sharply. "+
				"Consider splitting", verdict.Stats.Total, LargeChange))
	}

	// Code changed and no test changed. A heuristic, not a law - but the
	// question "how was this verified?" is worth asking every time.
	if verdict.Stats.GoFiles > 0 && verdict.Stats.TestFiles == 0 {
		verdict.Findings = append(verdict.Findings,
			"Go code changed but no _test.go file did: what proves this works, "+
				"and what would catch it breaking?")
	}

	seen := make(map[string]bool)

	for _, file := range c.Files {
		if file.Generated {
			continue
		}

		for _, risky := range riskyPaths {
			if !risky.match(file.Path) || seen[risky.reason] {
				continue
			}

			seen[risky.reason] = true

			verdict.Warnings = append(verdict.Warnings, fmt.Sprintf("%s %s", file.Path, risky.reason))
		}
	}

	sort.Strings(verdict.Warnings)

	if verdict.Stats.GeneratedFiles > 0 {
		verdict.Warnings = append(verdict.Warnings, fmt.Sprintf(
			"%d generated file(s), %d lines, excluded from the size count",
			verdict.Stats.GeneratedFiles, verdict.Stats.GeneratedLines))
	}

	return verdict
}

// Label is a one-word size for the pull request, the kind a bot adds.
func (s Stats) Label() string {
	switch {
	case s.Total <= 50:
		return "size/XS"
	case s.Total <= SmallChange:
		return "size/S"
	case s.Total <= LargeChange:
		return "size/M"
	case s.Total <= HugeChange:
		return "size/L"
	default:
		return "size/XL"
	}
}
