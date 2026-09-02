package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

/*
Tests for the container assets.

A Dockerfile is code: it can regress like any other file, and the failures it
produces (running as root, a shell as PID 1, a 900 MB image) are found in
production rather than at compile time. These tests are cheap insurance.
*/

// dockerfile returns the Dockerfile with comment lines removed.
//
// Stripping comments matters: this file explains the shell-form ENTRYPOINT it
// deliberately does not use, and a naive substring check would flag the
// explanation as the mistake.
func dockerfile(t *testing.T) string {
	t.Helper()

	content, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}

	var instructions strings.Builder

	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}

		instructions.WriteString(line)
		instructions.WriteString("\n")
	}

	return instructions.String()
}

func TestDockerfileIsMultiStage(t *testing.T) {
	t.Parallel()

	content := dockerfile(t)

	if strings.Count(content, "FROM ") < 2 {
		t.Fatal("not a multi-stage build: the toolchain image would ship to production")
	}

	builderIndex := strings.Index(content, "FROM golang")
	runtimeIndex := strings.LastIndex(content, "FROM ")

	if builderIndex < 0 || runtimeIndex <= builderIndex {
		t.Fatal("expected a golang builder stage followed by a runtime stage")
	}

	// The runtime stage must not be the toolchain image.
	runtimeStage := content[runtimeIndex:]

	if strings.Contains(runtimeStage, "golang:") {
		t.Fatal("the runtime stage is based on the golang image")
	}
}

func TestDockerfileRunsAsNonRoot(t *testing.T) {
	t.Parallel()

	content := dockerfile(t)

	if !strings.Contains(content, "USER nonroot") && !strings.Contains(content, "USER 65532") {
		t.Fatal("no non-root USER: a container escape would start as root")
	}
}

// TestEntrypointIsExecForm guards the signal path: in shell form the shell is
// PID 1, it does not forward SIGTERM, and every deploy becomes a SIGKILL.
func TestEntrypointIsExecForm(t *testing.T) {
	t.Parallel()

	content := dockerfile(t)

	if !strings.Contains(content, `ENTRYPOINT ["`) {
		t.Fatal(`ENTRYPOINT must be exec form: ENTRYPOINT ["/api"]`)
	}

	if strings.Contains(content, "ENTRYPOINT /") || strings.Contains(content, "CMD /api") {
		t.Fatal("shell-form ENTRYPOINT/CMD: signals would not reach the binary")
	}
}

func TestDockerfileBuildsAStaticBinary(t *testing.T) {
	t.Parallel()

	content := dockerfile(t)

	if !strings.Contains(content, "CGO_ENABLED=0") {
		t.Fatal("CGO_ENABLED=0 is required for a distroless/scratch runtime")
	}

	for _, flag := range []string{"-trimpath", "-s -w"} {
		if !strings.Contains(content, flag) {
			t.Errorf("missing build flag %q", flag)
		}
	}
}

// TestLayerOrderIsCacheFriendly: dependencies before source, or every source
// edit re-downloads the module graph.
func TestLayerOrderIsCacheFriendly(t *testing.T) {
	t.Parallel()

	content := dockerfile(t)

	modIndex := strings.Index(content, "COPY go.mod")
	sourceIndex := strings.Index(content, "COPY . .")

	if modIndex < 0 || sourceIndex < 0 {
		t.Fatal("expected go.mod and source copies")
	}

	if modIndex > sourceIndex {
		t.Fatal("source is copied before go.mod: the dependency layer can never be cached")
	}
}

func TestDockerignoreExcludesSecretsAndJunk(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(".dockerignore")
	if err != nil {
		t.Fatalf("read .dockerignore: %v", err)
	}

	for _, entry := range []string{".git", ".env", "*.db"} {
		if !strings.Contains(string(content), entry) {
			t.Errorf(".dockerignore does not exclude %q", entry)
		}
	}
}

func TestComposeIsValidAndSafe(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile("docker-compose.yml")
	if err != nil {
		t.Fatalf("read docker-compose.yml: %v", err)
	}

	var compose struct {
		Services map[string]struct {
			Environment     map[string]string `yaml:"environment"`
			StopGracePeriod string            `yaml:"stop_grace_period"`
			ReadOnly        bool              `yaml:"read_only"`
			CapDrop         []string          `yaml:"cap_drop"`
			SecurityOpt     []string          `yaml:"security_opt"`
		} `yaml:"services"`
	}

	if err := yaml.Unmarshal(content, &compose); err != nil {
		t.Fatalf("docker-compose.yml is not valid YAML: %v", err)
	}

	service, found := compose.Services["api"]
	if !found {
		t.Fatal("no api service in the compose file")
	}

	if !service.ReadOnly {
		t.Error("read_only is not set: the container filesystem should be immutable")
	}

	if len(service.CapDrop) == 0 {
		t.Error("cap_drop is not set: a web service needs no Linux capabilities")
	}

	// The grace period must outlast the application's own drain, or SIGKILL
	// interrupts it - which is the bug this assertion exists to catch.
	grace, err := time.ParseDuration(service.StopGracePeriod)
	if err != nil {
		t.Fatalf("stop_grace_period %q: %v", service.StopGracePeriod, err)
	}

	shutdown, err := time.ParseDuration(service.Environment["SHUTDOWN_TIMEOUT"])
	if err != nil {
		t.Fatalf("SHUTDOWN_TIMEOUT %q: %v", service.Environment["SHUTDOWN_TIMEOUT"], err)
	}

	if grace <= shutdown {
		t.Fatalf("stop_grace_period (%s) must be longer than SHUTDOWN_TIMEOUT (%s)", grace, shutdown)
	}
}
