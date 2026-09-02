package gitinfo_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-91/internal/gitinfo"
)

// newRepo builds a throwaway repository, so the tests exercise real git output
// rather than a fixture that drifts from it.
func newRepo(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	dir := t.TempDir()

	commands := [][]string{
		{"init", "--quiet", "--initial-branch=main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	}

	for _, args := range commands {
		run(t, dir, args...)
	}

	write(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "--quiet", "-m", "feat(app): add the entry point")

	return dir
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()

	command := exec.Command("git", args...)
	command.Dir = dir

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}

	return string(output)
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestIsRepository(t *testing.T) {
	repo := newRepo(t)

	if !gitinfo.IsRepository(context.Background(), repo) {
		t.Error("a freshly initialised repository was not recognised")
	}

	if gitinfo.IsRepository(context.Background(), t.TempDir()) {
		t.Error("an empty directory was reported as a repository")
	}
}

func TestBranchAndDefault(t *testing.T) {
	repo := newRepo(t)

	branch, err := gitinfo.Branch(context.Background(), repo)
	if err != nil {
		t.Fatalf("Branch: %v", err)
	}

	if branch != "main" {
		t.Errorf("branch = %q, want main", branch)
	}

	if got := gitinfo.DefaultBranch(context.Background(), repo); got != "main" {
		t.Errorf("default branch = %q, want main", got)
	}
}

// A commit body contains blank lines, so the log has to be parsed with an
// explicit record separator - splitting on newlines silently loses commits.
func TestCommitsParsesSubjectAndBody(t *testing.T) {
	repo := newRepo(t)

	run(t, repo, "checkout", "--quiet", "-b", "feature")

	write(t, repo, "store.go", "package main\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "--quiet", "-m", "fix(store): close rows on the error path",
		"-m", "Scan failing left the connection open.\n\nA burst of errors then exhausted the pool.")

	write(t, repo, "store2.go", "package main\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "--quiet", "-m", "test(store): cover the error path")

	commits, err := gitinfo.Commits(context.Background(), repo, "main")
	if err != nil {
		t.Fatalf("Commits: %v", err)
	}

	if len(commits) != 2 {
		t.Fatalf("commits = %d, want 2", len(commits))
	}

	// git log is newest first.
	if commits[1].Subject != "fix(store): close rows on the error path" {
		t.Errorf("subject = %q", commits[1].Subject)
	}

	if !strings.Contains(commits[1].Body, "exhausted the pool") {
		t.Errorf("the body lost its second paragraph: %q", commits[1].Body)
	}

	if commits[0].Hash == "" {
		t.Error("commit hash is empty")
	}
}

// The three-dot range diffs against the merge base. With two dots, everything
// that landed on main since branching would count as part of the change.
func TestDiffCountsOnlyThisBranch(t *testing.T) {
	repo := newRepo(t)

	run(t, repo, "checkout", "--quiet", "-b", "feature")

	write(t, repo, "feature.go", "package main\n\n// one\n// two\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "--quiet", "-m", "feat(app): add a feature")

	// Meanwhile, main moves on.
	run(t, repo, "checkout", "--quiet", "main")
	write(t, repo, "unrelated.go", "package main\n"+strings.Repeat("// noise\n", 50))
	run(t, repo, "add", ".")
	run(t, repo, "commit", "--quiet", "-m", "chore(app): unrelated work on main")
	run(t, repo, "checkout", "--quiet", "feature")

	diff, err := gitinfo.Diff(context.Background(), repo, "main")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	stats := diff.Stats()

	if stats.Files != 1 {
		t.Fatalf("files = %d, want 1 - main's commits leaked into the diff", stats.Files)
	}

	if stats.Added != 4 {
		t.Errorf("added = %d, want 4", stats.Added)
	}
}

func TestUncommittedDiff(t *testing.T) {
	repo := newRepo(t)

	write(t, repo, "main.go", "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n")

	diff, err := gitinfo.UncommittedDiff(context.Background(), repo)
	if err != nil {
		t.Fatalf("UncommittedDiff: %v", err)
	}

	if len(diff.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(diff.Files))
	}
}

// git puts its useful message on stderr, which Output() only exposes through
// the ExitError - without unwrapping it the caller gets "exit status 128".
func TestErrorsCarryGitsMessage(t *testing.T) {
	repo := newRepo(t)

	_, err := gitinfo.Commits(context.Background(), repo, "no-such-branch")
	if err == nil {
		t.Fatal("expected an error for a missing branch")
	}

	if !strings.Contains(err.Error(), "no-such-branch") {
		t.Errorf("error = %v, want it to name the branch", err)
	}
}
