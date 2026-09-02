package coverage_test

import (
	"strings"
	"testing"

	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-69/internal/coverage"
)

const sample = `mode: set
example.com/app/internal/pricing/pricing.go:10.20,12.3 2 1
example.com/app/internal/pricing/pricing.go:14.2,16.4 3 0
example.com/app/internal/pricing/format.go:5.10,7.2 1 1
example.com/app/internal/store/store.go:20.30,25.2 5 0
`

func TestParse(t *testing.T) {
	t.Parallel()

	profile, err := coverage.Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if profile.Mode != "set" {
		t.Fatalf("mode = %q", profile.Mode)
	}

	if len(profile.Blocks) != 4 {
		t.Fatalf("blocks = %d, want 4", len(profile.Blocks))
	}

	covered, total, percent := profile.Total()

	// 2 + 1 covered out of 2 + 3 + 1 + 5.
	if covered != 3 || total != 11 {
		t.Fatalf("covered/total = %d/%d, want 3/11", covered, total)
	}

	if percent < 27 || percent > 28 {
		t.Fatalf("percent = %.2f, want ~27.3", percent)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"no mode line":      "example.com/app/x.go:1.1,2.2 1 1\n",
		"missing fields":    "mode: set\nexample.com/app/x.go:1.1,2.2 1\n",
		"malformed span":    "mode: set\nexample.com/app/x.go:1.1 1 1\n",
		"non-numeric count": "mode: set\nexample.com/app/x.go:1.1,2.2 1 many\n",
		"no file separator": "mode: set\nnonsense\n",
	}

	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := coverage.Parse(strings.NewReader(input)); err == nil {
				t.Fatal("garbage was accepted")
			}
		})
	}
}

func TestByPackage(t *testing.T) {
	t.Parallel()

	profile, err := coverage.Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	packages := profile.ByPackage()

	if len(packages) != 2 {
		t.Fatalf("packages = %d, want 2", len(packages))
	}

	// Sorted by name: pricing then store.
	pricing := packages[0]

	if !strings.HasSuffix(pricing.Package, "internal/pricing") {
		t.Fatalf("first package = %q", pricing.Package)
	}

	if pricing.Covered != 3 || pricing.Total != 6 {
		t.Fatalf("pricing = %d/%d, want 3/6", pricing.Covered, pricing.Total)
	}

	if pricing.Percent != 50 {
		t.Fatalf("pricing percent = %.1f, want 50", pricing.Percent)
	}

	// The uncovered statements are attributed to the right file.
	if pricing.MissedFile["example.com/app/internal/pricing/pricing.go"] != 3 {
		t.Fatalf("missed statements = %v", pricing.MissedFile)
	}

	store := packages[1]

	if store.Percent != 0 || store.Total != 5 {
		t.Fatalf("store = %.1f%% of %d", store.Percent, store.Total)
	}
}

func TestPolicyCheck(t *testing.T) {
	t.Parallel()

	policy := coverage.Policy{
		TotalMinimum: 50,
		Gates: []coverage.Gate{
			{PathSuffix: "internal", Minimum: 40, Reason: "default for internal packages"},
			{PathSuffix: "internal/pricing", Minimum: 80, Reason: "critical path"},
		},
		Ignore: []coverage.Gate{
			{PathSuffix: "internal/store", Reason: "covered by integration tests elsewhere"},
		},
	}

	packages := []coverage.PackageCoverage{
		{Package: "example.com/app/internal/pricing", Percent: 70, Covered: 7, Total: 10},
		{Package: "example.com/app/internal/store", Percent: 0, Covered: 0, Total: 5},
		{Package: "example.com/app/internal/util", Percent: 45, Covered: 9, Total: 20},
	}

	violations := policy.Check(packages, 48)

	// pricing (70 < 80) and the total (48 < 50). store is ignored; util
	// passes the generic 40% gate.
	if len(violations) != 2 {
		t.Fatalf("violations = %v, want 2", violations)
	}

	if !strings.Contains(violations[0].String(), "pricing") {
		t.Fatalf("first violation = %s", violations[0])
	}

	if violations[0].Minimum != 80 {
		t.Fatalf("pricing gate = %.0f, want the most specific one (80)", violations[0].Minimum)
	}

	if violations[1].Package != "TOTAL" {
		t.Fatalf("second violation = %s", violations[1])
	}
}

func TestPolicyPassesWhenEverythingMeetsItsGate(t *testing.T) {
	t.Parallel()

	policy := coverage.Policy{
		TotalMinimum: 50,
		Gates:        []coverage.Gate{{PathSuffix: "internal", Minimum: 50, Reason: "baseline"}},
	}

	packages := []coverage.PackageCoverage{
		{Package: "example.com/app/internal/pricing", Percent: 90},
		{Package: "example.com/app/internal/store", Percent: 51},
	}

	if violations := policy.Check(packages, 70); len(violations) != 0 {
		t.Fatalf("violations = %v, want none", violations)
	}
}

// TestPolicyIsExactAtTheBoundary: a package sitting exactly on its threshold
// passes. Floating point makes this worth pinning down.
func TestPolicyIsExactAtTheBoundary(t *testing.T) {
	t.Parallel()

	policy := coverage.Policy{
		Gates: []coverage.Gate{{PathSuffix: "internal/pricing", Minimum: 80, Reason: "critical"}},
	}

	packages := []coverage.PackageCoverage{
		{Package: "example.com/app/internal/pricing", Percent: 80},
	}

	if violations := policy.Check(packages, 100); len(violations) != 0 {
		t.Fatalf("exactly at the threshold was rejected: %v", violations)
	}
}
