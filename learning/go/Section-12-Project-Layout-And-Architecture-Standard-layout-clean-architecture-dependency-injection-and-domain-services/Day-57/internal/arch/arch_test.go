// Package arch contains no production code. It holds one test that reads the
// source of this service and fails when a layer imports something it must not.
//
// A dependency rule that lives only in a README is a rule that erodes. This
// test makes it part of `go test ./...`.
package arch

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// moduleRoot is the import path prefix of this day's packages.
const moduleRoot = "example.com/onehundredday/" +
	"Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/" +
	"Day-57/"

// layers, innermost first. A package may import its own layer and any layer
// listed before it - never one listed after.
var layerOrder = []string{
	"internal/domain",
	"internal/usecase",
	"internal/adapter",
	"cmd",
}

// forbidden lists imports that must never appear in a given layer, whatever
// their origin. These are the "the domain must not know about the web or the
// database" rules, spelled out.
var forbidden = map[string][]string{
	"internal/domain": {
		"net/http", "database/sql", "encoding/json", "log",
	},
	"internal/usecase": {
		"net/http", "database/sql", "encoding/json",
	},
}

func layerOf(packagePath string) string {
	for i := len(layerOrder) - 1; i >= 0; i-- {
		if strings.HasPrefix(packagePath, layerOrder[i]) {
			return layerOrder[i]
		}
	}

	return ""
}

func layerIndex(layer string) int {
	for i, candidate := range layerOrder {
		if candidate == layer {
			return i
		}
	}

	return -1
}

// collectImports returns, for every package directory of this day, the list of
// import paths its files declare.
func collectImports(t *testing.T) map[string][]string {
	t.Helper()

	// This test lives in Day-57/internal/arch, so the day root is two levels up.
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}

	imports := make(map[string][]string)

	fileSet := token.NewFileSet()

	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		file, err := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}

		relative, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}

		packagePath := filepath.ToSlash(relative)

		for _, importSpec := range file.Imports {
			imports[packagePath] = append(imports[packagePath],
				strings.Trim(importSpec.Path.Value, `"`))
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walk source tree: %v", err)
	}

	if len(imports) == 0 {
		t.Fatal("no packages found - the test is not looking where it thinks it is")
	}

	return imports
}

// TestDependencyRule: dependencies point inward. An outer layer may import an
// inner one; the reverse is a design error, and here it is a test failure.
func TestDependencyRule(t *testing.T) {
	for packagePath, importPaths := range collectImports(t) {
		fromLayer := layerOf(packagePath)

		if fromLayer == "" {
			continue
		}

		for _, importPath := range importPaths {
			if !strings.HasPrefix(importPath, moduleRoot) {
				continue
			}

			toLayer := layerOf(strings.TrimPrefix(importPath, moduleRoot))

			if toLayer == "" {
				continue
			}

			if layerIndex(toLayer) > layerIndex(fromLayer) {
				t.Errorf(
					"dependency rule violated: %s (%s) imports %s (%s)\n"+
						"    dependencies must point inward: %s",
					packagePath, fromLayer, importPath, toLayer,
					strings.Join(layerOrder, " <- "),
				)
			}
		}
	}
}

// TestForbiddenImports: the inner layers must not know about transport or
// storage technology at all, not even through the standard library.
func TestForbiddenImports(t *testing.T) {
	for packagePath, importPaths := range collectImports(t) {
		// The arch test itself is allowed to import whatever it needs.
		if strings.HasPrefix(packagePath, "internal/arch") {
			continue
		}

		for layer, banned := range forbidden {
			if !strings.HasPrefix(packagePath, layer) {
				continue
			}

			for _, importPath := range importPaths {
				for _, bannedImport := range banned {
					if importPath == bannedImport {
						t.Errorf("%s imports %q, which is forbidden in %s",
							packagePath, importPath, layer)
					}
				}
			}
		}
	}
}

// TestDomainIsSelfContained is the strongest statement of the rule: the domain
// depends on the standard library only, so it compiles and tests in isolation.
func TestDomainIsSelfContained(t *testing.T) {
	imports := collectImports(t)

	for _, importPath := range imports["internal/domain"] {
		if strings.Contains(importPath, ".") && !strings.HasPrefix(importPath, "example.com/") {
			t.Errorf("domain imports third-party package %q", importPath)
		}

		if strings.HasPrefix(importPath, moduleRoot) {
			t.Errorf("domain imports %q from this service; it must depend on nothing", importPath)
		}
	}
}

// TestNothingImportsCommands: a binary is a leaf. If a package imports cmd,
// the wiring has leaked into the code being wired.
func TestNothingImportsCommands(t *testing.T) {
	for packagePath, importPaths := range collectImports(t) {
		for _, importPath := range importPaths {
			if strings.HasPrefix(importPath, moduleRoot+"cmd/") {
				t.Errorf("%s imports the binary package %q", packagePath, importPath)
			}
		}
	}
}
