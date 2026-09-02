// Package reproducible verifies that the same source produces the same binary.
//
// Why it matters: when an incident points at v1.4.2, you need to rebuild
// v1.4.2 and get the artifact that is actually running. If the rebuild differs,
// you cannot tell whether the difference is the bug, and every claim about
// "the code that shipped" becomes a guess.
//
// What breaks reproducibility in Go, in order of how often it bites:
//
//	absolute paths     the build machine's directory is baked into panics and
//	                   debug info -> fix with -trimpath
//	timestamps         a build date in -ldflags -X changes every build; use
//	                   the COMMIT time, not the build time
//	VCS stamping       the toolchain embeds the commit and a dirty flag by
//	                   default; a dirty tree is unreproducible by definition
//	toolchain version  a different Go version generates different code; pin it
//	CGO                the local C toolchain and headers leak in; CGO_ENABLED=0
//	                   where possible
//	module versions    an unpinned dependency is a different program
package reproducible

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// BuildSpec describes one build.
type BuildSpec struct {
	// Package is the package path to build, e.g. "./cmd/api".
	Package string
	// Output is the binary path to write.
	Output string
	// Version, Commit and CommitTime are stamped into the binary.
	Version    string
	Commit     string
	CommitTime time.Time
	// LDFlagsVar is the package path holding the stamped variables.
	LDFlagsVar string
	// Trimpath removes absolute paths from the binary.
	Trimpath bool
	// Deterministic sets the flags that remove the remaining sources of
	// variation.
	Deterministic bool
	// Env are extra environment variables for the build.
	Env []string
}

// Flags returns the go build arguments this spec produces, which is what makes
// the difference between two builds inspectable rather than mysterious.
func (s BuildSpec) Flags() []string {
	args := []string{"build"}

	if s.Trimpath {
		args = append(args, "-trimpath")
	}

	if s.Deterministic {
		// -buildvcs=false stops the toolchain stamping the commit and the
		// dirty flag, which differ between a CI checkout and a laptop.
		args = append(args, "-buildvcs=false")
	}

	ldflags := "-s -w"

	if s.LDFlagsVar != "" {
		ldflags += fmt.Sprintf(" -X %s.Version=%s", s.LDFlagsVar, s.Version)
		ldflags += fmt.Sprintf(" -X %s.Commit=%s", s.LDFlagsVar, s.Commit)

		// The COMMIT time, not the build time. A build timestamp guarantees a
		// different binary on every build, which is the single most common
		// way reproducibility is lost.
		ldflags += fmt.Sprintf(" -X %s.BuildTime=%s", s.LDFlagsVar, s.CommitTime.UTC().Format(time.RFC3339))
	}

	args = append(args, "-ldflags", ldflags, "-o", s.Output, s.Package)

	return args
}

// Build compiles the binary and returns its SHA-256.
func Build(ctx context.Context, spec BuildSpec) (string, error) {
	command := exec.CommandContext(ctx, "go", spec.Flags()...)

	command.Env = append(os.Environ(), spec.Env...)

	if spec.Deterministic {
		// CGO brings in the local C toolchain, its headers and its paths.
		command.Env = append(command.Env, "CGO_ENABLED=0")
	}

	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go %s: %v: %s",
			strings.Join(spec.Flags(), " "), err, strings.TrimSpace(string(output)))
	}

	return Digest(spec.Output)
}

// Digest returns the SHA-256 of a file, hex encoded.
func Digest(path string) (string, error) {
	file, err := os.Open(path) //nolint:gosec // the path comes from the caller
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}

	defer func() {
		if err := file.Close(); err != nil {
			_ = err
		}
	}()

	hash := sha256.New()

	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// Result is one reproducibility check.
type Result struct {
	Reproducible bool
	FirstDigest  string
	SecondDigest string
	Size         int64
	Flags        []string
	Elapsed      time.Duration
}

// Verify builds the same package twice, into different directories, and
// compares the digests.
//
// Different directories on purpose: building twice into the same path can hide
// a path dependency that a real CI runner - with a different working directory
// from your laptop - would expose.
func Verify(ctx context.Context, spec BuildSpec) (Result, error) {
	start := time.Now()

	firstDir, err := os.MkdirTemp("", "repro-a")
	if err != nil {
		return Result{}, fmt.Errorf("temp dir: %w", err)
	}

	defer func() {
		if err := os.RemoveAll(firstDir); err != nil {
			_ = err
		}
	}()

	secondDir, err := os.MkdirTemp("", "repro-b")
	if err != nil {
		return Result{}, fmt.Errorf("temp dir: %w", err)
	}

	defer func() {
		if err := os.RemoveAll(secondDir); err != nil {
			_ = err
		}
	}()

	firstSpec := spec
	firstSpec.Output = filepath.Join(firstDir, "binary")

	secondSpec := spec
	secondSpec.Output = filepath.Join(secondDir, "binary")

	first, err := Build(ctx, firstSpec)
	if err != nil {
		return Result{}, err
	}

	second, err := Build(ctx, secondSpec)
	if err != nil {
		return Result{}, err
	}

	info, err := os.Stat(firstSpec.Output)
	if err != nil {
		return Result{}, fmt.Errorf("stat: %w", err)
	}

	return Result{
		Reproducible: first == second,
		FirstDigest:  first,
		SecondDigest: second,
		Size:         info.Size(),
		Flags:        firstSpec.Flags(),
		Elapsed:      time.Since(start),
	}, nil
}

// Environment describes what a rebuild has to match to produce the same bytes.
//
// Recording this alongside the artifact is what makes "rebuild v1.4.2" a
// procedure rather than an archaeology project.
type Environment struct {
	GoVersion string
	GOOS      string
	GOARCH    string
	CGO       string
}

// Current returns this machine's build environment.
func Current() Environment {
	return Environment{
		GoVersion: runtime.Version(),
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
		CGO:       os.Getenv("CGO_ENABLED"),
	}
}

// String renders the environment for a build manifest.
func (e Environment) String() string {
	cgo := e.CGO

	if cgo == "" {
		cgo = "(default)"
	}

	return fmt.Sprintf("%s %s/%s CGO_ENABLED=%s", e.GoVersion, e.GOOS, e.GOARCH, cgo)
}
