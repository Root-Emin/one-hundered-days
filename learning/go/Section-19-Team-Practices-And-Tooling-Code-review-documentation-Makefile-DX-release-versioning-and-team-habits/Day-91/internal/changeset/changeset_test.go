package changeset_test

import (
	"strings"
	"testing"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-91/internal/changeset"
)

func parse(t *testing.T, numstat string) changeset.Changeset {
	t.Helper()

	diff, err := changeset.ParseNumstat(numstat)
	if err != nil {
		t.Fatalf("ParseNumstat: %v", err)
	}

	return diff
}

func TestParseNumstat(t *testing.T) {
	diff := parse(t, "12\t4\tinternal/store/store.go\n48\t0\tinternal/store/store_test.go\n")

	if len(diff.Files) != 2 {
		t.Fatalf("files = %d, want 2", len(diff.Files))
	}

	stats := diff.Stats()

	if stats.Added != 60 || stats.Removed != 4 || stats.Total != 64 {
		t.Errorf("stats = %+v", stats)
	}

	if stats.TestFiles != 1 || stats.GoFiles != 1 {
		t.Errorf("test files = %d, go files = %d, want 1 and 1", stats.TestFiles, stats.GoFiles)
	}
}

// git reports "-\t-" for a binary file; parsing that as an integer is the bug
// this guards.
func TestBinaryFilesParse(t *testing.T) {
	diff := parse(t, "-\t-\tdocs/diagram.png\n3\t1\tREADME.md\n")

	if len(diff.Files) != 2 {
		t.Fatalf("files = %d, want 2", len(diff.Files))
	}

	if !diff.Files[0].Binary {
		t.Error("the png should be marked binary")
	}

	if total := diff.Stats().Total; total != 4 {
		t.Errorf("total = %d, want 4 (a binary file contributes no lines)", total)
	}
}

func TestMalformedNumstatIsAnError(t *testing.T) {
	if _, err := changeset.ParseNumstat("not a numstat line\n"); err == nil {
		t.Error("expected an error for a malformed line")
	}

	if _, err := changeset.ParseNumstat("x\t2\tfile.go\n"); err == nil {
		t.Error("expected an error for a non-numeric count")
	}
}

// Counting a 30,000-line protobuf regeneration as a huge review teaches people
// to ignore the size warning, so generated files are excluded.
func TestGeneratedFilesAreExcludedFromTheSize(t *testing.T) {
	diff := parse(t, "10\t2\tinternal/api/api.go\n"+
		"20\t0\tinternal/api/api_test.go\n"+
		"9000\t0\tgen/orders/v1/orders.pb.go\n"+
		"400\t0\tgo.sum\n")

	stats := diff.Stats()

	if stats.Total != 32 {
		t.Errorf("reviewable lines = %d, want 32", stats.Total)
	}

	if stats.GeneratedFiles != 2 || stats.GeneratedLines != 9400 {
		t.Errorf("generated = %d files / %d lines, want 2 / 9400",
			stats.GeneratedFiles, stats.GeneratedLines)
	}

	if stats.Label() != "size/XS" {
		t.Errorf("label = %s, want size/XS", stats.Label())
	}
}

func TestSizeLabels(t *testing.T) {
	cases := []struct {
		lines int
		want  string
	}{
		{10, "size/XS"},
		{120, "size/S"},
		{300, "size/M"},
		{700, "size/L"},
		{2000, "size/XL"},
	}

	for _, testCase := range cases {
		diff := parse(t, numstatWithLines(testCase.lines))

		if got := diff.Stats().Label(); got != testCase.want {
			t.Errorf("%d lines -> %s, want %s", testCase.lines, got, testCase.want)
		}
	}
}

func numstatWithLines(lines int) string {
	return strings.Join([]string{itoa(lines), "0", "internal/api/api.go"}, "\t") + "\n" +
		"1\t0\tinternal/api/api_test.go\n"
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}

	var digits []byte

	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}

	return string(digits)
}

func TestHugeChangeBlocks(t *testing.T) {
	verdict := parse(t, "1500\t400\tinternal/api/api.go\n50\t0\tinternal/api/api_test.go\n").Review()

	if len(verdict.Findings) == 0 {
		t.Fatal("a 1950-line change should produce a blocking finding")
	}

	if !strings.Contains(verdict.Findings[0], "skimmed") {
		t.Errorf("finding = %q", verdict.Findings[0])
	}
}

func TestLargeChangeOnlyWarns(t *testing.T) {
	verdict := parse(t, "450\t0\tinternal/api/api.go\n50\t0\tinternal/api/api_test.go\n").Review()

	if len(verdict.Findings) != 0 {
		t.Errorf("a 500-line change should warn, not block: %v", verdict.Findings)
	}

	if len(verdict.Warnings) == 0 {
		t.Error("expected a size warning")
	}
}

func TestCodeWithoutTestsIsFlagged(t *testing.T) {
	verdict := parse(t, "86\t14\tinternal/auth/token.go\n").Review()

	found := false

	for _, finding := range verdict.Findings {
		if strings.Contains(finding, "_test.go") {
			found = true
		}
	}

	if !found {
		t.Errorf("expected a missing-test finding, got %v", verdict.Findings)
	}
}

func TestTestOnlyChangeIsNotFlagged(t *testing.T) {
	verdict := parse(t, "120\t0\tinternal/auth/token_test.go\n").Review()

	if len(verdict.Findings) != 0 {
		t.Errorf("a test-only change should not be flagged: %v", verdict.Findings)
	}
}

func TestRiskyPathsAreCalledOut(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"migrations/003_add_index.sql", "migrations"},
		{"go.mod", "dependencies"},
		{"internal/auth/token.go", "authentication"},
		{".github/workflows/release.yml", "deploy pipeline"},
		{"Dockerfile", "deploy pipeline"},
	}

	for _, testCase := range cases {
		t.Run(testCase.path, func(t *testing.T) {
			verdict := parse(t, "5\t1\t"+testCase.path+"\n5\t0\tmain_test.go\n").Review()

			found := false

			for _, warning := range verdict.Warnings {
				if strings.Contains(warning, testCase.want) {
					found = true
				}
			}

			if !found {
				t.Errorf("expected a warning mentioning %q, got %v", testCase.want, verdict.Warnings)
			}
		})
	}
}

// One warning per reason, however many files match it - a wall of identical
// warnings is noise.
func TestRiskyWarningsAreNotRepeated(t *testing.T) {
	verdict := parse(t, "1\t0\tmigrations/001.sql\n1\t0\tmigrations/002.sql\n"+
		"1\t0\tmigrations/003.sql\n1\t0\tmain_test.go\n").Review()

	count := 0

	for _, warning := range verdict.Warnings {
		if strings.Contains(warning, "migrations") {
			count++
		}
	}

	if count != 1 {
		t.Errorf("migration warnings = %d, want 1", count)
	}
}
