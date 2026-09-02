package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

/*
A small workflow reader.

A GitHub Actions workflow is YAML that nothing checks until it runs on
GitHub - a typo in a key name produces a job that silently never runs. Parsing
it locally, and asserting the properties that matter, turns that into a test.
*/

// Workflow is the subset of the schema this program cares about.
type Workflow struct {
	Name        string         `yaml:"name"`
	On          any            `yaml:"on"`
	Permissions any            `yaml:"permissions"`
	Concurrency any            `yaml:"concurrency"`
	Env         map[string]any `yaml:"env"`
	Jobs        map[string]Job `yaml:"jobs"`
}

type Job struct {
	Name           string         `yaml:"name"`
	RunsOn         any            `yaml:"runs-on"`
	TimeoutMinutes int            `yaml:"timeout-minutes"`
	Needs          any            `yaml:"needs"`
	If             string         `yaml:"if"`
	Strategy       Strategy       `yaml:"strategy"`
	Services       map[string]any `yaml:"services"`
	Steps          []Step         `yaml:"steps"`
	Permissions    any            `yaml:"permissions"`
}

type Strategy struct {
	Matrix   map[string]any `yaml:"matrix"`
	FailFast *bool          `yaml:"fail-fast"`
}

type Step struct {
	Name string         `yaml:"name"`
	Uses string         `yaml:"uses"`
	Run  string         `yaml:"run"`
	With map[string]any `yaml:"with"`
	Env  map[string]any `yaml:"env"`
	If   string         `yaml:"if"`
}

// LoadWorkflows reads every workflow in .github/workflows.
func LoadWorkflows(directory string) (map[string]Workflow, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read workflows: %w", err)
	}

	workflows := make(map[string]Workflow)

	for _, entry := range entries {
		name := entry.Name()

		if entry.IsDir() || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
			continue
		}

		content, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}

		var workflow Workflow

		if err := yaml.Unmarshal(content, &workflow); err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}

		workflows[name] = workflow
	}

	return workflows, nil
}

// Triggers normalises the `on:` field, which YAML may give us as a string, a
// list or a map.
//
// (And a classic trap: unquoted `on` is the YAML 1.1 boolean true, which some
// parsers turn into the key `true`. Handling it here is not paranoia.)
func (w Workflow) Triggers() []string {
	var triggers []string

	switch value := w.On.(type) {
	case string:
		triggers = append(triggers, value)

	case []any:
		for _, item := range value {
			triggers = append(triggers, fmt.Sprint(item))
		}

	case map[string]any:
		for key := range value {
			triggers = append(triggers, key)
		}
	}

	sort.Strings(triggers)

	return triggers
}

// UsedActions lists the actions a workflow depends on, with their versions.
func (w Workflow) UsedActions() []string {
	seen := make(map[string]struct{})

	for _, job := range w.Jobs {
		for _, step := range job.Steps {
			if step.Uses != "" {
				seen[step.Uses] = struct{}{}
			}
		}
	}

	actions := make([]string, 0, len(seen))

	for action := range seen {
		actions = append(actions, action)
	}

	sort.Strings(actions)

	return actions
}

// Findings reports problems worth fixing. Each one is a real failure mode,
// not a style preference.
func (w Workflow) Findings() []string {
	var findings []string

	if w.Permissions == nil {
		findings = append(findings,
			"no top-level permissions: the token defaults to broad write access")
	}

	for name, job := range w.Jobs {
		if job.TimeoutMinutes == 0 {
			findings = append(findings,
				fmt.Sprintf("job %q has no timeout-minutes: a hung job can run for six hours", name))
		}

		if job.RunsOn == nil {
			findings = append(findings, fmt.Sprintf("job %q has no runs-on", name))
		}

		if len(job.Steps) == 0 {
			findings = append(findings, fmt.Sprintf("job %q has no steps", name))
		}

		for _, step := range job.Steps {
			// ${{ }} inside a run block interpolates BEFORE the shell sees it,
			// so attacker-controlled text (a PR title, a branch name, an issue
			// body) becomes shell code.
			if step.Run != "" && strings.Contains(step.Run, "${{ github.event") {
				findings = append(findings, fmt.Sprintf(
					"job %q step %q interpolates github.event into a run block: pass it through env instead",
					name, step.Name))
			}

			if step.Uses != "" && !strings.Contains(step.Uses, "@") {
				findings = append(findings, fmt.Sprintf(
					"job %q uses %q without a version", name, step.Uses))
			}
		}
	}

	sort.Strings(findings)

	return findings
}
