package semver_test

import (
	"errors"
	"strings"
	"testing"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-94/internal/semver"
)

func mustParse(t *testing.T, value string) semver.Version {
	t.Helper()

	version, err := semver.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q): %v", value, err)
	}

	return version
}

func TestParseAndString(t *testing.T) {
	cases := map[string]string{
		"1.2.3":             "v1.2.3",
		"v1.2.3":            "v1.2.3",
		"v0.0.1":            "v0.0.1",
		"v1.0.0-rc.1":       "v1.0.0-rc.1",
		"v1.0.0+build.5":    "v1.0.0+build.5",
		"v2.10.0-beta.2+ci": "v2.10.0-beta.2+ci",
	}

	for input, want := range cases {
		version := mustParse(t, input)

		if got := version.String(); got != want {
			t.Errorf("Parse(%q).String() = %q, want %q", input, got, want)
		}
	}
}

func TestParseRejectsNonVersions(t *testing.T) {
	for _, input := range []string{"", "v1", "1.2", "1.2.3.4", "latest", "v1.2.x"} {
		if _, err := semver.Parse(input); !errors.Is(err, semver.ErrInvalidVersion) {
			t.Errorf("Parse(%q) = %v, want ErrInvalidVersion", input, err)
		}
	}
}

// The orderings a string comparison gets wrong.
func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.9.0", "v1.10.0", -1},
		{"v2.0.0", "v1.99.99", 1},
		{"v1.0.1", "v1.0.0", 1},
		{"v1.0.0-rc.1", "v1.0.0", -1},
		{"v1.0.0", "v1.0.0-rc.1", 1},
		{"v1.0.0-rc.2", "v1.0.0-rc.10", -1},
		{"v1.0.0-alpha", "v1.0.0-beta", -1},
		// Build metadata is ignored, as the specification requires.
		{"v1.0.0+build.1", "v1.0.0+build.2", 0},
	}

	for _, testCase := range cases {
		got := semver.Compare(mustParse(t, testCase.a), mustParse(t, testCase.b))

		if got != testCase.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", testCase.a, testCase.b, got, testCase.want)
		}
	}
}

func TestParseCommit(t *testing.T) {
	cases := []struct {
		subject  string
		body     string
		wantType string
		scope    string
		breaking bool
	}{
		{"feat(api): add reservations", "", "feat", "api", false},
		{"fix: close rows", "", "fix", "", false},
		{"feat(api)!: change the error body", "", "feat", "api", true},
		{"refactor(store): extract the builder", "", "refactor", "store", false},
		{"fix: something", "BREAKING CHANGE: the config key moved", "fix", "", true},
		{"fix: something", "BREAKING-CHANGE: the config key moved", "fix", "", true},
		// Not a conventional commit: no type, and no version bump.
		{"quick fix for the thing", "", "", "", false},
	}

	for _, testCase := range cases {
		commit := semver.ParseCommit("abc123", testCase.subject, testCase.body, "ada")

		if commit.Type != testCase.wantType {
			t.Errorf("%q: type = %q, want %q", testCase.subject, commit.Type, testCase.wantType)
		}

		if commit.Scope != testCase.scope {
			t.Errorf("%q: scope = %q, want %q", testCase.subject, commit.Scope, testCase.scope)
		}

		if commit.Breaking != testCase.breaking {
			t.Errorf("%q: breaking = %t, want %t", testCase.subject, commit.Breaking, testCase.breaking)
		}
	}
}

func TestBreakingNoteKeepsTheMigrationInstructions(t *testing.T) {
	commit := semver.ParseCommit("abc", "feat!: change the error body",
		"BREAKING CHANGE: bodies are now objects.\n\nMigration: switch on the error field.", "ada")

	note := commit.BreakingNote()

	if note == "" {
		t.Fatal("no breaking note extracted")
	}

	// The instructions usually come after a blank line; cutting at the first
	// one throws away the half that says what to do.
	if !strings.Contains(note, "Migration:") {
		t.Errorf("note = %q, want it to include the migration paragraph", note)
	}
}

func TestBumpFor(t *testing.T) {
	cases := []struct {
		name    string
		commits []semver.Commit
		want    semver.Bump
	}{
		{"nothing user-visible", []semver.Commit{
			semver.ParseCommit("a", "chore: bump deps", "", ""),
			semver.ParseCommit("b", "docs: fix a typo", "", ""),
			semver.ParseCommit("c", "test: add a case", "", ""),
		}, semver.None},
		{"a fix", []semver.Commit{
			semver.ParseCommit("a", "fix: close rows", "", ""),
		}, semver.Patch},
		{"a feature outranks a fix", []semver.Commit{
			semver.ParseCommit("a", "fix: close rows", "", ""),
			semver.ParseCommit("b", "feat: add an endpoint", "", ""),
		}, semver.Minor},
		{"breaking outranks everything", []semver.Commit{
			semver.ParseCommit("a", "feat: add an endpoint", "", ""),
			semver.ParseCommit("b", "fix!: rename the field", "", ""),
		}, semver.Major},
		{"perf is a patch", []semver.Commit{
			semver.ParseCommit("a", "perf: reuse the buffer", "", ""),
		}, semver.Patch},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := semver.BumpFor(testCase.commits); got != testCase.want {
				t.Errorf("BumpFor = %s, want %s", got, testCase.want)
			}
		})
	}
}

func TestNext(t *testing.T) {
	cases := []struct {
		current string
		bump    semver.Bump
		want    string
	}{
		{"v1.2.3", semver.Patch, "v1.2.4"},
		{"v1.2.3", semver.Minor, "v1.3.0"},
		{"v1.2.3", semver.Major, "v2.0.0"},
		{"v1.2.3", semver.None, "v1.2.3"},
		// Below 1.0.0 a breaking change is a MINOR bump: 0.y.z is defined as
		// unstable, and shipping 1.0.0 should be deliberate.
		{"v0.4.1", semver.Major, "v0.5.0"},
		{"v0.4.1", semver.Minor, "v0.5.0"},
		{"v0.4.1", semver.Patch, "v0.4.2"},
		// Releasing a pre-release drops the suffix.
		{"v1.3.0-rc.1", semver.Patch, "v1.3.0"},
	}

	for _, testCase := range cases {
		got := semver.Next(mustParse(t, testCase.current), testCase.bump)

		if got.String() != testCase.want {
			t.Errorf("Next(%s, %s) = %s, want %s", testCase.current, testCase.bump, got, testCase.want)
		}
	}
}

func TestLatestIgnoresNonVersions(t *testing.T) {
	latest, found := semver.Latest([]string{"v1.0.0", "not-a-tag", "v1.10.0", "v1.9.9", "release-2"})
	if !found {
		t.Fatal("no version found")
	}

	if latest.String() != "v1.10.0" {
		t.Errorf("latest = %s, want v1.10.0", latest)
	}

	if _, found := semver.Latest([]string{"nightly", "stable"}); found {
		t.Error("found a version among tags that contain none")
	}
}

func TestUnstableAndPreRelease(t *testing.T) {
	if !mustParse(t, "v0.9.9").IsUnstable() {
		t.Error("0.x should report as unstable")
	}

	if mustParse(t, "v1.0.0").IsUnstable() {
		t.Error("1.0.0 should not report as unstable")
	}

	if !mustParse(t, "v1.0.0-rc.1").IsPreRelease() {
		t.Error("rc.1 should report as a pre-release")
	}
}
