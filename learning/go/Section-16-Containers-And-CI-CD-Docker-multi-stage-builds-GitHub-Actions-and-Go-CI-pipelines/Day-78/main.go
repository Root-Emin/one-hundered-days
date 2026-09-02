package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

func main() {
	directory := ".github/workflows"

	if _, err := os.Stat(directory); err != nil {
		fmt.Println("Run this from the Day-78 directory.")

		return
	}

	workflows, err := LoadWorkflows(directory)
	if err != nil {
		fmt.Printf("  %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n1) The workflows in this repository")
	fmt.Println(strings.Repeat("-", 80))

	names := make([]string, 0, len(workflows))

	for name := range workflows {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		workflow := workflows[name]

		fmt.Printf("\n  %s  (name: %s)\n", name, workflow.Name)
		fmt.Printf("    triggers   : %s\n", strings.Join(workflow.Triggers(), ", "))
		fmt.Printf("    permissions: %v\n", workflow.Permissions)
		fmt.Printf("    jobs       : %d\n", len(workflow.Jobs))

		for jobName, job := range workflow.Jobs {
			fmt.Printf("      %-24s runs-on=%v timeout=%dm steps=%d\n",
				jobName, job.RunsOn, job.TimeoutMinutes, len(job.Steps))
		}

		if actions := workflow.UsedActions(); len(actions) > 0 {
			fmt.Printf("    actions    : %s\n", strings.Join(actions, ", "))
		}

		findings := workflow.Findings()

		if len(findings) == 0 {
			fmt.Println("    findings   : none")

			continue
		}

		for _, finding := range findings {
			fmt.Printf("    FINDING    : %s\n", finding)
		}
	}

	printAnatomy()
	printCaching()
	printPitfalls()
}

func printAnatomy() {
	fmt.Println("\n2) Anatomy of a workflow")
	fmt.Println(strings.Repeat("-", 80))

	fmt.Println(`
  name: ci                         what shows in the Actions tab

  on:                              WHEN it runs
    push: {branches: [main]}         every push to main
    pull_request:                    every PR update - this is the gate
    schedule: [{cron: '0 3 * * *'}]  nightly, for slow suites
    workflow_dispatch:               a manual button

  permissions:                     the GITHUB_TOKEN's scope. Least privilege:
    contents: read                 start at read and add what a job proves it needs

  concurrency:                     one run per branch; cancel superseded ones
    group: ci-${{ github.ref }}
    cancel-in-progress: true

  jobs:                            jobs run in PARALLEL unless 'needs' links them
    test:
      runs-on: ubuntu-latest       the runner image
      timeout-minutes: 10          bound it, always
      steps:                       steps run in SEQUENCE, sharing a filesystem
        - uses: actions/checkout@v4
        - run: go test ./...`)
}

func printCaching() {
	fmt.Println("\n3) Caching")
	fmt.Println(strings.Repeat("-", 80))

	fmt.Println(`  actions/setup-go with cache: true handles both Go caches:

      ~/go/pkg/mod        downloaded modules, keyed on go.sum
      ~/.cache/go-build   compiled packages, which is the bigger win on a
                          repository with many packages

  Rules that make a cache actually help:

    * key on the LOCKFILE (go.sum), not on a branch name. A key that changes
      on every commit is a cache that is never hit.
    * set cache-dependency-path when go.sum is not at the repository root -
      as in this repository, where the module lives in learning/go.
    * a cache miss must not fail the build. It is an optimisation.
    * caches are scoped per branch, with fallback to the default branch, so a
      new PR inherits main's cache rather than starting cold.`)
}

func printPitfalls() {
	fmt.Println("\n4) Pitfalls worth knowing before they bite")
	fmt.Println(strings.Repeat("-", 80))

	pitfalls := []struct {
		problem string
		fix     string
	}{
		{"`on` written unquoted", "YAML 1.1 reads it as the boolean true; some tools then see no triggers"},
		{"${{ }} inside run:", "interpolated before the shell runs - a PR title becomes shell code. Use env:"},
		{"No permissions block", "the token defaults to write; a compromised action can push to the repo"},
		{"Actions pinned to a tag", "tags move. Pin to a commit SHA for anything that sees secrets"},
		{"No timeout-minutes", "a deadlocked test can run for six hours"},
		{"pull_request_target", "runs with secrets against PR code - almost always the wrong trigger"},
		{"Secrets in a fork PR", "they are not available, by design. Do not work around it"},
		{"cache-dependency-path missing", "silently caches nothing when go.sum is not at the root"},
		{"No `go mod tidy` check", "an untidy go.mod passes locally and breaks a fresh clone"},
	}

	fmt.Printf("  %-32s %s\n", "PITFALL", "WHY IT MATTERS")

	for _, pitfall := range pitfalls {
		fmt.Printf("  %-32s %s\n", pitfall.problem, pitfall.fix)
	}

	fmt.Println("\n  Local dry run:")
	fmt.Println("    act -j test            # https://github.com/nektos/act runs workflows in Docker")
	fmt.Println("    actionlint             # a static checker for workflow YAML")
	fmt.Println()
}
