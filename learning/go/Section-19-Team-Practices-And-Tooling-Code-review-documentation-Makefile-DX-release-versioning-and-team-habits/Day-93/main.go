// Day 93 - Team practices: the Makefile and developer experience.
//
// DX is the time between "git clone" and "my first change is running". Every
// minute of it is paid by every contributor, every time, and it is invisible
// to the person who already has the project set up.
//
// So the tribal knowledge goes into commands:
//
//	Makefile              every step a contributor needs, with make help
//	scripts/setup.sh      clone to running service, one command
//	.envrc                the same configuration in every shell (direnv)
//	.githooks/pre-commit  the fast checks, before the commit exists
//	cmd/doctor            "why does it not work on my machine", answered
//
// And because a Makefile rots exactly like documentation, internal/makefilelint
// checks it: required targets present, every target documented, .PHONY
// complete, tools pinned.
//
// Run: go run ./Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-93
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-93/internal/doctor"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-93/internal/makefilelint"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	if err := demoMakefile(); err != nil {
		return err
	}

	demoDoctor()
	demoSetup()
	demoHooks()

	section("5. Why encode any of this")

	for _, line := range []string{
		"a command in the README is a command someone will type wrong; a target is not",
		"'make check' means the same thing on every machine, including CI's",
		"the pinned linter version is why a lint failure reproduces at all",
		"a newcomer's first hour is spent on the change, not on the setup",
	} {
		fmt.Println("  - " + line)
	}

	fmt.Println()
	fmt.Println("  the test is simple: can someone who has never seen this repository get")
	fmt.Println("  a running service without asking anyone a question? Everything in this")
	fmt.Println("  day exists to make the answer yes.")

	fmt.Println("\n  the setup guide is in docs/CONTRIBUTING.md")

	return nil
}

func demoMakefile() error {
	section("1. The Makefile is the contributor interface")

	makefile, err := makefilelint.Load(filepath.Join(dayDir(), "Makefile"))
	if err != nil {
		return err
	}

	fmt.Printf("  %d targets, default goal %q\n\n", len(makefile.Targets), makefile.DefaultGoal)
	fmt.Print(makefile.Help())

	issues := makefile.Check(makefilelint.RequiredTargets)

	fmt.Printf("\n  lint: %d issue(s)\n", len(issues))

	for _, issue := range issues {
		fmt.Println("    " + issue.String())
	}

	fmt.Println("\n  the same lint against a Makefile that grew organically:")

	sloppy := makefilelint.Parse(`
BINARY = app

test:
	go test ./...

build:
	go build -o app ./cmd/app

deploy:
	./deploy.sh production
`)

	for _, issue := range sloppy.Check(makefilelint.RequiredTargets) {
		fmt.Println("    " + issue.String())
	}

	fmt.Println()
	fmt.Println("  the ones that bite in practice: no help (so the reader greps the")
	fmt.Println("  source), no .PHONY (so 'make build' silently does nothing the day a")
	fmt.Println("  directory called build appears), and @latest tooling (so a lint")
	fmt.Println("  failure cannot be reproduced).")

	return nil
}

func demoDoctor() {
	section("2. make doctor: why it does not work on your machine")

	report := doctor.Run(context.Background(), doctor.DefaultOptions())

	for _, result := range report.Results {
		fmt.Println(result)
	}

	ok, warn, fail := report.Counts()

	fmt.Printf("\n  %d ok, %d warning(s), %d failure(s)\n", ok, warn, fail)
	fmt.Println()
	fmt.Println("  every line carries a FIX, not a verdict. \"golangci-lint: not found\"")
	fmt.Println("  has done half the job; \"run make tools\" is the other half.")
	fmt.Println()
	fmt.Println("  the failure/warning split matters too: a missing linter does not stop")
	fmt.Println("  anyone building or testing, so it must not stop the setup script.")
}

func demoSetup() {
	section("3. One command from clone to running")

	fmt.Println("  ./scripts/setup.sh")
	fmt.Println("    1/5 doctor        the toolchain, before anything else fails obscurely")
	fmt.Println("    2/5 go mod download")
	fmt.Println("    3/5 make tools    the pinned linter")
	fmt.Println("    4/5 make migrate  a database that exists")
	fmt.Println("    5/5 go test       proof that it works before you change anything")
	fmt.Println()
	fmt.Println("  then:")
	fmt.Println("    make run          the service, on :8093")
	fmt.Println("    make check        fmt, vet, lint, race - what CI runs")
	fmt.Println()
	fmt.Println("  verified end to end while writing this day: make migrate applied two")
	fmt.Println("  migrations, make build produced dist/notes-api, and the binary served")
	fmt.Println("  POST /notes and GET /healthz.")

	section("3b. .envrc and direnv (optional)")

	fmt.Println("  direnv loads .envrc when you cd in and unloads it when you leave, so")
	fmt.Println("  your shell, your editor's terminal and make run all see the same")
	fmt.Println("  configuration - which is where \"works on my machine\" usually comes from.")
	fmt.Println()
	fmt.Println("  it stays OPTIONAL: every variable has a default in the code, so the")
	fmt.Println("  project works without it. A tool that is required to build is a tool")
	fmt.Println("  that belongs in the setup script, not in a suggestion.")
	fmt.Println()
	fmt.Println("  secrets go in .envrc.local, which is gitignored and sourced from .envrc.")
}

func demoHooks() {
	section("4. The pre-commit hook")

	fmt.Println("  make hooks    ->  git config core.hooksPath <day>/.githooks")
	fmt.Println()
	fmt.Println("  core.hooksPath is what makes hooks shareable: .git/hooks is not")
	fmt.Println("  version-controlled, so a hook that lives there exists only on the")
	fmt.Println("  machine where someone remembered to copy it.")
	fmt.Println()
	fmt.Println("  what it runs, and what it deliberately does not:")

	for _, line := range []string{
		"gofmt on the STAGED files only - not the whole tree, so unrelated work",
		"  in progress does not block your commit",
		"go vet and go build - about a second",
		"NOT the race detector, NOT the integration tests: a hook that takes",
		"  thirty seconds gets bypassed with --no-verify within a week, and then",
		"  it protects nothing",
	} {
		fmt.Println("    " + line)
	}

	hookPath := filepath.Join(dayDir(), ".githooks", "pre-commit")

	if info, err := os.Stat(hookPath); err == nil {
		mode := info.Mode()

		fmt.Printf("\n  %s is %s", hookPath, mode)

		if mode&0o111 == 0 {
			fmt.Print("  <- NOT executable; git will skip it silently")
		} else {
			fmt.Print("  (executable, as git requires)")
		}

		fmt.Println()
	}
}

func dayDir() string {
	path := filepath.Join(
		"Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits",
		"Day-93")

	if _, err := os.Stat(path); err != nil {
		return "."
	}

	return path
}

func section(title string) {
	fmt.Printf("\n%s\n%s\n", title, strings.Repeat("=", len(title)))
}
