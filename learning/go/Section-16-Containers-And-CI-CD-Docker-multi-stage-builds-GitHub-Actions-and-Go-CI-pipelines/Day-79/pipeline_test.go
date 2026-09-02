package main

import (
	"fmt"
	"strings"
	"testing"
)

func pipeline(t *testing.T) Workflow {
	t.Helper()

	workflows, err := LoadWorkflows(".github/workflows")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	workflow, found := workflows["ci.yml"]
	if !found {
		t.Fatal("no ci.yml")
	}

	return workflow
}

func TestEveryStageExists(t *testing.T) {
	t.Parallel()

	workflow := pipeline(t)

	for _, job := range []string{"lint", "test", "integration", "coverage", "build", "image"} {
		if _, found := workflow.Jobs[job]; !found {
			t.Errorf("no %q job", job)
		}
	}
}

// TestLintJobRunsEveryChecker: each of these catches a different class of
// mistake, and dropping one is a silent loss of coverage.
func TestLintJobRunsEveryChecker(t *testing.T) {
	t.Parallel()

	job := pipeline(t).Jobs["lint"]

	commands := strings.Join(stepCommands(job), "\n")

	for _, checker := range []string{"gofmt", "go vet", "govulncheck"} {
		if !strings.Contains(commands, checker) {
			t.Errorf("lint job does not run %s", checker)
		}
	}

	if !usesAction(job, "golangci/golangci-lint-action") {
		t.Error("lint job does not run golangci-lint")
	}
}

func TestTestJobUsesRaceAndShuffle(t *testing.T) {
	t.Parallel()

	commands := strings.Join(stepCommands(pipeline(t).Jobs["test"]), "\n")

	if !strings.Contains(commands, "-race") {
		t.Error("tests run without -race: data races would reach production")
	}

	if !strings.Contains(commands, "-shuffle=on") {
		t.Error("tests run without -shuffle=on: order-dependent tests stay hidden")
	}

	if !strings.Contains(commands, "go build ./...") {
		t.Error("the job never builds every package")
	}
}

func TestMatrixCoversMoreThanOneVersion(t *testing.T) {
	t.Parallel()

	job := pipeline(t).Jobs["test"]

	if len(job.Strategy.Matrix) == 0 {
		t.Fatal("the test job has no matrix")
	}

	versions, found := job.Strategy.Matrix["go"]
	if !found {
		t.Fatal("the matrix does not vary the Go version")
	}

	list, ok := versions.([]any)
	if !ok || len(list) < 2 {
		t.Fatalf("go versions = %v, want at least two", versions)
	}

	// fail-fast must be off, or one failing cell hides the others.
	if job.Strategy.FailFast == nil || *job.Strategy.FailFast {
		t.Error("fail-fast is not disabled: a failure in one cell cancels the rest")
	}
}

// TestServiceContainersHaveHealthChecks is the anti-flake assertion: without
// a health check the job starts querying before the database is listening.
func TestServiceContainersHaveHealthChecks(t *testing.T) {
	t.Parallel()

	job := pipeline(t).Jobs["integration"]

	if len(job.Services) == 0 {
		t.Fatal("the integration job has no service containers")
	}

	for name, definition := range job.Services {
		service, ok := definition.(map[string]any)
		if !ok {
			t.Errorf("service %q is not a mapping", name)

			continue
		}

		options, _ := service["options"].(string)

		if !strings.Contains(options, "--health-cmd") {
			t.Errorf("service %q has no health check: the job will start before it is ready", name)
		}

		if _, found := service["image"]; !found {
			t.Errorf("service %q has no image", name)
		}
	}
}

func TestIntegrationJobUsesTheBuildTag(t *testing.T) {
	t.Parallel()

	commands := strings.Join(stepCommands(pipeline(t).Jobs["integration"]), "\n")

	if !strings.Contains(commands, "-tags=integration") {
		t.Error("the integration job does not select the tagged suite")
	}
}

func TestCoverageIsPublished(t *testing.T) {
	t.Parallel()

	job := pipeline(t).Jobs["coverage"]

	commands := strings.Join(stepCommands(job), "\n")

	if !strings.Contains(commands, "-coverprofile") {
		t.Error("no coverage profile is produced")
	}

	if !strings.Contains(commands, "GITHUB_STEP_SUMMARY") {
		t.Error("coverage is not written to the job summary, so nobody sees it")
	}

	if !usesAction(job, "actions/upload-artifact") {
		t.Error("the coverage profile is not uploaded as an artifact")
	}
}

// TestBuildWaitsForTheChecks: producing an artifact from code that failed its
// tests is how a broken build gets deployed.
func TestBuildWaitsForTheChecks(t *testing.T) {
	t.Parallel()

	job := pipeline(t).Jobs["build"]

	needs := fmt.Sprint(job.Needs)

	for _, required := range []string{"lint", "test", "integration"} {
		if !strings.Contains(needs, required) {
			t.Errorf("build does not wait for %s", required)
		}
	}
}

// TestImageIsNotPushedFromPullRequests is the security assertion: a fork PR
// must never receive registry credentials.
func TestImageIsNotPushedFromPullRequests(t *testing.T) {
	t.Parallel()

	job := pipeline(t).Jobs["image"]

	var (
		loginGuarded bool
		pushGuarded  bool
	)

	for _, step := range job.Steps {
		if strings.Contains(step.Uses, "docker/login-action") {
			if strings.Contains(step.If, "pull_request") {
				loginGuarded = true
			}
		}

		if strings.Contains(step.Uses, "docker/build-push-action") {
			if push, found := step.With["push"]; found && strings.Contains(fmt.Sprint(push), "pull_request") {
				pushGuarded = true
			}
		}
	}

	if !loginGuarded {
		t.Error("the registry login is not guarded against pull requests")
	}

	if !pushGuarded {
		t.Error("the image push is not guarded against pull requests")
	}
}

func TestImageIsScanned(t *testing.T) {
	t.Parallel()

	job := pipeline(t).Jobs["image"]

	if !usesAction(job, "aquasecurity/trivy-action") {
		t.Fatal("the image is never scanned")
	}

	for _, step := range job.Steps {
		if !strings.Contains(step.Uses, "trivy") {
			continue
		}

		if fmt.Sprint(step.With["exit-code"]) != "1" {
			t.Error("the scanner does not fail the build")
		}

		// Failing on CVEs with no available fix teaches everyone to ignore
		// the scanner.
		if fmt.Sprint(step.With["ignore-unfixed"]) != "true" {
			t.Error("the scanner is not set to ignore unfixable findings")
		}
	}
}

func TestPermissionsAreScopedPerJob(t *testing.T) {
	t.Parallel()

	workflow := pipeline(t)

	if workflow.Permissions == nil {
		t.Fatal("no top-level permissions")
	}

	// The two jobs that need more than read must declare it themselves,
	// rather than the whole workflow running with write access.
	for _, name := range []string{"coverage", "image"} {
		if workflow.Jobs[name].Permissions == nil {
			t.Errorf("job %q does not declare its own permissions", name)
		}
	}
}

func TestNoJobIsUnbounded(t *testing.T) {
	t.Parallel()

	for name, job := range pipeline(t).Jobs {
		if job.TimeoutMinutes == 0 {
			t.Errorf("job %q has no timeout", name)
		}

		if job.TimeoutMinutes > 30 {
			t.Errorf("job %q allows %d minutes: too long to be a useful signal",
				name, job.TimeoutMinutes)
		}
	}
}

//
// HELPERS
//

func stepCommands(job Job) []string {
	commands := make([]string, 0, len(job.Steps))

	for _, step := range job.Steps {
		if step.Run != "" {
			commands = append(commands, step.Run)
		}
	}

	return commands
}

func usesAction(job Job, action string) bool {
	for _, step := range job.Steps {
		if strings.HasPrefix(step.Uses, action) {
			return true
		}
	}

	return false
}
