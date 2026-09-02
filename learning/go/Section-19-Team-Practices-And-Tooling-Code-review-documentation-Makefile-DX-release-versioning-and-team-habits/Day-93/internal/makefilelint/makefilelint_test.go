package makefilelint_test

import (
	"strings"
	"testing"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-93/internal/makefilelint"
)

// The project's own Makefile has to pass its own linter, or the rules are
// aspirational.
func TestProjectMakefileIsClean(t *testing.T) {
	makefile, err := makefilelint.Load("../../Makefile")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, issue := range makefile.Check(makefilelint.RequiredTargets) {
		t.Errorf("%s", issue)
	}
}

func TestProjectMakefileHasEveryRequiredTarget(t *testing.T) {
	makefile, err := makefilelint.Load("../../Makefile")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, name := range makefilelint.RequiredTargets {
		if !makefile.Has(name) {
			t.Errorf("missing target: %s", name)
		}
	}
}

func TestParseFindsTargetsHelpAndPhony(t *testing.T) {
	content := `
BINARY := app
GOLANGCI_VERSION := v2.6.2

.DEFAULT_GOAL := help

.PHONY: help test

## help: print this help
help:
	@echo hi

## test: run the tests
test: build
	go test ./...

build:
	go build ./...
`

	makefile := makefilelint.Parse(content)

	if len(makefile.Targets) != 3 {
		t.Fatalf("targets = %v, want 3", makefile.Names())
	}

	if makefile.DefaultGoal != "help" {
		t.Errorf("default goal = %q, want help", makefile.DefaultGoal)
	}

	for _, target := range makefile.Targets {
		if target.Name != "test" {
			continue
		}

		if target.Help != "run the tests" {
			t.Errorf("test help = %q", target.Help)
		}

		if len(target.Prerequisites) != 1 || target.Prerequisites[0] != "build" {
			t.Errorf("test prerequisites = %v, want [build]", target.Prerequisites)
		}
	}

	if makefile.Variables["BINARY"] != "app" {
		t.Errorf("BINARY = %q", makefile.Variables["BINARY"])
	}
}

// A recipe line starts with a tab, and a recipe can contain a colon. Parsing
// one as a rule would invent targets that do not exist.
func TestRecipeLinesAreNotParsedAsTargets(t *testing.T) {
	content := "## run: start it\nrun:\n\t@echo \"note: this contains a colon\"\n\tcurl http://localhost:8080\n"

	makefile := makefilelint.Parse(content)

	if len(makefile.Targets) != 1 || makefile.Targets[0].Name != "run" {
		t.Errorf("targets = %v, want [run]", makefile.Names())
	}
}

func TestCheckRules(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		required []string
		rule     string
	}{
		{
			name:     "missing target",
			content:  ".DEFAULT_GOAL := help\n.PHONY: help\n## help: h\nhelp:\n\t@echo hi\n",
			required: []string{"help", "test"},
			rule:     "missing_target",
		},
		{
			name:     "missing help comment",
			content:  ".DEFAULT_GOAL := help\n.PHONY: help test\n## help: h\nhelp:\n\t@echo\ntest:\n\tgo test ./...\n",
			required: []string{"help"},
			rule:     "missing_help",
		},
		{
			name:     "not phony",
			content:  ".DEFAULT_GOAL := help\n.PHONY: help\n## help: h\nhelp:\n\t@echo\n## test: t\ntest:\n\tgo test ./...\n",
			required: []string{"help"},
			rule:     "not_phony",
		},
		{
			name:     "no default goal",
			content:  ".PHONY: help\n## help: h\nhelp:\n\t@echo\n",
			required: []string{"help"},
			rule:     "no_default_goal",
		},
		{
			name:     "default goal is not help",
			content:  ".DEFAULT_GOAL := build\n.PHONY: help build\n## help: h\nhelp:\n\t@echo\n## build: b\nbuild:\n\tgo build ./...\n",
			required: []string{"help"},
			rule:     "default_goal",
		},
		{
			name: "unpinned tools",
			content: ".DEFAULT_GOAL := help\n.PHONY: help tools\n## help: h\nhelp:\n\t@echo\n" +
				"## tools: install tools\ntools:\n\tgo install example.com/linter@latest\n",
			required: []string{"help"},
			rule:     "unpinned_tools",
		},
		{
			name:     "not a makefile",
			content:  "just some text\n",
			required: []string{"help"},
			rule:     "empty",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			issues := makefilelint.Parse(testCase.content).Check(testCase.required)

			found := false

			for _, issue := range issues {
				if issue.Rule == testCase.rule {
					found = true
				}
			}

			if !found {
				t.Errorf("expected rule %s, got %v", testCase.rule, issues)
			}
		})
	}
}

func TestHelpRendersDocumentedTargetsOnly(t *testing.T) {
	makefile := makefilelint.Parse(
		".DEFAULT_GOAL := help\n.PHONY: help secret\n## help: print this help\nhelp:\n\t@echo\nsecret:\n\t@echo\n")

	help := makefile.Help()

	if !strings.Contains(help, "help") || !strings.Contains(help, "print this help") {
		t.Errorf("help = %q", help)
	}

	if strings.Contains(help, "secret") {
		t.Errorf("an undocumented target appeared in help: %q", help)
	}
}
