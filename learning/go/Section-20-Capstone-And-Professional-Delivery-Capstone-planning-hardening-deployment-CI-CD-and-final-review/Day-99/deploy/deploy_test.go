// Package deploy holds tests for the deployment artefacts.
//
// The Dockerfile, the compose file and the workflows are configuration nobody
// checks until they run - on someone else's machine, weeks from now, and the
// first sign one is wrong is usually an outage.
//
// They are text, and text can be parsed and asserted. That is what makes
// deployment configuration testable at all, and it needs no Docker daemon:
// these tests run in CI on a runner that has one and on a laptop that does not.
package deploy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// root is the day's directory, from this package.
const root = ".."

func read(t *testing.T, name string) string {
	t.Helper()

	content, err := os.ReadFile(filepath.Join(root, name)) //nolint:gosec // fixed paths in the repo
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}

	return string(content)
}

// instructions returns the Dockerfile's directives with comments stripped, so
// an assertion cannot be satisfied by a comment that merely mentions the word.
func instructions(t *testing.T) []string {
	t.Helper()

	var kept []string

	for _, line := range strings.Split(read(t, "Dockerfile"), "\n") {
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		kept = append(kept, trimmed)
	}

	return kept
}

func TestDockerfileIsMultiStage(t *testing.T) {
	froms := 0

	for _, line := range instructions(t) {
		if strings.HasPrefix(strings.ToUpper(line), "FROM ") {
			froms++
		}
	}

	if froms < 2 {
		t.Errorf("FROM count = %d, want at least 2: the shipped image must not contain the compiler", froms)
	}
}

// "nonroot" is the difference between a container escape landing as uid 0 and
// landing as nobody.
func TestImageRunsAsNonRoot(t *testing.T) {
	user := ""

	for _, line := range instructions(t) {
		if strings.HasPrefix(strings.ToUpper(line), "USER ") {
			user = strings.TrimSpace(line[len("USER "):])
		}
	}

	if user == "" {
		t.Fatal("no USER instruction: the container would run as root")
	}

	if strings.HasPrefix(user, "root") || user == "0" || strings.HasPrefix(user, "0:") {
		t.Errorf("USER = %q", user)
	}
}

// A floating tag is how a rebuild silently changes which vulnerabilities you
// have - which is exactly what Day 98's govulncheck run was about.
func TestBaseImagesArePinned(t *testing.T) {
	for _, line := range instructions(t) {
		if !strings.HasPrefix(strings.ToUpper(line), "FROM ") {
			continue
		}

		image := strings.Fields(strings.TrimPrefix(line, "FROM "))[0]

		if strings.HasPrefix(image, "--platform") {
			image = strings.Fields(line)[2]
		}

		if strings.HasSuffix(image, ":latest") || !strings.Contains(image, ":") {
			t.Errorf("unpinned base image %q", image)
		}
	}

	// And the Go version is a patch version, not a minor one.
	if !strings.Contains(read(t, "Dockerfile"), "golang:1.26.6") {
		t.Error("the Go version is not pinned to the patched 1.26.6 (see docs/SECURITY.md)")
	}
}

// The shell form makes the process a child of /bin/sh, which does not forward
// SIGTERM - so every deploy becomes a 30-second wait followed by SIGKILL.
func TestEntrypointUsesTheExecForm(t *testing.T) {
	found := false

	for _, line := range instructions(t) {
		if !strings.HasPrefix(strings.ToUpper(line), "ENTRYPOINT") {
			continue
		}

		found = true

		if !strings.Contains(line, "[") {
			t.Errorf("ENTRYPOINT %q uses the shell form; SIGTERM would not reach the process", line)
		}
	}

	if !found {
		t.Error("no ENTRYPOINT")
	}
}

func TestBuildIsReproducible(t *testing.T) {
	dockerfile := read(t, "Dockerfile")

	for _, flag := range []string{"-trimpath", "-buildvcs=false", "CGO_ENABLED=0"} {
		if !strings.Contains(dockerfile, flag) {
			t.Errorf("the build is missing %s, which makes it unreproducible", flag)
		}
	}

	// The COMMIT time, not the build time.
	if !strings.Contains(dockerfile, "COMMIT_TIME") {
		t.Error("no COMMIT_TIME build arg: a build timestamp makes the image unreproducible by construction")
	}

	if strings.Contains(dockerfile, "date +") || strings.Contains(dockerfile, "$(date") {
		t.Error("the build stamps the current date, which changes the image on every build")
	}
}

func TestDockerignoreExcludesTheDatabaseAndSecrets(t *testing.T) {
	ignored := read(t, ".dockerignore")

	for _, pattern := range []string{".git", "*.db", ".envrc"} {
		if !strings.Contains(ignored, pattern) {
			t.Errorf(".dockerignore does not exclude %q", pattern)
		}
	}
}

//
// COMPOSE
//

type composeFile struct {
	Services map[string]struct {
		Image           string            `yaml:"image"`
		Ports           []string          `yaml:"ports"`
		Environment     map[string]string `yaml:"environment"`
		Volumes         []string          `yaml:"volumes"`
		StopGracePeriod string            `yaml:"stop_grace_period"`
		ReadOnly        bool              `yaml:"read_only"`
		SecurityOpt     []string          `yaml:"security_opt"`
		CapDrop         []string          `yaml:"cap_drop"`
		Healthcheck     struct {
			Test []string `yaml:"test"`
		} `yaml:"healthcheck"`
	} `yaml:"services"`
	Volumes map[string]any `yaml:"volumes"`
}

func compose(t *testing.T) composeFile {
	t.Helper()

	var parsed composeFile

	if err := yaml.Unmarshal([]byte(read(t, "docker-compose.yml")), &parsed); err != nil {
		t.Fatalf("parse docker-compose.yml: %v", err)
	}

	return parsed
}

// A container's filesystem is discarded on restart. Without a volume, every
// deploy is a data loss incident.
func TestDatabaseLivesOnAVolume(t *testing.T) {
	service := compose(t).Services["linkr"]

	found := false

	for _, volume := range service.Volumes {
		if strings.Contains(volume, ":/data") {
			found = true
		}
	}

	if !found {
		t.Errorf("no volume mounted at /data: volumes = %v", service.Volumes)
	}

	if service.Environment["LINKR_DATABASE_URL"] != "/data/linkr.db" {
		t.Errorf("the database is not on the volume: %q", service.Environment["LINKR_DATABASE_URL"])
	}
}

// The grace period has to outlive the drain, or a rolling deploy kills
// in-flight requests instead of finishing them.
func TestStopGracePeriodOutlivesTheDrain(t *testing.T) {
	service := compose(t).Services["linkr"]

	if service.StopGracePeriod == "" {
		t.Fatal("no stop_grace_period: docker's 10s default is shorter than the shutdown timeout")
	}

	// The service's own defaults: 15s shutdown + 3s drain delay in production.
	if service.StopGracePeriod != "45s" {
		t.Errorf("stop_grace_period = %q; it must exceed the 15s shutdown timeout plus the drain delay",
			service.StopGracePeriod)
	}
}

// /metrics hands an attacker the traffic shape, error rate and deployment
// size. It binds to localhost inside the container and must not be published.
func TestMetricsPortIsNotPublished(t *testing.T) {
	service := compose(t).Services["linkr"]

	for _, port := range service.Ports {
		if strings.Contains(port, "9096") {
			t.Errorf("the metrics port is published: %q", port)
		}
	}

	if !strings.HasPrefix(service.Environment["LINKR_METRICS_ADDR"], "127.0.0.1") {
		t.Errorf("LINKR_METRICS_ADDR = %q, want a loopback address",
			service.Environment["LINKR_METRICS_ADDR"])
	}
}

func TestContainerIsHardened(t *testing.T) {
	service := compose(t).Services["linkr"]

	if !service.ReadOnly {
		t.Error("read_only is not set: the binary could rewrite itself")
	}

	if !contains(service.SecurityOpt, "no-new-privileges:true") {
		t.Errorf("security_opt = %v, want no-new-privileges", service.SecurityOpt)
	}

	if !contains(service.CapDrop, "ALL") {
		t.Errorf("cap_drop = %v, want ALL - the service needs no capabilities", service.CapDrop)
	}
}

// The compose file must run in the production configuration, or it proves
// nothing about what gets deployed.
func TestComposeRunsProductionConfiguration(t *testing.T) {
	service := compose(t).Services["linkr"]

	if service.Environment["LINKR_ENV"] != "production" {
		t.Errorf("LINKR_ENV = %q, want production", service.Environment["LINKR_ENV"])
	}
}

//
// WORKFLOWS
//

type workflow struct {
	Name        string         `yaml:"name"`
	Permissions map[string]any `yaml:"permissions"`
	Jobs        map[string]struct {
		RunsOn         string            `yaml:"runs-on"`
		TimeoutMinutes int               `yaml:"timeout-minutes"`
		Needs          any               `yaml:"needs"`
		Env            map[string]string `yaml:"env"`
		Steps          []struct {
			Name string            `yaml:"name"`
			Uses string            `yaml:"uses"`
			Run  string            `yaml:"run"`
			If   string            `yaml:"if"`
			Env  map[string]string `yaml:"env"`
			With map[string]any    `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func loadWorkflow(t *testing.T, name string) workflow {
	t.Helper()

	var parsed workflow

	if err := yaml.Unmarshal([]byte(read(t, filepath.Join(".github", "workflows", name))), &parsed); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}

	return parsed
}

// A hung job with no timeout runs for six hours and bills for all of them.
func TestEveryJobHasATimeout(t *testing.T) {
	for _, name := range []string{"ci.yml", "release.yml"} {
		parsed := loadWorkflow(t, name)

		for jobName, job := range parsed.Jobs {
			if job.TimeoutMinutes == 0 {
				t.Errorf("%s: job %q has no timeout-minutes", name, jobName)
			}
		}
	}
}

// The default token can write to the repository; narrowing it is free.
func TestWorkflowsNarrowTheirPermissions(t *testing.T) {
	for _, name := range []string{"ci.yml", "release.yml"} {
		if len(loadWorkflow(t, name).Permissions) == 0 {
			t.Errorf("%s has no top-level permissions block", name)
		}
	}
}

// An unpinned action is code you did not review running with your token.
func TestActionsArePinned(t *testing.T) {
	for _, name := range []string{"ci.yml", "release.yml"} {
		parsed := loadWorkflow(t, name)

		for jobName, job := range parsed.Jobs {
			for _, step := range job.Steps {
				if step.Uses != "" && !strings.Contains(step.Uses, "@") {
					t.Errorf("%s: job %q uses %q with no version", name, jobName, step.Uses)
				}
			}
		}
	}
}

// A pull request from a fork must never publish an image: that is a
// supply-chain compromise with a green checkmark.
func TestImagesArePushedOnlyFromMain(t *testing.T) {
	parsed := loadWorkflow(t, "ci.yml")

	job, found := parsed.Jobs["image"]
	if !found {
		t.Fatal("no image job in ci.yml")
	}

	for _, step := range job.Steps {
		push, hasPush := step.With["push"]
		if !hasPush {
			continue
		}

		condition, _ := push.(string)

		if !strings.Contains(condition, "refs/heads/main") {
			t.Errorf("the image is pushed with condition %q, which is not restricted to main", condition)
		}
	}
}

// The image must not be built until the code is known good.
func TestImageJobWaitsForTheChecks(t *testing.T) {
	job := loadWorkflow(t, "ci.yml").Jobs["image"]

	needs, ok := job.Needs.([]any)
	if !ok {
		t.Fatalf("the image job's needs = %v, want a list", job.Needs)
	}

	required := map[string]bool{"test": false, "lint": false, "vulnerabilities": false}

	for _, need := range needs {
		if name, ok := need.(string); ok {
			required[name] = true
		}
	}

	for name, present := range required {
		if !present {
			t.Errorf("the image job does not wait for %q", name)
		}
	}
}

// The release runs the tests AGAIN on the tag: a tag can be moved, and a
// release nobody verified is a release nobody can trust.
func TestReleaseVerifiesBeforePublishing(t *testing.T) {
	parsed := loadWorkflow(t, "release.yml")

	verify, found := parsed.Jobs["verify"]
	if !found {
		t.Fatal("no verify job in release.yml")
	}

	commands := ""

	for _, step := range verify.Steps {
		commands += step.Run + "\n"
	}

	for _, want := range []string{"go test", "go vet", "govulncheck"} {
		if !strings.Contains(commands, want) {
			t.Errorf("the verify job does not run %s", want)
		}
	}

	for _, jobName := range []string{"binaries", "image"} {
		job := parsed.Jobs[jobName]

		needs, _ := job.Needs.([]any)

		if len(needs) == 0 {
			t.Errorf("the %s job does not wait for verify", jobName)
		}
	}
}

// Checksums travel with the artefacts, so a download can be verified by
// someone who does not trust the transport.
func TestReleasePublishesChecksums(t *testing.T) {
	parsed := loadWorkflow(t, "release.yml")

	job := parsed.Jobs["binaries"]

	// A build flag can live in the run script OR in an env block, and both
	// have the same effect - so the assertion looks at both rather than at
	// the shape someone happened to write it in.
	commands := ""

	for name, value := range job.Env {
		commands += name + "=" + value + "\n"
	}

	for _, step := range job.Steps {
		commands += step.Run + "\n"

		for name, value := range step.Env {
			commands += name + "=" + value + "\n"
		}
	}

	if !strings.Contains(commands, "sha256sum") {
		t.Error("the release does not publish checksums")
	}

	for _, flag := range []string{"-trimpath", "CGO_ENABLED", "-buildvcs=false"} {
		if !strings.Contains(commands, flag) {
			t.Errorf("the release binaries are missing %s, which makes them unreproducible", flag)
		}
	}

	// The COMMIT time, not the build time.
	if !strings.Contains(commands, "%cI") {
		t.Error("the release stamps something other than the commit time")
	}
}

// CI must run govulncheck: it is the check that would have found Day 98's
// five standard-library findings without a human going looking.
func TestCIScansDependencies(t *testing.T) {
	parsed := loadWorkflow(t, "ci.yml")

	if _, found := parsed.Jobs["vulnerabilities"]; !found {
		t.Fatal("ci.yml has no vulnerabilities job")
	}
}

//
// THE RUNBOOK
//

// A runbook that does not answer these is a runbook nobody can follow at 3am.
func TestRunbookAnswersTheOperatorsQuestions(t *testing.T) {
	runbook := strings.ToLower(read(t, filepath.Join("docs", "RUNBOOK.md")))

	for _, question := range []string{
		"rollback",
		"migration",
		"environment variable",
		"health",
		"readiness",
		"outbox",
		"restore",
	} {
		if !strings.Contains(runbook, question) {
			t.Errorf("the runbook does not mention %q", question)
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}
