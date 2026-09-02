// Package semver parses semantic versions and derives the next one from a
// list of Conventional Commits.
//
// The number is a promise, not a label. MAJOR.MINOR.PATCH tells a caller
// whether they can upgrade without reading anything:
//
//	PATCH  bug fixes; upgrade blind
//	MINOR  new features, nothing removed; upgrade blind
//	MAJOR  something they depend on changed or is gone; read the notes
//
// Breaking that promise once teaches every consumer to pin an exact version and
// read every diff, which costs them the benefit of versioning entirely.
//
// Deriving the bump from commit messages - rather than someone's judgement at
// release time - is what keeps the promise honest. A commit marked "feat!" is
// a major bump whether or not anyone remembers it three weeks later.
package semver

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a parsed semantic version.
type Version struct {
	Major      int
	Minor      int
	Patch      int
	PreRelease string
	Build      string
}

// ErrInvalidVersion means the string is not a semantic version.
var ErrInvalidVersion = errors.New("invalid semantic version")

var versionPattern = regexp.MustCompile(
	`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.\-]+))?(?:\+([0-9A-Za-z.\-]+))?$`)

// Parse reads a version, with or without a leading "v".
func Parse(value string) (Version, error) {
	match := versionPattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return Version{}, fmt.Errorf("%q: %w", value, ErrInvalidVersion)
	}

	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	patch, _ := strconv.Atoi(match[3])

	return Version{
		Major:      major,
		Minor:      minor,
		Patch:      patch,
		PreRelease: match[4],
		Build:      match[5],
	}, nil
}

// String renders the version with a leading "v", the form git tags use.
func (v Version) String() string {
	out := fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)

	if v.PreRelease != "" {
		out += "-" + v.PreRelease
	}

	if v.Build != "" {
		out += "+" + v.Build
	}

	return out
}

// IsPreRelease reports whether this version carries a pre-release suffix.
func (v Version) IsPreRelease() bool {
	return v.PreRelease != ""
}

// IsUnstable reports whether the version is below 1.0.0.
//
// Below 1.0.0 the promise is explicitly suspended: the specification allows
// anything to change at any time, which is why "0.x forever" is a way of never
// committing to an API.
func (v Version) IsUnstable() bool {
	return v.Major == 0
}

// Compare orders two versions: -1, 0 or 1.
//
// Build metadata is ignored, as the specification requires: 1.0.0+build.1 and
// 1.0.0+build.2 are the same version.
func Compare(a, b Version) int {
	for _, pair := range [][2]int{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if pair[0] != pair[1] {
			if pair[0] < pair[1] {
				return -1
			}

			return 1
		}
	}

	switch {
	case a.PreRelease == b.PreRelease:
		return 0
	case a.PreRelease == "":
		// A release outranks any pre-release of the same number.
		return 1
	case b.PreRelease == "":
		return -1
	default:
		return comparePreRelease(a.PreRelease, b.PreRelease)
	}
}

// comparePreRelease compares dot-separated identifiers, numerically where both
// are numeric - so rc.2 sorts after rc.10 only if you compare as strings, which
// is exactly the bug this avoids.
func comparePreRelease(a, b string) int {
	left := strings.Split(a, ".")
	right := strings.Split(b, ".")

	for i := 0; i < len(left) && i < len(right); i++ {
		leftNumber, leftErr := strconv.Atoi(left[i])
		rightNumber, rightErr := strconv.Atoi(right[i])

		if leftErr == nil && rightErr == nil {
			if leftNumber != rightNumber {
				if leftNumber < rightNumber {
					return -1
				}

				return 1
			}

			continue
		}

		if compared := strings.Compare(left[i], right[i]); compared != 0 {
			return compared
		}
	}

	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	default:
		return 0
	}
}

//
// DERIVING THE NEXT VERSION
//

// Bump is the size of a version increment.
type Bump int

const (
	// None means nothing user-visible changed.
	None Bump = iota
	// Patch is a bug fix.
	Patch
	// Minor is a backwards-compatible feature.
	Minor
	// Major is a breaking change.
	Major
)

// String names the bump.
func (b Bump) String() string {
	return [...]string{"none", "patch", "minor", "major"}[b]
}

// Commit is one Conventional Commit.
type Commit struct {
	Hash     string
	Type     string // feat, fix, docs, ...
	Scope    string
	Breaking bool
	Subject  string
	Body     string
	Author   string
}

var conventionalPattern = regexp.MustCompile(
	`^(?P<type>[a-z]+)(?:\((?P<scope>[^)]+)\))?(?P<breaking>!)?:\s*(?P<subject>.+)$`)

// ParseCommit reads a Conventional Commit subject and body.
//
// A message that does not follow the convention is not an error: it is a
// commit with no type, which contributes no version bump and appears under
// "other" in the notes. Rejecting it would mean a release tool that refuses to
// run on a real repository.
func ParseCommit(hash, subject, body, author string) Commit {
	commit := Commit{Hash: hash, Subject: strings.TrimSpace(subject), Body: body, Author: author}

	match := conventionalPattern.FindStringSubmatch(commit.Subject)
	if match == nil {
		return commit
	}

	for i, name := range conventionalPattern.SubexpNames() {
		switch name {
		case "type":
			commit.Type = match[i]
		case "scope":
			commit.Scope = match[i]
		case "breaking":
			commit.Breaking = match[i] == "!"
		case "subject":
			commit.Subject = match[i]
		}
	}

	// The footer form, which is the one the specification requires tools to
	// support: "BREAKING CHANGE: <description>" in the body.
	if strings.Contains(body, "BREAKING CHANGE:") || strings.Contains(body, "BREAKING-CHANGE:") {
		commit.Breaking = true
	}

	return commit
}

// BreakingNote returns the text after "BREAKING CHANGE:", which is where the
// migration instructions live.
func (c Commit) BreakingNote() string {
	for _, marker := range []string{"BREAKING CHANGE:", "BREAKING-CHANGE:"} {
		_, after, found := strings.Cut(c.Body, marker)
		if !found {
			continue
		}

		// Everything after the marker, including later paragraphs: the
		// migration instructions usually come after a blank line, and cutting
		// at the first one throws away the half that tells the reader what to
		// do.
		return strings.TrimSpace(after)
	}

	return ""
}

// BumpFor returns the increment a set of commits requires.
func BumpFor(commits []Commit) Bump {
	bump := None

	for _, commit := range commits {
		switch {
		case commit.Breaking:
			return Major

		case commit.Type == "feat":
			if bump < Minor {
				bump = Minor
			}

		case commit.Type == "fix", commit.Type == "perf", commit.Type == "revert":
			if bump < Patch {
				bump = Patch
			}
		}
	}

	return bump
}

// Next applies a bump to a version.
//
// The 0.x rule is the one people get wrong: below 1.0.0 a breaking change
// bumps the MINOR, not the major, because 0.y.z is defined as unstable and Go
// modules treat every 0.x as compatible with every other. Shipping 1.0.0 is
// the act of promising stability - it should be deliberate, not the accidental
// result of a "!" in a commit message.
func Next(current Version, bump Bump) Version {
	next := Version{Major: current.Major, Minor: current.Minor, Patch: current.Patch}

	if current.IsPreRelease() {
		// Releasing a pre-release drops the suffix: 1.2.0-rc.1 becomes 1.2.0,
		// whatever the commits since then were.
		return Version{Major: current.Major, Minor: current.Minor, Patch: current.Patch}
	}

	switch bump {
	case Major:
		if current.IsUnstable() {
			next.Minor++
			next.Patch = 0

			return next
		}

		next.Major++
		next.Minor = 0
		next.Patch = 0

	case Minor:
		next.Minor++
		next.Patch = 0

	case Patch:
		next.Patch++

	case None:
		return current
	}

	return next
}

// NextFor combines BumpFor and Next.
func NextFor(current Version, commits []Commit) (Version, Bump) {
	bump := BumpFor(commits)

	return Next(current, bump), bump
}

// Latest returns the highest version in a list of tags, ignoring anything that
// is not a version.
func Latest(tags []string) (Version, bool) {
	var (
		latest Version
		found  bool
	)

	for _, tag := range tags {
		version, err := Parse(tag)
		if err != nil {
			continue
		}

		if !found || Compare(version, latest) > 0 {
			latest = version
			found = true
		}
	}

	return latest, found
}
