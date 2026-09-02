// Package makefilelint checks that a Makefile is usable by someone who has
// never seen it.
//
// A Makefile is the project's user interface for contributors, and it rots the
// same way documentation does: a target gets renamed, the README still names
// the old one, and the next person spends twenty minutes on something that
// should have taken none.
//
// The checks:
//
//   - the targets a team agrees on exist (test, lint, run, migrate, ...)
//   - every target carries a "## name: description" line, so `make help` can
//     print itself instead of the reader grepping the source
//   - .PHONY lists the targets that are commands rather than files, or `make
//     test` silently does nothing the day someone adds a directory called test
//   - .DEFAULT_GOAL is set, so bare `make` is helpful rather than arbitrary
package makefilelint

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Target is one Makefile rule.
type Target struct {
	Name string
	// Help is the text from its "## name: description" comment.
	Help string
	// Line is where the rule appears.
	Line int
	// Prerequisites are the targets it depends on.
	Prerequisites []string
}

// Makefile is the parsed file.
type Makefile struct {
	Targets     []Target
	Phony       []string
	DefaultGoal string
	Variables   map[string]string
}

var (
	// A rule: "name: prereqs" at the start of a line, not a variable
	// assignment and not a pattern rule.
	rulePattern = regexp.MustCompile(`^([a-zA-Z0-9_./-]+)\s*:([^=]*)$`)
	// The help convention: "## target: description".
	helpPattern     = regexp.MustCompile(`^##\s+([a-zA-Z0-9_-]+):\s*(.+?)\s*$`)
	variablePattern = regexp.MustCompile(`^([A-Z][A-Z0-9_]*)\s*[:?+]?=\s*(.*)$`)
)

// Parse reads a Makefile.
func Parse(content string) Makefile {
	result := Makefile{Variables: make(map[string]string)}

	helps := make(map[string]string)

	lines := strings.Split(content, "\n")

	for number, line := range lines {
		trimmed := strings.TrimRight(line, " \t\r")

		// A recipe line starts with a tab; nothing structural does.
		if strings.HasPrefix(trimmed, "\t") {
			continue
		}

		if match := helpPattern.FindStringSubmatch(trimmed); match != nil {
			helps[match[1]] = match[2]

			continue
		}

		if strings.HasPrefix(trimmed, "#") {
			continue
		}

		if match := variablePattern.FindStringSubmatch(trimmed); match != nil {
			if match[1] == "DEFAULT_GOAL" {
				result.DefaultGoal = strings.TrimSpace(match[2])
			}

			result.Variables[match[1]] = strings.TrimSpace(match[2])

			continue
		}

		if strings.HasPrefix(trimmed, ".DEFAULT_GOAL") {
			_, value, _ := strings.Cut(trimmed, "=")
			result.DefaultGoal = strings.TrimSpace(value)

			continue
		}

		if strings.HasPrefix(trimmed, ".PHONY") {
			_, value, _ := strings.Cut(trimmed, ":")
			result.Phony = append(result.Phony, strings.Fields(value)...)

			continue
		}

		match := rulePattern.FindStringSubmatch(trimmed)
		if match == nil || strings.HasPrefix(match[1], ".") {
			continue
		}

		result.Targets = append(result.Targets, Target{
			Name:          match[1],
			Line:          number + 1,
			Prerequisites: strings.Fields(match[2]),
		})
	}

	for i := range result.Targets {
		result.Targets[i].Help = helps[result.Targets[i].Name]
	}

	return result
}

// Load reads and parses a Makefile from disk.
func Load(path string) (Makefile, error) {
	content, err := os.ReadFile(path) //nolint:gosec // the path comes from the caller
	if err != nil {
		return Makefile{}, fmt.Errorf("read %s: %w", path, err)
	}

	return Parse(string(content)), nil
}

// Has reports whether a target is defined.
func (m Makefile) Has(name string) bool {
	for _, target := range m.Targets {
		if target.Name == name {
			return true
		}
	}

	return false
}

// Names returns every target name, sorted.
func (m Makefile) Names() []string {
	names := make([]string, 0, len(m.Targets))

	for _, target := range m.Targets {
		names = append(names, target.Name)
	}

	sort.Strings(names)

	return names
}

// RequiredTargets is the interface a contributor can rely on in any of the
// team's repositories.
//
// The value is the consistency, not the list. Someone moving between four
// services should not have to learn four ways to run the tests.
var RequiredTargets = []string{"help", "setup", "run", "build", "test", "lint", "fmt", "migrate", "check"}

// Issue is one problem with the Makefile.
type Issue struct {
	Rule    string
	Target  string
	Line    int
	Message string
}

// String renders an issue for a terminal report.
func (i Issue) String() string {
	if i.Target == "" {
		return fmt.Sprintf("%-18s %s", i.Rule, i.Message)
	}

	return fmt.Sprintf("%-18s %-14s %s", i.Rule, i.Target, i.Message)
}

// Check validates a Makefile against the conventions.
func (m Makefile) Check(required []string) []Issue {
	var issues []Issue

	if len(m.Targets) == 0 {
		return []Issue{{Rule: "empty", Message: "no targets found - is this a Makefile?"}}
	}

	for _, name := range required {
		if !m.Has(name) {
			issues = append(issues, Issue{
				Rule: "missing_target", Target: name,
				Message: "not defined; contributors expect the same targets in every repository",
			})
		}
	}

	phony := make(map[string]bool, len(m.Phony))

	for _, name := range m.Phony {
		phony[name] = true
	}

	for _, target := range m.Targets {
		if target.Help == "" {
			issues = append(issues, Issue{
				Rule: "missing_help", Target: target.Name, Line: target.Line,
				Message: "no \"## " + target.Name + ": ...\" comment, so make help cannot describe it",
			})
		}

		if !phony[target.Name] {
			issues = append(issues, Issue{
				Rule: "not_phony", Target: target.Name, Line: target.Line,
				Message: "not in .PHONY: if a file or directory with this name appears, make will skip the target",
			})
		}
	}

	if m.DefaultGoal == "" {
		issues = append(issues, Issue{
			Rule:    "no_default_goal",
			Message: "no .DEFAULT_GOAL: bare `make` runs the first target, whatever that happens to be",
		})
	} else if m.DefaultGoal != "help" {
		issues = append(issues, Issue{
			Rule: "default_goal", Target: m.DefaultGoal,
			Message: "the default goal is not help; bare `make` should tell a newcomer what exists",
		})
	}

	// A pinned tool version is the difference between "lint fails on my
	// machine only" and a reproducible check.
	pinned := false

	for name, value := range m.Variables {
		if strings.Contains(name, "VERSION") && value != "" && value != "latest" {
			pinned = true
		}
	}

	if !pinned && m.Has("tools") {
		issues = append(issues, Issue{
			Rule:    "unpinned_tools",
			Target:  "tools",
			Message: "no pinned tool version: @latest installs a different linter for every contributor",
		})
	}

	return issues
}

// Help renders what `make help` would print, from the parsed file.
func (m Makefile) Help() string {
	var builder strings.Builder

	targets := make([]Target, len(m.Targets))
	copy(targets, m.Targets)

	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })

	for _, target := range targets {
		if target.Help == "" {
			continue
		}

		builder.WriteString(fmt.Sprintf("  %-16s %s\n", target.Name, target.Help))
	}

	return builder.String()
}
