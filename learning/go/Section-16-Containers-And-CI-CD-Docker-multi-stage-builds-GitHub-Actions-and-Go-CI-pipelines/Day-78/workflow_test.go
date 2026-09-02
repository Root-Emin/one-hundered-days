package main

import (
	"os"
	"strings"
	"testing"
)

/*
Tests for the workflows themselves.

A broken workflow is only discovered when it runs on GitHub - and a workflow
that silently does not trigger is not discovered at all. These tests turn both
into a local failure.
*/

func loadTestWorkflows(t *testing.T) map[string]Workflow {
	t.Helper()

	workflows, err := LoadWorkflows(".github/workflows")
	if err != nil {
		t.Fatalf("load workflows: %v", err)
	}

	if len(workflows) == 0 {
		t.Fatal("no workflows found")
	}

	return workflows
}

func TestWorkflowsParse(t *testing.T) {
	t.Parallel()

	for name, workflow := range loadTestWorkflows(t) {
		if workflow.Name == "" {
			t.Errorf("%s has no name", name)
		}

		if len(workflow.Jobs) == 0 {
			t.Errorf("%s has no jobs", name)
		}
	}
}

// TestTriggersAreReal catches the YAML 1.1 trap: an unquoted `on` key can be
// parsed as the boolean true, leaving a workflow with no triggers at all.
func TestTriggersAreReal(t *testing.T) {
	t.Parallel()

	for name, workflow := range loadTestWorkflows(t) {
		triggers := workflow.Triggers()

		if len(triggers) == 0 {
			t.Errorf("%s has no triggers: check that `on:` was not parsed as a boolean", name)

			continue
		}

		for _, trigger := range triggers {
			if trigger == "true" || trigger == "false" {
				t.Errorf("%s: `on` was parsed as a boolean (%s)", name, trigger)
			}
		}
	}
}

func TestCIRunsOnPushAndPullRequest(t *testing.T) {
	t.Parallel()

	workflow, found := loadTestWorkflows(t)["ci.yml"]
	if !found {
		t.Fatal("no ci.yml")
	}

	triggers := strings.Join(workflow.Triggers(), ",")

	for _, expected := range []string{"push", "pull_request"} {
		if !strings.Contains(triggers, expected) {
			t.Errorf("ci.yml does not trigger on %s", expected)
		}
	}
}

// TestPermissionsAreDeclared: without a permissions block the GITHUB_TOKEN has
// broad write access, and a compromised action can push to the repository.
func TestPermissionsAreDeclared(t *testing.T) {
	t.Parallel()

	for name, workflow := range loadTestWorkflows(t) {
		if workflow.Permissions == nil {
			t.Errorf("%s has no top-level permissions block", name)
		}
	}
}

func TestJobsHaveTimeouts(t *testing.T) {
	t.Parallel()

	for name, workflow := range loadTestWorkflows(t) {
		for jobName, job := range workflow.Jobs {
			if job.TimeoutMinutes == 0 {
				t.Errorf("%s job %q has no timeout-minutes", name, jobName)
			}
		}
	}
}

func TestActionsArePinnedToAVersion(t *testing.T) {
	t.Parallel()

	for name, workflow := range loadTestWorkflows(t) {
		for _, action := range workflow.UsedActions() {
			if !strings.Contains(action, "@") {
				t.Errorf("%s uses %q with no version: the action can change under you", name, action)
			}
		}
	}
}

// TestGoVersionComesFromGoMod: hardcoding a Go version in the workflow is how
// CI ends up testing on a different toolchain than production runs.
func TestGoVersionComesFromGoMod(t *testing.T) {
	t.Parallel()

	workflow := loadTestWorkflows(t)["ci.yml"]

	var checked bool

	for _, job := range workflow.Jobs {
		for _, step := range job.Steps {
			if !strings.HasPrefix(step.Uses, "actions/setup-go") {
				continue
			}

			checked = true

			if _, found := step.With["go-version-file"]; !found {
				t.Error("setup-go does not use go-version-file: the toolchain can drift from go.mod")
			}

			if cache, found := step.With["cache"]; !found || cache != true {
				t.Error("setup-go does not enable caching: every run downloads the module graph")
			}
		}
	}

	if !checked {
		t.Fatal("ci.yml does not set up Go")
	}
}

func TestCIRunsTheTests(t *testing.T) {
	t.Parallel()

	workflow := loadTestWorkflows(t)["ci.yml"]

	var (
		builds bool
		tests  bool
		race   bool
	)

	for _, job := range workflow.Jobs {
		for _, step := range job.Steps {
			switch {
			case strings.Contains(step.Run, "go build"):
				builds = true
			case strings.Contains(step.Run, "go test"):
				tests = true

				if strings.Contains(step.Run, "-race") {
					race = true
				}
			}
		}
	}

	if !builds {
		t.Error("ci.yml never runs go build")
	}

	if !tests {
		t.Error("ci.yml never runs go test")
	}

	if !race {
		t.Error("ci.yml runs tests without -race: data races would only appear in production")
	}
}

// TestNoScriptInjection is the security test: interpolating attacker-controlled
// text into a run block is remote code execution on the runner.
func TestNoScriptInjection(t *testing.T) {
	t.Parallel()

	for name, workflow := range loadTestWorkflows(t) {
		for jobName, job := range workflow.Jobs {
			for _, step := range job.Steps {
				if step.Run == "" {
					continue
				}

				for _, dangerous := range []string{
					"${{ github.event.pull_request.title",
					"${{ github.event.pull_request.body",
					"${{ github.event.issue.title",
					"${{ github.event.comment.body",
					"${{ github.head_ref",
				} {
					if strings.Contains(step.Run, dangerous) {
						t.Errorf("%s job %q step %q interpolates %s into a shell: pass it via env instead",
							name, jobName, step.Name, dangerous)
					}
				}
			}
		}
	}
}

func TestWorkflowFilesAreWhereGitHubLooks(t *testing.T) {
	t.Parallel()

	// GitHub only reads .github/workflows at the REPOSITORY root. The copy in
	// this day directory is a teaching artefact, and the day's README says so.
	entries, err := os.ReadDir(".github/workflows")
	if err != nil {
		t.Fatalf("read directory: %v", err)
	}

	if len(entries) < 2 {
		t.Fatalf("expected at least two workflow files, found %d", len(entries))
	}
}
