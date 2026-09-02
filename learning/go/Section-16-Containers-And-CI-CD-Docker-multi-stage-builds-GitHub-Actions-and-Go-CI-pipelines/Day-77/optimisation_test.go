package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

/*
These tests build the binary for real and inspect the Dockerfiles. They are
slower than a unit test and worth it: the failures they catch (a version that
is never injected, a cache layer in the wrong order, a root user) are all
invisible until something goes wrong in production.
*/

func buildBinary(t *testing.T, ldflags string, env ...string) string {
	t.Helper()

	binary := filepath.Join(t.TempDir(), "api")

	args := []string{"build", "-trimpath"}

	if ldflags != "" {
		args = append(args, "-ldflags="+ldflags)
	}

	args = append(args, "-o", binary, "./cmd/api")

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	command := exec.CommandContext(ctx, "go", args...)
	command.Env = append(os.Environ(), env...)

	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}

	return binary
}

// TestVersionInjection is the assertion behind every "which build is this?"
// question in production.
func TestVersionInjection(t *testing.T) {
	t.Parallel()

	binary := buildBinary(t,
		"-s -w -X main.version=v1.2.3 -X main.commit=abc1234 -X main.buildTime=2026-01-01T00:00:00Z",
		"CGO_ENABLED=0")

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	output, err := exec.CommandContext(ctx, binary, "-version").Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var info struct {
		Version   string `json:"version"`
		Commit    string `json:"commit"`
		BuildTime string `json:"build_time"`
	}

	if err := json.Unmarshal(output, &info); err != nil {
		t.Fatalf("decode %s: %v", output, err)
	}

	if info.Version != "v1.2.3" || info.Commit != "abc1234" {
		t.Fatalf("build info = %+v, want the injected values", info)
	}
}

func TestDefaultVersionIsDev(t *testing.T) {
	t.Parallel()

	binary := buildBinary(t, "", "CGO_ENABLED=0")

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	output, err := exec.CommandContext(ctx, binary, "-version").Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var info struct {
		Version string `json:"version"`
	}

	if err := json.Unmarshal(output, &info); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// A build with no flags must be obviously unversioned, not silently
	// claim to be a release.
	if info.Version != "dev" {
		t.Fatalf("version = %q, want dev", info.Version)
	}
}

// TestStrippingShrinksTheBinary measures the claim the Dockerfile makes.
func TestStrippingShrinksTheBinary(t *testing.T) {
	t.Parallel()

	full := buildBinary(t, "", "CGO_ENABLED=0")
	stripped := buildBinary(t, "-s -w", "CGO_ENABLED=0")

	fullInfo, err := os.Stat(full)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	strippedInfo, err := os.Stat(stripped)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if strippedInfo.Size() >= fullInfo.Size() {
		t.Fatalf("stripped binary (%d) is not smaller than the full one (%d)",
			strippedInfo.Size(), fullInfo.Size())
	}

	saving := 1 - float64(strippedInfo.Size())/float64(fullInfo.Size())

	// The documented figure is "about a third". If a toolchain change makes
	// that wrong, the documentation should be updated - this test is how
	// anyone finds out.
	if saving < 0.15 {
		t.Fatalf("stripping saved only %.0f%%, the docs claim ~30%%", saving*100)
	}

	t.Logf("full %.2f MB, stripped %.2f MB, saving %.0f%%",
		float64(fullInfo.Size())/(1<<20), float64(strippedInfo.Size())/(1<<20), saving*100)
}

//
// DOCKERFILE PROPERTIES
//

func readDockerfile(t *testing.T, name string) string {
	t.Helper()

	content, err := os.ReadFile(filepath.Join("dockerfiles", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
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

func TestOptimisedDockerfileUsesCacheMounts(t *testing.T) {
	t.Parallel()

	content := readDockerfile(t, "Dockerfile")

	for _, expected := range []string{
		"--mount=type=cache,target=/go/pkg/mod",
		"--mount=type=cache,target=/root/.cache/go-build",
	} {
		if !strings.Contains(content, expected) {
			t.Errorf("missing BuildKit cache mount: %s", expected)
		}
	}

	// Dependencies before source, or the cache mount cannot help.
	if strings.Index(content, "COPY go.mod") > strings.Index(content, "COPY . .") {
		t.Error("source is copied before the module manifests")
	}
}

func TestNaiveDockerfileIsTheCounterExample(t *testing.T) {
	t.Parallel()

	content := readDockerfile(t, "Dockerfile.naive")

	// It is supposed to be bad: this test documents HOW, so nobody
	// accidentally "fixes" the teaching example and loses the comparison.
	if strings.Count(content, "FROM ") != 1 {
		t.Error("the naive example should be single-stage")
	}

	if strings.Contains(content, "USER ") {
		t.Error("the naive example should run as root, to show what that looks like")
	}

	if strings.Contains(content, `CMD ["`) {
		t.Error("the naive example should use shell-form CMD")
	}
}

func TestScratchImageAddsWhatScratchLacks(t *testing.T) {
	t.Parallel()

	content := readDockerfile(t, "Dockerfile.scratch")

	// Without these, TLS calls fail with x509 errors and time zones do not
	// resolve - the two things everyone forgets with scratch.
	for _, expected := range []string{"ca-certificates.crt", "zoneinfo", "/etc/passwd"} {
		if !strings.Contains(content, expected) {
			t.Errorf("scratch image does not provide %s", expected)
		}
	}

	if !strings.Contains(content, "USER 65532") {
		t.Error("scratch image does not drop to a non-root uid")
	}
}

func TestAlpineImageHasAHealthcheck(t *testing.T) {
	t.Parallel()

	content := readDockerfile(t, "Dockerfile.alpine")

	if !strings.Contains(content, "HEALTHCHECK") {
		t.Error("alpine has a shell, so it should carry a HEALTHCHECK")
	}

	if !strings.Contains(content, "adduser") || !strings.Contains(content, "USER app") {
		t.Error("alpine image should create and use a non-root user")
	}
}

func TestEveryDockerfileBuildsAStaticBinary(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"Dockerfile", "Dockerfile.scratch", "Dockerfile.alpine"} {
		content := readDockerfile(t, name)

		if !strings.Contains(content, "CGO_ENABLED=0") {
			t.Errorf("%s does not disable CGO", name)
		}

		if !strings.Contains(content, "-trimpath") {
			t.Errorf("%s does not use -trimpath", name)
		}
	}
}
