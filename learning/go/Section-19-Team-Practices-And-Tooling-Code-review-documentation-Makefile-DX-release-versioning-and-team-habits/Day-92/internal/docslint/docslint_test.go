package docslint_test

import (
	"os"
	"path/filepath"
	"testing"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-92/internal/docslint"
)

func TestFindsEveryKindOfIssue(t *testing.T) {
	issues, err := docslint.CheckPackage(filepath.Join("testdata", "badpackage"), docslint.DefaultOptions())
	if err != nil {
		t.Fatalf("CheckPackage: %v", err)
	}

	counts := docslint.Summary(issues)

	want := map[string]int{
		"missing_package_doc": 1, // the package comment
		"missing_doc":         3, // UndocumentedFunction, UndocumentedType, MaxWidgets
		"bad_prefix":          3, // CountWidgets, Widget, DefaultTimeout
	}

	for rule, expected := range want {
		if counts[rule] != expected {
			t.Errorf("%s = %d, want %d (issues: %v)", rule, counts[rule], expected, issues)
		}
	}
}

// Unexported symbols are nobody else's problem, and reporting them would bury
// the findings that matter.
func TestUnexportedSymbolsAreIgnored(t *testing.T) {
	issues, err := docslint.CheckPackage(filepath.Join("testdata", "badpackage"), docslint.DefaultOptions())
	if err != nil {
		t.Fatalf("CheckPackage: %v", err)
	}

	for _, issue := range issues {
		if issue.Symbol == "unexportedIsFine" || issue.Symbol == "unexportedTypeIsFine" {
			t.Errorf("unexported symbol reported: %v", issue)
		}
	}
}

// The package's own code has to pass its own linter.
func TestThisPackageIsDocumented(t *testing.T) {
	issues, err := docslint.CheckPackage(".", docslint.DefaultOptions())
	if err != nil {
		t.Fatalf("CheckPackage: %v", err)
	}

	for _, issue := range issues {
		t.Errorf("%s", issue)
	}
}

func TestOptionsTurnRulesOff(t *testing.T) {
	options := docslint.DefaultOptions()
	options.RequireNamePrefix = false

	issues, err := docslint.CheckPackage(filepath.Join("testdata", "badpackage"), options)
	if err != nil {
		t.Fatalf("CheckPackage: %v", err)
	}

	if count := docslint.Summary(issues)["bad_prefix"]; count != 0 {
		t.Errorf("bad_prefix = %d with the rule disabled, want 0", count)
	}

	options.RequireExportedDoc = false

	issues, err = docslint.CheckPackage(filepath.Join("testdata", "badpackage"), options)
	if err != nil {
		t.Fatalf("CheckPackage: %v", err)
	}

	if count := docslint.Summary(issues)["missing_doc"]; count != 0 {
		t.Errorf("missing_doc = %d with the rule disabled, want 0", count)
	}
}

func TestWellDocumentedPackagePasses(t *testing.T) {
	dir := t.TempDir()

	source := `// Package good is documented.
package good

// Widget is a thing.
type Widget struct{}

// Count returns how many widgets exist.
func Count() int { return 0 }

// MaxWidgets is the cap.
const MaxWidgets = 10

// Errors returned by this package.
var (
	ErrOne = errorsNew("one")
	ErrTwo = errorsNew("two")
)

func errorsNew(string) error { return nil }
`

	if err := os.WriteFile(filepath.Join(dir, "good.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	issues, err := docslint.CheckPackage(dir, docslint.DefaultOptions())
	if err != nil {
		t.Fatalf("CheckPackage: %v", err)
	}

	for _, issue := range issues {
		t.Errorf("%s", issue)
	}
}

// Deprecated: markers legitimately come before the symbol's name.
func TestDeprecationMarkerIsAllowed(t *testing.T) {
	dir := t.TempDir()

	source := `// Package old is documented.
package old

// Deprecated: use Count instead. Size returns the widget count.
func Size() int { return 0 }
`

	if err := os.WriteFile(filepath.Join(dir, "old.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	issues, err := docslint.CheckPackage(dir, docslint.DefaultOptions())
	if err != nil {
		t.Fatalf("CheckPackage: %v", err)
	}

	for _, issue := range issues {
		t.Errorf("%s", issue)
	}
}

// A whole day's worth of packages, as CI would run it.
func TestEveryPackageInThisDayIsDocumented(t *testing.T) {
	issues, err := docslint.CheckDirectory(filepath.Join("..", ".."), docslint.DefaultOptions())
	if err != nil {
		t.Fatalf("CheckDirectory: %v", err)
	}

	for _, issue := range issues {
		t.Errorf("%s", issue)
	}
}
