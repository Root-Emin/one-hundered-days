// Package arch holds the architecture tests: the dependency rules of this
// service, checked by the build instead of by memory.
package arch

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const modulePrefix = "example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-60/"

// layers, innermost first.
var layers = []string{
	"internal/domain",
	"internal/service",
	"internal/storage",
	"internal/transport",
	"cmd",
}

func layerOf(name string) int {
	for i := len(layers) - 1; i >= 0; i-- {
		if strings.HasPrefix(name, layers[i]) {
			return i
		}
	}

	return -1
}

func graph(t *testing.T) map[string][]string {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}

	result := make(map[string][]string)
	fileSet := token.NewFileSet()

	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		// Test files are excluded on purpose: like cmd, a test is allowed to
		// wire the whole stack (the httpapi tests run against real SQLite).
		// The rules below describe production dependencies.
		if strings.HasSuffix(path, "_test.go") {
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

		name := filepath.ToSlash(relative)

		if _, seen := result[name]; !seen {
			result[name] = nil
		}

		for _, importSpec := range file.Imports {
			result[name] = append(result[name], strings.Trim(importSpec.Path.Value, `"`))
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(result) == 0 {
		t.Fatal("no packages found")
	}

	return result
}

// TestDependenciesPointInward is the rule from the README, enforced.
func TestDependenciesPointInward(t *testing.T) {
	for name, imports := range graph(t) {
		from := layerOf(name)

		if from < 0 {
			continue
		}

		for _, importPath := range imports {
			if !strings.HasPrefix(importPath, modulePrefix) {
				continue
			}

			dependency := strings.TrimPrefix(importPath, modulePrefix)

			to := layerOf(dependency)

			if to < 0 {
				continue
			}

			if to > from {
				t.Errorf("%s imports %s: dependencies must point inward (%s)",
					name, dependency, strings.Join(layers, " <- "))
			}
		}
	}
}

// TestStorageAndTransportDoNotKnowEachOther: two outer layers at the same
// level must not couple, or swapping either one drags the other along.
func TestStorageAndTransportDoNotKnowEachOther(t *testing.T) {
	for name, imports := range graph(t) {
		for _, importPath := range imports {
			dependency := strings.TrimPrefix(importPath, modulePrefix)

			if strings.HasPrefix(name, "internal/transport") && strings.HasPrefix(dependency, "internal/storage") {
				t.Errorf("%s imports %s: the transport layer must not know the storage engine", name, dependency)
			}

			if strings.HasPrefix(name, "internal/storage") && strings.HasPrefix(dependency, "internal/transport") {
				t.Errorf("%s imports %s", name, dependency)
			}
		}
	}
}

// TestDomainIsPure: the innermost layer imports the standard library only.
func TestDomainIsPure(t *testing.T) {
	for _, importPath := range graph(t)["internal/domain"] {
		if strings.HasPrefix(importPath, modulePrefix) {
			t.Errorf("domain imports %q from this service", importPath)
		}

		// A dot in the first path element means a hosted module.
		if first, _, _ := strings.Cut(importPath, "/"); strings.Contains(first, ".") {
			t.Errorf("domain imports third-party package %q", importPath)
		}
	}
}

// TestOnlyStorageImportsADriver: exactly one package may know which database
// is in use.
func TestOnlyStorageImportsADriver(t *testing.T) {
	for name, imports := range graph(t) {
		for _, importPath := range imports {
			if !strings.Contains(importPath, "sqlite") || strings.HasPrefix(importPath, modulePrefix) {
				continue
			}

			if !strings.HasPrefix(name, "internal/storage") {
				t.Errorf("%s imports the database driver %q", name, importPath)
			}
		}
	}
}

// TestNoImportCycles walks the graph. Go rejects cycles at build time, but a
// failure here names the whole path instead of one edge.
func TestNoImportCycles(t *testing.T) {
	edges := make(map[string][]string)

	for name, imports := range graph(t) {
		for _, importPath := range imports {
			if strings.HasPrefix(importPath, modulePrefix) {
				edges[name] = append(edges[name], strings.TrimPrefix(importPath, modulePrefix))
			}
		}
	}

	state := make(map[string]int)

	var (
		stack []string
		visit func(string)
	)

	visit = func(name string) {
		state[name] = 1
		stack = append(stack, name)

		for _, dependency := range edges[name] {
			switch state[dependency] {
			case 0:
				visit(dependency)
			case 1:
				t.Errorf("import cycle: %s", strings.Join(append(stack, dependency), " -> "))
			}
		}

		stack = stack[:len(stack)-1]
		state[name] = 2
	}

	names := make([]string, 0, len(edges))

	for name := range edges {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		if state[name] == 0 {
			visit(name)
		}
	}
}
