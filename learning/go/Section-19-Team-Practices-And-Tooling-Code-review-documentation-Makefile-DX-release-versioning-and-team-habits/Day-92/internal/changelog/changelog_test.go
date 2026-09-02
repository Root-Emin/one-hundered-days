package changelog_test

import (
	"strings"
	"testing"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-92/internal/changelog"
)

const good = `# Changelog

## [Unreleased]

### Added

- A thing that is not released yet.

## [1.2.0] - 2026-08-30

### Added

- Reservations endpoint.

### Changed

- Errors use one body shape.

## [1.1.0] - 2026-07-14

### Security

- Request bodies capped at 1 MiB.

## [1.0.0] - 2026-06-02

### Added

- The first release.
`

func TestParse(t *testing.T) {
	parsed := changelog.Parse(good)

	if parsed.Title != "Changelog" {
		t.Errorf("title = %q", parsed.Title)
	}

	if len(parsed.Releases) != 4 {
		t.Fatalf("releases = %d, want 4", len(parsed.Releases))
	}

	unreleased, found := parsed.Unreleased()
	if !found {
		t.Fatal("no Unreleased section")
	}

	if unreleased.Total() != 1 {
		t.Errorf("unreleased entries = %d, want 1", unreleased.Total())
	}

	latest, found := parsed.Latest()
	if !found {
		t.Fatal("no released version")
	}

	if latest.Version != "1.2.0" || latest.Date != "2026-08-30" {
		t.Errorf("latest = %s (%s), want 1.2.0 (2026-08-30)", latest.Version, latest.Date)
	}

	if got := len(latest.Entries["Added"]); got != 1 {
		t.Errorf("1.2.0 Added entries = %d, want 1", got)
	}
}

func TestValidateAcceptsAGoodChangelog(t *testing.T) {
	if problems := changelog.Parse(good).Validate(); len(problems) != 0 {
		t.Errorf("expected no problems, got %v", problems)
	}
}

func TestValidationRules(t *testing.T) {
	cases := []struct {
		name    string
		content string
		rule    string
	}{
		{
			name:    "no unreleased section",
			content: "# Changelog\n\n## [1.0.0] - 2026-01-01\n\n### Added\n\n- Thing.\n",
			rule:    "missing_unreleased",
		},
		{
			name:    "missing date",
			content: "# Changelog\n\n## [Unreleased]\n\n## [1.0.0]\n\n### Added\n\n- Thing.\n",
			rule:    "missing_date",
		},
		{
			name:    "unknown category",
			content: "# Changelog\n\n## [Unreleased]\n\n## [1.0.0] - 2026-01-01\n\n### Misc\n\n- Thing.\n",
			rule:    "unknown_category",
		},
		{
			name:    "not semver",
			content: "# Changelog\n\n## [Unreleased]\n\n## [v1] - 2026-01-01\n\n### Added\n\n- Thing.\n",
			rule:    "bad_version",
		},
		{
			name: "out of order",
			content: "# Changelog\n\n## [Unreleased]\n\n## [1.0.0] - 2026-01-01\n\n### Added\n\n- One.\n" +
				"\n## [2.0.0] - 2026-02-01\n\n### Added\n\n- Two.\n",
			rule: "out_of_order",
		},
		{
			name:    "released with nothing in it",
			content: "# Changelog\n\n## [Unreleased]\n\n## [1.0.0] - 2026-01-01\n",
			rule:    "empty_release",
		},
		{
			name:    "not a changelog at all",
			content: "# Some document\n\nWith prose and no releases.\n",
			rule:    "empty",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			problems := changelog.Parse(testCase.content).Validate()

			found := false

			for _, problem := range problems {
				if problem.Rule == testCase.rule {
					found = true
				}
			}

			if !found {
				t.Errorf("expected rule %s, got %v", testCase.rule, problems)
			}
		})
	}
}

// A yanked release has no entries by design, so it must not be reported as
// empty.
func TestYankedReleaseIsNotEmpty(t *testing.T) {
	content := "# Changelog\n\n## [Unreleased]\n\n## [1.0.1] - 2026-01-02 [YANKED]\n\n" +
		"## [1.0.0] - 2026-01-01\n\n### Added\n\n- Thing.\n"

	for _, problem := range changelog.Parse(content).Validate() {
		if problem.Rule == "empty_release" {
			t.Errorf("a yanked release was reported as empty: %v", problem)
		}
	}
}

// Semver ordering, including the rule a string comparison gets wrong:
// 1.0.0-rc.1 comes BEFORE 1.0.0, and 1.10.0 comes after 1.9.0.
func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.1.0", "1.0.9", 1},
		{"2.0.0", "1.99.99", 1},
		{"1.9.0", "1.10.0", -1},
		{"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0", "1.0.0-rc.1", 1},
		{"1.0.0-rc.1", "1.0.0-rc.2", -1},
		{"v1.2.0", "1.2.0", 0},
	}

	for _, testCase := range cases {
		if got := changelog.CompareVersions(testCase.a, testCase.b); got != testCase.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", testCase.a, testCase.b, got, testCase.want)
		}
	}
}

// The project's own changelog has to pass its own validator, or the rules are
// aspirational.
func TestProjectChangelogIsValid(t *testing.T) {
	parsed, err := changelog.Load("../../CHANGELOG.md")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	for _, problem := range parsed.Validate() {
		t.Errorf("%s", problem)
	}

	if _, found := parsed.Unreleased(); !found {
		t.Error("the project changelog has no Unreleased section")
	}
}

// A breaking change has to be findable by someone scanning before an upgrade.
func TestBreakingChangesAreCalledOut(t *testing.T) {
	parsed, err := changelog.Load("../../CHANGELOG.md")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	found := false

	for _, release := range parsed.Releases {
		for _, entry := range release.Entries["Changed"] {
			if strings.Contains(strings.ToLower(entry), "breaking") {
				found = true
			}
		}
	}

	if !found {
		t.Error("no entry marked as breaking; 1.2.0 changed the error body shape and should say so")
	}
}
