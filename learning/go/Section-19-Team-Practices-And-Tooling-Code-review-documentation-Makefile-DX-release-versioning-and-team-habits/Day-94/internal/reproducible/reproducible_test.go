package reproducible_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-94/internal/reproducible"
)

const (
	demoPackage = "../../cmd/demoapp"
	buildInfo   = "example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-94/internal/buildinfo"
)

func spec() reproducible.BuildSpec {
	return reproducible.BuildSpec{
		Package:       demoPackage,
		Version:       "v1.3.0",
		Commit:        "e5f6a7b8c9d0",
		CommitTime:    time.Date(2026, 9, 1, 18, 30, 0, 0, time.UTC),
		LDFlagsVar:    buildInfo,
		Trimpath:      true,
		Deterministic: true,
	}
}

func TestFlagsIncludeTheDeterminismSwitches(t *testing.T) {
	flags := strings.Join(spec().Flags(), " ")

	for _, want := range []string{"-trimpath", "-buildvcs=false", "-ldflags", "-X " + buildInfo + ".Version=v1.3.0"} {
		if !strings.Contains(flags, want) {
			t.Errorf("flags %q are missing %q", flags, want)
		}
	}

	// The COMMIT time, not now.
	if !strings.Contains(flags, "2026-09-01T18:30:00Z") {
		t.Errorf("the commit time was not stamped: %q", flags)
	}
}

// The property the whole package exists for: same source, same bytes.
func TestSameSourceProducesTheSameBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles twice")
	}

	result, err := reproducible.Verify(t.Context(), spec())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if !result.Reproducible {
		t.Errorf("two builds differ:\n  %s\n  %s", result.FirstDigest, result.SecondDigest)
	}

	if result.Size == 0 {
		t.Error("the binary is empty")
	}
}

// A build timestamp makes reproducibility impossible by construction, which is
// why the spec stamps the commit time. This proves the failure mode is real.
func TestABuildTimestampBreaksReproducibility(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles twice")
	}

	dir := t.TempDir()

	first := spec()
	first.CommitTime = time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	first.Output = filepath.Join(dir, "first")

	second := spec()
	second.CommitTime = time.Date(2026, 9, 1, 10, 0, 1, 0, time.UTC)
	second.Output = filepath.Join(dir, "second")

	firstDigest, err := reproducible.Build(t.Context(), first)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	secondDigest, err := reproducible.Build(t.Context(), second)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if firstDigest == secondDigest {
		t.Error("two different stamped timestamps produced the same binary - the stamp is not reaching the build")
	}
}

func TestDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")

	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	digest, err := reproducible.Digest(path)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}

	// SHA-256 of "hello".
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

	if digest != want {
		t.Errorf("digest = %s, want %s", digest, want)
	}

	if _, err := reproducible.Digest(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("expected an error for a missing file")
	}
}

func TestBuildFailureCarriesTheCompilerOutput(t *testing.T) {
	broken := spec()
	broken.Package = "./this/package/does/not/exist"
	broken.Output = filepath.Join(t.TempDir(), "binary")

	_, err := reproducible.Build(t.Context(), broken)
	if err == nil {
		t.Fatal("expected a build failure")
	}

	// "exit status 1" on its own would be useless: the go tool writes the
	// reason to stderr, which Output() only exposes through the ExitError.
	message := err.Error()

	if !strings.Contains(message, "does not exist") && !strings.Contains(message, "not found") {
		t.Errorf("error = %v, want the go tool's own message", err)
	}

	if !strings.Contains(message, broken.Package) {
		t.Errorf("error = %v, want it to name the package that failed", err)
	}
}

func TestCurrentEnvironment(t *testing.T) {
	environment := reproducible.Current()

	if environment.GoVersion == "" || environment.GOOS == "" || environment.GOARCH == "" {
		t.Errorf("environment = %+v", environment)
	}

	if !strings.Contains(environment.String(), environment.GoVersion) {
		t.Errorf("String() = %q", environment.String())
	}
}
