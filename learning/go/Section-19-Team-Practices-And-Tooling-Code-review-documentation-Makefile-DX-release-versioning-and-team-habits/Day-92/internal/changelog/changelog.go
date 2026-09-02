// Package changelog parses and validates a Keep a Changelog file.
//
// A changelog is written for the person deciding whether to upgrade. That
// makes it a different document from the git history, and the difference is
// the whole reason to keep one by hand:
//
//	git log      every change, ordered by when it landed, written for the team
//	CHANGELOG    user-visible changes, ordered by release, written for callers
//
// "Refactor the retry loop" belongs in the history and nowhere else. "Retries
// now stop after 30 seconds instead of retrying forever" belongs in the
// changelog, because somebody's timeout budget depends on it.
//
// The format (https://keepachangelog.com) is worth following because tooling
// can read it: this package, a release job, a docs site.
package changelog

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Categories are the sections Keep a Changelog defines.
//
// The list is closed on purpose. "Misc" and "Other" are where entries go to be
// ignored, and a reader scanning for breaking changes needs to know that
// Removed and Changed are the only two places they can hide.
var Categories = []string{"Added", "Changed", "Deprecated", "Removed", "Fixed", "Security"}

// UnreleasedHeading is the section every change lands in before a release.
const UnreleasedHeading = "Unreleased"

// Release is one version's entry.
type Release struct {
	// Version is the semantic version, without a leading "v", or
	// "Unreleased".
	Version string
	// Date is the release date in YYYY-MM-DD form; empty for Unreleased.
	Date string
	// Yanked marks a release pulled after publication.
	Yanked bool
	// Entries maps a category to its bullet points.
	Entries map[string][]string
	// Line is where the heading appears, for error messages.
	Line int
}

// Total returns how many entries the release has.
func (r Release) Total() int {
	total := 0

	for _, entries := range r.Entries {
		total += len(entries)
	}

	return total
}

// Changelog is a parsed file.
type Changelog struct {
	Title    string
	Releases []Release
}

// Unreleased returns the Unreleased section, if there is one.
func (c Changelog) Unreleased() (Release, bool) {
	for _, release := range c.Releases {
		if strings.EqualFold(release.Version, UnreleasedHeading) {
			return release, true
		}
	}

	return Release{}, false
}

// Latest returns the most recent released version.
func (c Changelog) Latest() (Release, bool) {
	for _, release := range c.Releases {
		if !strings.EqualFold(release.Version, UnreleasedHeading) {
			return release, true
		}
	}

	return Release{}, false
}

var (
	// ## [1.2.0] - 2026-08-30   /   ## [Unreleased]   /   ## [1.1.0] - ... [YANKED]
	releaseHeading = regexp.MustCompile(`^##\s+\[?([^\]\s]+)\]?(?:\s*-\s*(\d{4}-\d{2}-\d{2}))?(.*)$`)
	// ### Added
	categoryHeading = regexp.MustCompile(`^###\s+(.+?)\s*$`)
	bulletPoint     = regexp.MustCompile(`^\s*[-*]\s+(.+?)\s*$`)
	semverPattern   = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.\-]+))?$`)
)

// Parse reads a changelog from text.
func Parse(content string) Changelog {
	var (
		result   Changelog
		current  *Release
		category string
	)

	for number, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimRight(line, " \t\r")

		if strings.HasPrefix(trimmed, "# ") && result.Title == "" {
			result.Title = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))

			continue
		}

		if match := releaseHeading.FindStringSubmatch(trimmed); match != nil && strings.HasPrefix(trimmed, "## ") {
			result.Releases = append(result.Releases, Release{
				Version: match[1],
				Date:    match[2],
				Yanked:  strings.Contains(strings.ToUpper(match[3]), "YANKED"),
				Entries: make(map[string][]string),
				Line:    number + 1,
			})

			current = &result.Releases[len(result.Releases)-1]
			category = ""

			continue
		}

		if match := categoryHeading.FindStringSubmatch(trimmed); match != nil && current != nil {
			category = strings.TrimSpace(match[1])

			continue
		}

		if match := bulletPoint.FindStringSubmatch(trimmed); match != nil && current != nil && category != "" {
			current.Entries[category] = append(current.Entries[category], match[1])
		}
	}

	return result
}

// Load reads a changelog from a file.
func Load(path string) (Changelog, error) {
	content, err := os.ReadFile(path) //nolint:gosec // the path comes from the caller
	if err != nil {
		return Changelog{}, fmt.Errorf("read %s: %w", path, err)
	}

	return Parse(string(content)), nil
}

// Problem is one validation failure.
type Problem struct {
	Rule    string
	Version string
	Line    int
	Message string
}

// String renders a problem for a terminal report.
func (p Problem) String() string {
	if p.Version == "" {
		return fmt.Sprintf("%-22s %s", p.Rule, p.Message)
	}

	return fmt.Sprintf("%-22s [%s] %s", p.Rule, p.Version, p.Message)
}

// Validate checks the structure a release tool depends on.
func (c Changelog) Validate() []Problem {
	var problems []Problem

	if len(c.Releases) == 0 {
		return []Problem{{Rule: "empty", Message: "no releases found - is this a Keep a Changelog file?"}}
	}

	if _, found := c.Unreleased(); !found {
		problems = append(problems, Problem{
			Rule: "missing_unreleased",
			Message: "no Unreleased section: every change needs somewhere to land, " +
				"and its absence is why changelogs get written from git log at release time",
		})
	}

	valid := make(map[string]bool, len(Categories))

	for _, category := range Categories {
		valid[category] = true
	}

	var versions []Release

	for _, release := range c.Releases {
		unreleased := strings.EqualFold(release.Version, UnreleasedHeading)

		if !unreleased {
			if !semverPattern.MatchString(release.Version) {
				problems = append(problems, Problem{
					Rule: "bad_version", Version: release.Version, Line: release.Line,
					Message: "not a semantic version: a release tool cannot order or compare this",
				})
			} else {
				versions = append(versions, release)
			}

			if release.Date == "" {
				problems = append(problems, Problem{
					Rule: "missing_date", Version: release.Version, Line: release.Line,
					Message: "no date: readers use it to tell whether they are years behind",
				})
			}
		}

		for category := range release.Entries {
			if !valid[category] {
				problems = append(problems, Problem{
					Rule: "unknown_category", Version: release.Version, Line: release.Line,
					Message: fmt.Sprintf("unknown category %q; use one of %s",
						category, strings.Join(Categories, ", ")),
				})
			}
		}

		if !unreleased && release.Total() == 0 && !release.Yanked {
			problems = append(problems, Problem{
				Rule: "empty_release", Version: release.Version, Line: release.Line,
				Message: "released with no entries",
			})
		}
	}

	// Newest first is the convention, and a release tool that reads "the
	// latest version" off the top depends on it.
	for i := 1; i < len(versions); i++ {
		if CompareVersions(versions[i-1].Version, versions[i].Version) < 0 {
			problems = append(problems, Problem{
				Rule: "out_of_order", Version: versions[i].Version, Line: versions[i].Line,
				Message: fmt.Sprintf("%s appears after %s; releases go newest first",
					versions[i].Version, versions[i-1].Version),
			})
		}
	}

	sort.Slice(problems, func(i, j int) bool { return problems[i].Line < problems[j].Line })

	return problems
}

// CompareVersions orders two semantic versions: -1, 0 or 1.
//
// Pre-release versions sort BEFORE their release (1.0.0-rc.1 < 1.0.0), which
// is what the semver specification requires and the opposite of what a string
// comparison does.
func CompareVersions(a, b string) int {
	aMatch := semverPattern.FindStringSubmatch(strings.TrimPrefix(a, "v"))
	bMatch := semverPattern.FindStringSubmatch(strings.TrimPrefix(b, "v"))

	if aMatch == nil || bMatch == nil {
		return strings.Compare(a, b)
	}

	for i := 1; i <= 3; i++ {
		left, _ := strconv.Atoi(aMatch[i])
		right, _ := strconv.Atoi(bMatch[i])

		if left != right {
			if left < right {
				return -1
			}

			return 1
		}
	}

	switch {
	case aMatch[4] == bMatch[4]:
		return 0
	case aMatch[4] == "":
		// No pre-release suffix means this IS the release, which outranks any
		// release candidate of the same number.
		return 1
	case bMatch[4] == "":
		return -1
	default:
		return strings.Compare(aMatch[4], bMatch[4])
	}
}
