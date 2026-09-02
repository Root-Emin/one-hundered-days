package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

/*
Day 76 - the explainer and verifier.

Docker is not always available (a locked-down laptop, a CI runner without a
daemon), so this program does what CAN be checked without it:

  - the container assets parse and say what they claim to say
  - the binary really does build with the flags the Dockerfile uses
  - the sizes those flags produce are measured, not asserted

Run:

	go run .
*/

func main() {
	if _, err := os.Stat("Dockerfile"); err != nil {
		fmt.Println("Run this from the Day-76 directory:")
		fmt.Println("\n    cd learning/go/Section-16-.../Day-76 && go run .")

		return
	}

	fmt.Println("\n1) Container assets")
	fmt.Println(strings.Repeat("-", 78))

	checkDockerfile()
	checkCompose()
	checkDockerignore()

	fmt.Println("\n2) The build, as the Dockerfile runs it")
	fmt.Println(strings.Repeat("-", 78))

	measureBuilds()

	fmt.Println("\n3) Docker")
	fmt.Println(strings.Repeat("-", 78))

	reportDocker()

	printLayers()
}

//
// ASSET CHECKS
//

func checkDockerfile() {
	content, err := os.ReadFile("Dockerfile")
	if err != nil {
		fmt.Printf("  Dockerfile: %v\n", err)

		return
	}

	text := string(content)

	checks := []struct {
		label string
		ok    bool
		why   string
	}{
		{"multi-stage build", strings.Count(strings.ToUpper(text), "FROM ") >= 2,
			"the toolchain image never ships"},
		{"static binary (CGO_ENABLED=0)", strings.Contains(text, "CGO_ENABLED=0"),
			"required for a distroless or scratch runtime"},
		{"dependencies cached before source", strings.Index(text, "COPY go.mod") < strings.Index(text, "COPY . ."),
			"a source edit must not re-download the module graph"},
		{"symbols stripped", strings.Contains(text, "-s -w"),
			"roughly a third smaller"},
		{"reproducible paths (-trimpath)", strings.Contains(text, "-trimpath"),
			"no build machine paths inside the binary"},
		{"non-root USER", strings.Contains(text, "USER nonroot"),
			"a container escape must not start as root"},
		{"exec-form ENTRYPOINT", strings.Contains(text, `ENTRYPOINT ["`),
			"the binary is PID 1 and receives SIGTERM directly"},
		{"minimal runtime base", strings.Contains(text, "distroless") || strings.Contains(text, "scratch"),
			"no shell, no package manager, nothing to exploit"},
	}

	for _, check := range checks {
		mark := "ok  "

		if !check.ok {
			mark = "MISS"
		}

		fmt.Printf("  [%s] %-32s %s\n", mark, check.label, check.why)
	}
}

func checkCompose() {
	content, err := os.ReadFile("docker-compose.yml")
	if err != nil {
		fmt.Printf("  docker-compose.yml: %v\n", err)

		return
	}

	var compose struct {
		Services map[string]struct {
			Ports           []string          `yaml:"ports"`
			Environment     map[string]string `yaml:"environment"`
			StopGracePeriod string            `yaml:"stop_grace_period"`
			ReadOnly        bool              `yaml:"read_only"`
			SecurityOpt     []string          `yaml:"security_opt"`
			CapDrop         []string          `yaml:"cap_drop"`
		} `yaml:"services"`
	}

	if err := yaml.Unmarshal(content, &compose); err != nil {
		fmt.Printf("  [FAIL] docker-compose.yml does not parse: %v\n", err)

		return
	}

	for name, service := range compose.Services {
		fmt.Printf("\n  service %q: ports=%v grace=%s read_only=%t cap_drop=%v\n",
			name, service.Ports, service.StopGracePeriod, service.ReadOnly, service.CapDrop)

		for key, value := range service.Environment {
			fmt.Printf("    env %s=%s\n", key, value)
		}
	}
}

func checkDockerignore() {
	content, err := os.ReadFile(".dockerignore")
	if err != nil {
		fmt.Printf("\n  [MISS] no .dockerignore: the whole directory is uploaded to the daemon\n")

		return
	}

	required := []string{".git", ".env"}

	missing := make([]string, 0, len(required))

	for _, entry := range required {
		if !strings.Contains(string(content), entry) {
			missing = append(missing, entry)
		}
	}

	if len(missing) > 0 {
		fmt.Printf("\n  [WARN] .dockerignore does not exclude %s\n", strings.Join(missing, ", "))

		return
	}

	fmt.Printf("\n  [ok  ] .dockerignore excludes .git and .env (secrets must never enter an image)\n")
}

//
// BUILD MEASUREMENT
//

func measureBuilds() {
	directory, err := os.MkdirTemp("", "day76-build-*")
	if err != nil {
		fmt.Printf("  temp dir: %v\n", err)

		return
	}

	defer func() {
		if err := os.RemoveAll(directory); err != nil {
			fmt.Printf("  cleanup: %v\n", err)
		}
	}()

	packagePath := "./cmd/api"

	builds := []struct {
		label string
		args  []string
		env   []string
	}{
		{"plain go build", []string{"build"}, nil},
		{"CGO_ENABLED=0", []string{"build"}, []string{"CGO_ENABLED=0"}},
		{"+ -trimpath -ldflags=-s -w", []string{"build", "-trimpath", "-ldflags=-s -w"}, []string{"CGO_ENABLED=0"}},
		{"+ linux/amd64 (as the image)", []string{"build", "-trimpath", "-ldflags=-s -w"},
			[]string{"CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64"}},
	}

	fmt.Printf("  %-32s %-12s %s\n", "BUILD", "SIZE", "TIME")

	var baseline int64

	for i, build := range builds {
		output := filepath.Join(directory, fmt.Sprintf("api-%d", i))

		args := append(append([]string{}, build.args...), "-o", output, packagePath)

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)

		command := exec.CommandContext(ctx, "go", args...)
		command.Env = append(os.Environ(), build.env...)

		start := time.Now()

		if combined, err := command.CombinedOutput(); err != nil {
			cancel()

			fmt.Printf("  %-32s failed: %s\n", build.label, strings.TrimSpace(string(combined)))

			continue
		}

		cancel()

		elapsed := time.Since(start)

		info, err := os.Stat(output)
		if err != nil {
			fmt.Printf("  %-32s stat: %v\n", build.label, err)

			continue
		}

		size := info.Size()

		if baseline == 0 {
			baseline = size
		}

		saving := ""

		if size != baseline {
			saving = fmt.Sprintf("  (%+.0f%%)", (float64(size)/float64(baseline)-1)*100)
		}

		fmt.Printf("  %-32s %-12s %s%s\n",
			build.label,
			fmt.Sprintf("%.1f MB", float64(size)/(1<<20)),
			elapsed.Round(time.Millisecond),
			saving)
	}

	fmt.Println("\n  The final row is the binary the image contains. Its runtime base")
	fmt.Println("  (distroless/static) adds about 2 MB, so the whole image is roughly")
	fmt.Println("  that size plus two - compare with ~800 MB for the golang image.")
}

//
// DOCKER
//

func reportDocker() {
	path, err := exec.LookPath("docker")
	if err != nil {
		fmt.Println("  docker is not installed. The Dockerfile and compose file above are")
		fmt.Println("  still the deliverable; run them wherever a daemon is available.")

		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := exec.CommandContext(ctx, path, "info").Run(); err != nil {
		fmt.Println("  docker is installed but the daemon is not reachable.")
		fmt.Println("  Start Docker Desktop (or dockerd) and run:")
	} else {
		fmt.Println("  docker is ready. From the module root (learning/go):")
	}

	fmt.Println()
	fmt.Println("    docker build -f Section-16-.../Day-76/Dockerfile \\")
	fmt.Println("      --build-arg VERSION=v0.1.0 \\")
	fmt.Println("      --build-arg COMMIT=$(git rev-parse --short HEAD) \\")
	fmt.Println("      -t day76-api:dev .")
	fmt.Println()
	fmt.Println("    docker run --rm -p 8080:8080 day76-api:dev")
	fmt.Println("    docker images day76-api:dev            # expect ~10 MB")
	fmt.Println("    docker stop <container>                # SIGTERM -> graceful shutdown")
}

func printLayers() {
	fmt.Println("\nWhy each choice")
	fmt.Println(strings.Repeat("-", 78))

	rows := []struct {
		choice string
		reason string
	}{
		{"multi-stage", "the 800 MB toolchain stays in the builder; only the binary ships"},
		{"distroless/static", "~2 MB, no shell: an attacker with code execution finds nothing to run"},
		{"USER nonroot", "uid 65532; root in a container is root on the host after an escape"},
		{"ENTRYPOINT exec form", "the binary is PID 1 and gets SIGTERM; the shell form swallows it"},
		{"CGO_ENABLED=0", "static binary, no libc: required for scratch/distroless"},
		{"go.mod copied first", "a source edit does not re-download the dependency graph"},
		{".dockerignore", "keeps .git, .env and local databases out of the build context"},
		{"read_only + cap_drop", "free defence in depth: an immutable filesystem, no capabilities"},
		{"stop_grace_period > shutdown timeout", "the drain finishes before SIGKILL"},
	}

	for _, row := range rows {
		fmt.Printf("  %-38s %s\n", row.choice, row.reason)
	}

	fmt.Println()
}
