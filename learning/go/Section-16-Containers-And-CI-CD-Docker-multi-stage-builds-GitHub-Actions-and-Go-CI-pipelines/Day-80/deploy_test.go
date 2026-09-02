package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

/*
Tests for the deployable artifacts.

The failures these catch - a root container, a grace period shorter than the
drain, a release workflow that skips the tests - are all discovered during a
deploy otherwise.
*/

// mustDuration parses a duration from the compose file, failing the test with
// a useful message rather than a panic.
func mustDuration(t *testing.T, value string) time.Duration {
	t.Helper()

	parsed, err := time.ParseDuration(value)
	if err != nil {
		t.Fatalf("duration %q: %v", value, err)
	}

	return parsed
}

func instructions(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var stripped strings.Builder

	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}

		stripped.WriteString(line)
		stripped.WriteString("\n")
	}

	return stripped.String()
}

func TestDockerfileIsProductionShaped(t *testing.T) {
	t.Parallel()

	content := instructions(t, "Dockerfile")

	checks := map[string]string{
		"FROM":               "multi-stage build",
		"CGO_ENABLED=0":      "static binary",
		"-trimpath":          "reproducible build",
		"USER nonroot":       "non-root runtime",
		`ENTRYPOINT ["`:      "exec-form entrypoint, so the binary is PID 1",
		"HEALTHCHECK":        "container health check",
		"distroless":         "minimal runtime base",
		"buildinfo.Version":  "version injected at link time",
		"--mount=type=cache": "BuildKit cache mounts",
	}

	for needle, why := range checks {
		if !strings.Contains(content, needle) {
			t.Errorf("Dockerfile is missing %q (%s)", needle, why)
		}
	}

	if strings.Count(content, "FROM ") < 2 {
		t.Error("not multi-stage: the toolchain would ship to production")
	}
}

// TestGracePeriodOutlivesTheDrain is the deploy-safety assertion: the
// container must not be SIGKILLed while it is still draining.
func TestGracePeriodOutlivesTheDrain(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile("docker-compose.yml")
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}

	var compose struct {
		Services map[string]struct {
			Environment     map[string]string `yaml:"environment"`
			StopGracePeriod string            `yaml:"stop_grace_period"`
			ReadOnly        bool              `yaml:"read_only"`
			CapDrop         []string          `yaml:"cap_drop"`
			Healthcheck     map[string]any    `yaml:"healthcheck"`
		} `yaml:"services"`
	}

	if err := yaml.Unmarshal(content, &compose); err != nil {
		t.Fatalf("compose is not valid YAML: %v", err)
	}

	api, found := compose.Services["api"]
	if !found {
		t.Fatal("no api service")
	}

	grace := mustDuration(t, api.StopGracePeriod)
	shutdown := mustDuration(t, api.Environment["SHUTDOWN_TIMEOUT"])
	drain := mustDuration(t, api.Environment["DRAIN_DELAY"])

	if grace <= shutdown+drain {
		t.Fatalf("stop_grace_period (%s) must exceed DRAIN_DELAY + SHUTDOWN_TIMEOUT (%s + %s)",
			api.StopGracePeriod, api.Environment["DRAIN_DELAY"], api.Environment["SHUTDOWN_TIMEOUT"])
	}

	if !api.ReadOnly || len(api.CapDrop) == 0 {
		t.Error("the container is not hardened (read_only + cap_drop)")
	}

	if len(api.Healthcheck) == 0 {
		t.Error("no healthcheck in the compose service")
	}
}

func TestComposeStackIsComplete(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile("docker-compose.yml")
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}

	var compose struct {
		Services map[string]any `yaml:"services"`
	}

	if err := yaml.Unmarshal(content, &compose); err != nil {
		t.Fatalf("parse: %v", err)
	}

	for _, service := range []string{"api", "prometheus"} {
		if _, found := compose.Services[service]; !found {
			t.Errorf("the stack has no %q service", service)
		}
	}

	if _, err := os.Stat("prometheus.yml"); err != nil {
		t.Error("prometheus.yml is missing, so the scrape target is undefined")
	}
}

// TestReleaseWorkflowVerifiesBeforePublishing: a tag must never be released
// without the checks a pull request gets.
func TestReleaseWorkflowVerifiesBeforePublishing(t *testing.T) {
	t.Parallel()

	workflows, err := LoadWorkflows(".github/workflows")
	if err != nil {
		t.Fatalf("load workflows: %v", err)
	}

	release, found := workflows["release.yml"]
	if !found {
		t.Fatal("no release.yml")
	}

	triggers := strings.Join(release.Triggers(), ",")

	if !strings.Contains(triggers, "push") {
		t.Error("the release workflow does not trigger on a tag push")
	}

	verify, found := release.Jobs["verify"]
	if !found {
		t.Fatal("the release workflow has no verify job")
	}

	commands := ""

	for _, step := range verify.Steps {
		commands += step.Run + "\n"
	}

	if !strings.Contains(commands, "go test") || !strings.Contains(commands, "go vet") {
		t.Error("the verify job does not run vet and tests")
	}

	for _, job := range []string{"binaries", "image"} {
		definition, found := release.Jobs[job]
		if !found {
			t.Errorf("no %q job", job)

			continue
		}

		if !strings.Contains(fmt.Sprint(definition.Needs), "verify") {
			t.Errorf("job %q does not wait for verify", job)
		}
	}
}

func TestMakefileExposesTheReleaseFlow(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}

	for _, target := range []string{"release:", "docker-build:", "tag:", "ci:", "test:", "lint:"} {
		if !strings.Contains(string(content), target) {
			t.Errorf("Makefile has no %s target", target)
		}
	}

	// The release target must inject the version, or the artifacts cannot
	// say what they are.
	if !strings.Contains(string(content), "buildinfo.Version") {
		t.Error("the Makefile does not inject the version into the binary")
	}
}

func TestDeployDocsCoverTheOperatorQuestions(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile("docs/DEPLOY.md")
	if err != nil {
		t.Fatalf("read DEPLOY.md: %v", err)
	}

	text := string(content)

	for _, topic := range []string{
		"PORT", "SHUTDOWN_TIMEOUT", "DRAIN_DELAY",
		"/healthz", "/readyz", "/metrics",
		"Rolling back", "Troubleshooting",
	} {
		if !strings.Contains(text, topic) {
			t.Errorf("DEPLOY.md does not document %q", topic)
		}
	}
}
