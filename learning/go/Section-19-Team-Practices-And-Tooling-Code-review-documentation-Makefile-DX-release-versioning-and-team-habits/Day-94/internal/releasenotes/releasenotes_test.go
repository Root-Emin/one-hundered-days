package releasenotes_test

import (
	"strings"
	"testing"
	"time"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-94/internal/releasenotes"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-94/internal/semver"
)

func sample() []semver.Commit {
	return []semver.Commit{
		semver.ParseCommit("aaa1111", "feat(api): add reservations", "", "ada"),
		semver.ParseCommit("bbb2222", "fix(store): close rows", "", "grace"),
		semver.ParseCommit("ccc3333", "refactor(store): extract the builder", "", "ada"),
		semver.ParseCommit("ddd4444", "feat(api)!: error bodies are objects",
			"BREAKING CHANGE: switch on the error field.\n\nMigration: replace string comparisons.", "ada"),
		semver.ParseCommit("eee5555", "chore: bump deps", "", "linus"),
	}
}

func build(t *testing.T) releasenotes.Notes {
	t.Helper()

	previous, err := semver.Parse("v1.2.3")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	commits := sample()

	version, _ := semver.NextFor(previous, commits)

	return releasenotes.Build(previous, version, commits, time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC))
}

func TestBuildGroupsCommits(t *testing.T) {
	notes := build(t)

	if notes.Version.String() != "v2.0.0" {
		t.Errorf("version = %s, want v2.0.0", notes.Version)
	}

	if len(notes.Breaking) != 1 {
		t.Errorf("breaking = %d, want 1", len(notes.Breaking))
	}

	if len(notes.Sections["feat"]) != 2 {
		t.Errorf("feat = %d, want 2", len(notes.Sections["feat"]))
	}

	if len(notes.Sections["fix"]) != 1 {
		t.Errorf("fix = %d, want 1", len(notes.Sections["fix"]))
	}

	// refactor and chore are real work and invisible to a deployer: counted,
	// not listed.
	if notes.Skipped != 2 {
		t.Errorf("skipped = %d, want 2", notes.Skipped)
	}

	if len(notes.Contributors) != 3 {
		t.Errorf("contributors = %v, want 3", notes.Contributors)
	}
}

// A deployer who reads nothing else must still see the breaking changes, so
// they come before everything.
func TestBreakingChangesComeFirst(t *testing.T) {
	rendered := build(t).Markdown(releasenotes.Options{})

	breaking := strings.Index(rendered, "### Breaking changes")
	added := strings.Index(rendered, "### Added")

	if breaking < 0 {
		t.Fatal("no breaking changes section")
	}

	if added >= 0 && breaking > added {
		t.Error("the breaking changes section comes after Added")
	}

	if !strings.Contains(rendered, "Migration: replace string comparisons") {
		t.Error("the migration instructions were dropped")
	}
}

// A breaking change with no migration note is a support ticket waiting to
// happen, so the gap is stated rather than hidden.
func TestBreakingChangeWithoutANoteIsCalledOut(t *testing.T) {
	previous, _ := semver.Parse("v1.0.0")
	next, _ := semver.Parse("v2.0.0")

	commits := []semver.Commit{
		semver.ParseCommit("aaa", "feat(api)!: remove the legacy endpoint", "", "ada"),
	}

	rendered := releasenotes.Build(previous, next, commits, time.Now()).Markdown(releasenotes.Options{})

	if !strings.Contains(rendered, "No migration note") {
		t.Errorf("a breaking change with no note was not flagged:\n%s", rendered)
	}
}

func TestCommitLinks(t *testing.T) {
	rendered := build(t).Markdown(releasenotes.Options{Repository: "https://github.com/acme/catalog/"})

	if !strings.Contains(rendered, "https://github.com/acme/catalog/commit/aaa1111") {
		t.Errorf("no commit link in:\n%s", rendered)
	}

	// Without a repository there are no links, and nothing is broken.
	plain := build(t).Markdown(releasenotes.Options{})

	if strings.Contains(plain, "commit/") {
		t.Error("links were rendered with no repository configured")
	}
}

func TestSummaryIsOneLine(t *testing.T) {
	summary := build(t).Summary()

	if strings.Contains(summary, "\n") {
		t.Errorf("summary spans lines: %q", summary)
	}

	for _, want := range []string{"v2.0.0", "major", "BREAKING"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary %q is missing %q", summary, want)
		}
	}
}

func TestInternalOnlyReleaseSaysSo(t *testing.T) {
	previous, _ := semver.Parse("v1.0.0")

	commits := []semver.Commit{
		semver.ParseCommit("aaa", "refactor: tidy up", "", "ada"),
		semver.ParseCommit("bbb", "test: add cases", "", "ada"),
	}

	rendered := releasenotes.Build(previous, previous, commits, time.Now()).Markdown(releasenotes.Options{})

	if !strings.Contains(rendered, "2 internal change") {
		t.Errorf("internal changes were not accounted for:\n%s", rendered)
	}
}

func TestAuthorsAreOptional(t *testing.T) {
	withAuthors := build(t).Markdown(releasenotes.Options{IncludeAuthors: true})
	without := build(t).Markdown(releasenotes.Options{})

	if !strings.Contains(withAuthors, "### Contributors") {
		t.Error("contributors missing when requested")
	}

	if strings.Contains(without, "### Contributors") {
		t.Error("contributors rendered when not requested")
	}
}
