package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

/*
Day 77 - Containers & CI/CD: Multi-Stage Builds and Optimization

Tasks covered:

 1. Module caching: go.mod copied before source, plus BuildKit cache mounts
 2. CGO disabled, producing a static binary that needs no libc at runtime
 3. Debug symbols stripped with -ldflags, measured rather than assumed
 4. Image scanning: what to run, and what the findings mean

Files:

	cmd/api/main.go              the program being built
	dockerfiles/Dockerfile.naive one stage, no caching, root, shell CMD
	dockerfiles/Dockerfile       the optimised build
	dockerfiles/Dockerfile.scratch  the smallest possible image
	dockerfiles/Dockerfile.alpine   the pragmatic middle, with a shell

Run:

	go run .            # measure the flags, check the Dockerfiles, print the plan
	go run . scan       # run an image scanner if one is installed

Build (from learning/go):

	DOCKER_BUILDKIT=1 docker build -f Section-16-.../Day-77/dockerfiles/Dockerfile \
	  --build-arg VERSION=v1.0.0 -t day77:optimised .
*/

func main() {
	if _, err := os.Stat("dockerfiles"); err != nil {
		fmt.Println("Run this from the Day-77 directory.")

		return
	}

	if len(os.Args) > 1 && os.Args[1] == "scan" {
		runScan()

		return
	}

	fmt.Println("\n1) What the build flags actually do")
	fmt.Println(strings.Repeat("-", 80))

	measureFlags()

	fmt.Println("\n2) Version injection through -ldflags")
	fmt.Println(strings.Repeat("-", 80))

	verifyVersionInjection()

	fmt.Println("\n3) The four Dockerfiles")
	fmt.Println(strings.Repeat("-", 80))

	compareDockerfiles()

	fmt.Println("\n4) Layer caching")
	fmt.Println(strings.Repeat("-", 80))

	explainCaching()

	fmt.Println("\n5) Scanning")
	fmt.Println(strings.Repeat("-", 80))

	explainScanning()
}

//
// FLAG MEASUREMENT
//

func measureFlags() {
	directory, err := os.MkdirTemp("", "day77-*")
	if err != nil {
		fmt.Printf("  temp dir: %v\n", err)

		return
	}

	defer func() {
		if err := os.RemoveAll(directory); err != nil {
			fmt.Printf("  cleanup: %v\n", err)
		}
	}()

	builds := []struct {
		label string
		args  []string
		env   []string
		note  string
	}{
		{"default", []string{"build"}, nil, "what go build gives you"},
		{"CGO_ENABLED=0", []string{"build"}, []string{"CGO_ENABLED=0"},
			"static: no libc needed at runtime"},
		{"-ldflags=-s -w", []string{"build", "-ldflags=-s -w"}, []string{"CGO_ENABLED=0"},
			"symbol table and DWARF removed"},
		{"-trimpath -ldflags=-s -w", []string{"build", "-trimpath", "-ldflags=-s -w"},
			[]string{"CGO_ENABLED=0"}, "no local paths; reproducible"},
		{"linux/amd64 (image target)", []string{"build", "-trimpath", "-ldflags=-s -w"},
			[]string{"CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64"}, "the binary that ships"},
	}

	fmt.Printf("  %-30s %-11s %-9s %s\n", "FLAGS", "SIZE", "VS FIRST", "WHY")

	var baseline int64

	for i, build := range builds {
		output := filepath.Join(directory, fmt.Sprintf("api-%d", i))

		args := append(append([]string{}, build.args...), "-o", output, "./cmd/api")

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)

		command := exec.CommandContext(ctx, "go", args...)
		command.Env = append(os.Environ(), build.env...)

		combined, err := command.CombinedOutput()

		cancel()

		if err != nil {
			fmt.Printf("  %-30s failed: %s\n", build.label, strings.TrimSpace(string(combined)))

			continue
		}

		info, err := os.Stat(output)
		if err != nil {
			continue
		}

		if baseline == 0 {
			baseline = info.Size()
		}

		change := ""

		if info.Size() != baseline {
			change = fmt.Sprintf("%+.0f%%", (float64(info.Size())/float64(baseline)-1)*100)
		}

		fmt.Printf("  %-30s %-11s %-9s %s\n",
			build.label,
			fmt.Sprintf("%.2f MB", float64(info.Size())/(1<<20)),
			change,
			build.note)
	}

	fmt.Println("\n  Stripping symbols is the big one. The cost: a panic stack trace still")
	fmt.Println("  has function names (Go keeps those), but a debugger and a core dump")
	fmt.Println("  have much less to work with. Teams that debug from core dumps keep")
	fmt.Println("  the symbols and accept the size.")
}

// verifyVersionInjection proves that -X actually sets the variable, rather
// than asserting it in a comment.
func verifyVersionInjection() {
	directory, err := os.MkdirTemp("", "day77-version-*")
	if err != nil {
		fmt.Printf("  temp dir: %v\n", err)

		return
	}

	defer func() {
		if err := os.RemoveAll(directory); err != nil {
			fmt.Printf("  cleanup: %v\n", err)
		}
	}()

	binary := filepath.Join(directory, "api")

	ldflags := fmt.Sprintf("-s -w -X main.version=v9.9.9 -X main.commit=deadbeef -X main.buildTime=%s",
		time.Now().UTC().Format(time.RFC3339))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags="+ldflags, "-o", binary, "./cmd/api")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")

	if output, err := build.CombinedOutput(); err != nil {
		fmt.Printf("  build failed: %s\n", output)

		return
	}

	output, err := exec.CommandContext(ctx, binary, "-version").Output()
	if err != nil {
		fmt.Printf("  run failed: %v\n", err)

		return
	}

	var info map[string]any

	if err := json.Unmarshal(output, &info); err != nil {
		fmt.Printf("  decode failed: %v\n", err)

		return
	}

	fmt.Printf("  built with: -ldflags \"%s\"\n\n", ldflags)
	fmt.Printf("  the binary reports: version=%v commit=%v go=%v\n",
		info["version"], info["commit"], info["go_version"])

	if recorded, ok := info["ldflags"].(string); ok && recorded != "" {
		fmt.Printf("  and it knows its own link flags: %s\n", recorded)
	}

	fmt.Println("\n  This is how \"which build is running?\" gets an answer in production.")
	fmt.Println("  Go also records the VCS revision automatically when building inside a")
	fmt.Println("  git checkout: debug.ReadBuildInfo() carries it.")
}

//
// DOCKERFILE COMPARISON
//

func compareDockerfiles() {
	files := []struct {
		name    string
		base    string
		size    string
		summary string
	}{
		{"Dockerfile.naive", "golang:1.26", "~900 MB", "toolchain, source and module cache all shipped; runs as root"},
		{"Dockerfile", "distroless/static", "~8 MB", "the one to copy: cached deps, static binary, non-root, no shell"},
		{"Dockerfile.scratch", "scratch", "~6 MB", "smallest; you add CA certs, tzdata and /etc/passwd by hand"},
		{"Dockerfile.alpine", "alpine:3.21", "~14 MB", "has a shell: easier to debug, larger surface to attack"},
	}

	fmt.Printf("  %-22s %-20s %-9s %s\n", "FILE", "RUNTIME BASE", "IMAGE", "TRADE-OFF")

	for _, file := range files {
		if _, err := os.Stat(filepath.Join("dockerfiles", file.name)); err != nil {
			fmt.Printf("  %-22s MISSING\n", file.name)

			continue
		}

		fmt.Printf("  %-22s %-20s %-9s %s\n", file.name, file.base, file.size, file.summary)
	}

	fmt.Println("\n  Sizes are for this program on linux/amd64; the shape of the")
	fmt.Println("  comparison is what matters, not the exact numbers.")
}

func explainCaching() {
	fmt.Println("  Docker caches per instruction. An instruction is re-run when its own")
	fmt.Println("  text changes, or when anything it copied changed - and everything")
	fmt.Println("  after it is re-run too.")
	fmt.Println()
	fmt.Println("  WRONG                              RIGHT")
	fmt.Println("    COPY . .                           COPY go.mod go.sum ./")
	fmt.Println("    RUN go mod download                RUN go mod download")
	fmt.Println("    RUN go build                       COPY . .")
	fmt.Println("                                       RUN go build")
	fmt.Println()
	fmt.Println("  On the left, editing one line of Go re-downloads every dependency.")
	fmt.Println("  On the right, that only happens when go.mod or go.sum change.")
	fmt.Println()
	fmt.Println("  BuildKit cache mounts go further: the module and build caches persist")
	fmt.Println("  between builds without ending up in a layer.")
	fmt.Println()
	fmt.Println("      RUN --mount=type=cache,target=/go/pkg/mod \\")
	fmt.Println("          --mount=type=cache,target=/root/.cache/go-build \\")
	fmt.Println("          go build ...")
	fmt.Println()
	fmt.Println("  In CI, where the daemon is fresh every run, the equivalent is")
	fmt.Println("  actions/cache or the registry cache:")
	fmt.Println("      docker buildx build --cache-from type=gha --cache-to type=gha,mode=max")
}

//
// SCANNING
//

func explainScanning() {
	fmt.Println("  An image is a filesystem: it has a package list, and that list has")
	fmt.Println("  known vulnerabilities. Scanning is how you find out before an")
	fmt.Println("  attacker does.")
	fmt.Println()

	scanners := []struct {
		tool    string
		command string
		note    string
	}{
		{"trivy", "trivy image day77:optimised", "the usual default: OS packages plus Go modules"},
		{"grype", "grype day77:optimised", "similar coverage, different database"},
		{"docker scout", "docker scout cves day77:optimised", "built into recent Docker Desktop"},
		{"govulncheck", "govulncheck ./...", "Go-specific, and reports only REACHABLE vulnerabilities"},
	}

	fmt.Printf("  %-16s %-42s %s\n", "TOOL", "COMMAND", "NOTE")

	for _, scanner := range scanners {
		fmt.Printf("  %-16s %-42s %s\n", scanner.tool, scanner.command, scanner.note)
	}

	fmt.Println("\n  What the base image choice does to the report:")
	fmt.Println("    golang:1.26        hundreds of OS packages -> a long CVE list to triage")
	fmt.Println("    alpine:3.21        a handful of packages")
	fmt.Println("    distroless/static  almost nothing: no shell, no package manager")
	fmt.Println("    scratch            nothing at all; only your own binary is scanned")
	fmt.Println()
	fmt.Println("  A smaller base is not just faster to pull - it is a shorter list of")
	fmt.Println("  things somebody has to patch on a Friday afternoon.")
	fmt.Println()
	fmt.Println("  In CI (Day 79 wires this in):")
	fmt.Println("    trivy image --exit-code 1 --severity HIGH,CRITICAL --ignore-unfixed $IMAGE")
	fmt.Println()
	fmt.Println("  --ignore-unfixed matters: failing a build for a CVE with no available")
	fmt.Println("  fix teaches the team to ignore the scanner.")
	fmt.Println()
	fmt.Println("  Run \"go run . scan\" to use whichever scanner is installed here.")
}

func runScan() {
	fmt.Println("\nImage scanning")
	fmt.Println(strings.Repeat("-", 80))

	for _, tool := range []string{"trivy", "grype", "docker"} {
		path, err := exec.LookPath(tool)
		if err != nil {
			fmt.Printf("  %-8s not installed\n", tool)

			continue
		}

		fmt.Printf("  %-8s found at %s\n", tool, path)
	}

	fmt.Println("\n  govulncheck works without Docker and scans THIS module's dependencies:")
	fmt.Println()

	path, err := exec.LookPath("govulncheck")
	if err != nil {
		fmt.Println("    go install golang.org/x/vuln/cmd/govulncheck@latest")
		fmt.Println("    govulncheck ./...")

		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	command := exec.CommandContext(ctx, path, "./...")
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr

	if err := command.Run(); err != nil {
		fmt.Printf("\n  govulncheck reported findings (exit: %v)\n", err)
	}
}
