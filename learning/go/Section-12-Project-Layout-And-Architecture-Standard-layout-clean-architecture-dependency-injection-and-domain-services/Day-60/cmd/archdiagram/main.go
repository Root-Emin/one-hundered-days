// Command archdiagram prints the package diagram of this service by reading
// the source, so the picture in README.md can be checked instead of trusted.
//
// Drawing import arrows by hand is where accidental cycles hide. Generating
// them takes thirty lines and never goes stale.
package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const modulePrefix = "example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-60/"

func main() {
	root := "."

	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	graph, err := buildGraph(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "archdiagram: %v\n", err)
		os.Exit(1)
	}

	packages := make([]string, 0, len(graph))

	for name := range graph {
		packages = append(packages, name)
	}

	sort.Strings(packages)

	fmt.Println("\nPackage diagram (generated from the source)")
	fmt.Println(strings.Repeat("-", 72))

	for _, name := range packages {
		dependencies := graph[name]

		sort.Strings(dependencies)

		if len(dependencies) == 0 {
			fmt.Printf("%-28s (depends on nothing in this service)\n", name)
			continue
		}

		fmt.Printf("%-28s -> %s\n", name, strings.Join(dependencies, ", "))
	}

	fmt.Println("\nDependency rule check")
	fmt.Println(strings.Repeat("-", 72))

	if violations := check(graph); len(violations) == 0 {
		fmt.Println("  ok: every arrow points inward")
	} else {
		for _, violation := range violations {
			fmt.Printf("  VIOLATION: %s\n", violation)
		}

		os.Exit(1)
	}

	if cycles := findCycles(graph); len(cycles) > 0 {
		fmt.Println("\n  import cycles found:")

		for _, cycle := range cycles {
			fmt.Printf("    %s\n", cycle)
		}

		os.Exit(1)
	}

	fmt.Println("  ok: no import cycles")
	fmt.Println()
}

func buildGraph(root string) (map[string][]string, error) {
	graph := make(map[string][]string)
	fileSet := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
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

		if _, seen := graph[name]; !seen {
			graph[name] = nil
		}

		for _, importSpec := range file.Imports {
			importPath := strings.Trim(importSpec.Path.Value, `"`)

			if !strings.HasPrefix(importPath, modulePrefix) {
				continue
			}

			dependency := strings.TrimPrefix(importPath, modulePrefix)

			if !contains(graph[name], dependency) {
				graph[name] = append(graph[name], dependency)
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return graph, nil
}

// layers, innermost first.
var layers = []string{"internal/domain", "internal/service", "internal/storage", "internal/transport", "cmd"}

func layerOf(name string) int {
	for i := len(layers) - 1; i >= 0; i-- {
		if strings.HasPrefix(name, layers[i]) {
			return i
		}
	}

	return -1
}

func check(graph map[string][]string) []string {
	var violations []string

	for name, dependencies := range graph {
		from := layerOf(name)

		if from < 0 {
			continue
		}

		for _, dependency := range dependencies {
			to := layerOf(dependency)

			if to < 0 {
				continue
			}

			// storage and transport are both outer layers and must not
			// import each other; anything else must point strictly inward.
			if to > from || (to == from && !strings.HasPrefix(dependency, name)) {
				violations = append(violations,
					fmt.Sprintf("%s imports %s", name, dependency))
			}
		}
	}

	sort.Strings(violations)

	return violations
}

// findCycles is a plain depth-first search. Go itself rejects import cycles,
// but the same walk over a design document catches them before the code exists.
func findCycles(graph map[string][]string) []string {
	var (
		cycles  []string
		state   = make(map[string]int) // 0 unvisited, 1 in progress, 2 done
		stack   []string
		visitFn func(string)
	)

	visitFn = func(name string) {
		state[name] = 1
		stack = append(stack, name)

		for _, dependency := range graph[name] {
			switch state[dependency] {
			case 0:
				visitFn(dependency)
			case 1:
				cycles = append(cycles, strings.Join(append(stack, dependency), " -> "))
			}
		}

		stack = stack[:len(stack)-1]
		state[name] = 2
	}

	names := make([]string, 0, len(graph))

	for name := range graph {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		if state[name] == 0 {
			visitFn(name)
		}
	}

	return cycles
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}

	return false
}
