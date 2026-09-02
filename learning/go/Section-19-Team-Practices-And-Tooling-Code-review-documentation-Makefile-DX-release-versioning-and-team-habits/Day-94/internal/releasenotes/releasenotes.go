// Package releasenotes turns commits into notes a deployer can act on.
//
// The audience is the person deciding whether to deploy this at 4pm on a
// Thursday. They need three things, in this order:
//
//  1. does this break anything I depend on, and what do I do about it
//  2. what is new
//  3. what was fixed
//
// Everything else - the refactors, the test additions, the dependency bumps -
// is in the git history, and putting it in the notes buries the three things
// that matter.
package releasenotes

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-94/internal/semver"
)

// section maps a commit type onto a heading, in the order they appear.
//
// Types absent from this list are omitted on purpose: "chore", "refactor",
// "test", "style" and "ci" are invisible to a deployer.
var sections = []struct {
	Type    string
	Heading string
}{
	{"feat", "Added"},
	{"fix", "Fixed"},
	{"perf", "Performance"},
	{"revert", "Reverted"},
	{"deprecate", "Deprecated"},
	{"security", "Security"},
	{"docs", "Documentation"},
}

// Notes is a rendered release.
type Notes struct {
	Version  semver.Version
	Previous semver.Version
	Date     time.Time
	Bump     semver.Bump

	Breaking     []semver.Commit
	Sections     map[string][]semver.Commit
	Contributors []string
	// Skipped counts commits that are real work but invisible to a deployer.
	Skipped int
}

// Options tune the rendering.
type Options struct {
	// Repository is used to build commit links, e.g.
	// "https://github.com/acme/catalog". Empty means no links.
	Repository string
	// IncludeAuthors adds a contributors list.
	IncludeAuthors bool
}

// Build groups commits into a release.
func Build(previous, version semver.Version, commits []semver.Commit, at time.Time) Notes {
	notes := Notes{
		Version:  version,
		Previous: previous,
		Date:     at,
		Bump:     semver.BumpFor(commits),
		Sections: make(map[string][]semver.Commit),
	}

	known := make(map[string]bool, len(sections))

	for _, section := range sections {
		known[section.Type] = true
	}

	authors := make(map[string]bool)

	for _, commit := range commits {
		if commit.Author != "" {
			authors[commit.Author] = true
		}

		if commit.Breaking {
			notes.Breaking = append(notes.Breaking, commit)
		}

		if !known[commit.Type] {
			// Real work, invisible to a deployer: counted, not listed.
			notes.Skipped++

			continue
		}

		notes.Sections[commit.Type] = append(notes.Sections[commit.Type], commit)
	}

	for author := range authors {
		notes.Contributors = append(notes.Contributors, author)
	}

	sort.Strings(notes.Contributors)

	return notes
}

// Markdown renders the notes.
func (n Notes) Markdown(options Options) string {
	var builder strings.Builder

	builder.WriteString(fmt.Sprintf("## %s - %s\n\n", n.Version, n.Date.Format("2006-01-02")))

	if n.Previous.String() != "v0.0.0" {
		builder.WriteString(fmt.Sprintf("_Changes since %s._\n\n", n.Previous))
	}

	// Breaking changes go FIRST, and say what to do - not just what changed.
	// A deployer who reads nothing else must still see these.
	if len(n.Breaking) > 0 {
		builder.WriteString("### Breaking changes\n\n")
		builder.WriteString("Read these before upgrading.\n\n")

		for _, commit := range n.Breaking {
			builder.WriteString("- **" + describe(commit) + "**")

			if link := commitLink(options.Repository, commit.Hash); link != "" {
				builder.WriteString(" " + link)
			}

			builder.WriteString("\n")

			if note := commit.BreakingNote(); note != "" {
				for _, line := range strings.Split(note, "\n") {
					builder.WriteString("  " + strings.TrimSpace(line) + "\n")
				}
			} else {
				// A breaking change with no migration note is a support
				// ticket waiting to happen, so the gap is stated rather than
				// hidden.
				builder.WriteString("  _No migration note in the commit; ask the author before deploying._\n")
			}

			builder.WriteString("\n")
		}
	}

	for _, section := range sections {
		commits := n.Sections[section.Type]
		if len(commits) == 0 {
			continue
		}

		builder.WriteString("### " + section.Heading + "\n\n")

		for _, commit := range commits {
			builder.WriteString("- " + describe(commit))

			if link := commitLink(options.Repository, commit.Hash); link != "" {
				builder.WriteString(" " + link)
			}

			builder.WriteString("\n")
		}

		builder.WriteString("\n")
	}

	if options.IncludeAuthors && len(n.Contributors) > 0 {
		builder.WriteString("### Contributors\n\n")

		for _, author := range n.Contributors {
			builder.WriteString("- " + author + "\n")
		}

		builder.WriteString("\n")
	}

	if n.Skipped > 0 {
		builder.WriteString(fmt.Sprintf(
			"_%d internal change(s) (refactors, tests, tooling) are omitted; see the git log._\n",
			n.Skipped))
	}

	return builder.String()
}

// describe renders one commit line, with its scope as a prefix.
func describe(commit semver.Commit) string {
	subject := commit.Subject

	if subject == "" {
		return commit.Hash
	}

	// Capitalise the first letter: the commit convention is lower-case, the
	// release note reads as prose.
	subject = strings.ToUpper(subject[:1]) + subject[1:]

	if commit.Scope != "" {
		return fmt.Sprintf("%s: %s", commit.Scope, subject)
	}

	return subject
}

func commitLink(repository, hash string) string {
	if repository == "" || hash == "" {
		return ""
	}

	short := hash

	if len(short) > 7 {
		short = short[:7]
	}

	return fmt.Sprintf("([%s](%s/commit/%s))", short, strings.TrimSuffix(repository, "/"), hash)
}

// Summary is the one-line version for a chat message or a deploy log.
func (n Notes) Summary() string {
	counts := make([]string, 0, len(sections))

	for _, section := range sections {
		if count := len(n.Sections[section.Type]); count > 0 {
			counts = append(counts, fmt.Sprintf("%d %s", count, strings.ToLower(section.Heading)))
		}
	}

	summary := fmt.Sprintf("%s (%s bump", n.Version, n.Bump)

	if len(n.Breaking) > 0 {
		summary += fmt.Sprintf(", %d BREAKING", len(n.Breaking))
	}

	summary += ")"

	if len(counts) > 0 {
		summary += ": " + strings.Join(counts, ", ")
	}

	return summary
}
