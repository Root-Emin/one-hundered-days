// Package gitinfo reads the local repository, so the checks run against a real
// branch instead of a fixture.
//
// Everything here shells out to git rather than using a library: git is
// already installed wherever this runs, its output is stable, and a tool that
// needs a 30 MB dependency to read a commit message will not get installed.
package gitinfo

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-91/internal/changeset"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-91/internal/prlint"
)

// recordSeparator marks the end of one commit in the log output.
//
// A commit body contains newlines and blank lines, so splitting the log on
// those loses commits. An explicit separator that cannot appear in a message
// is the only reliable way to parse this.
const recordSeparator = "%x1e"

// Branch returns the current branch name.
func Branch(ctx context.Context, repo string) (string, error) {
	return run(ctx, repo, "rev-parse", "--abbrev-ref", "HEAD")
}

// DefaultBranch guesses the trunk: origin's HEAD if it is set, otherwise main,
// otherwise master.
func DefaultBranch(ctx context.Context, repo string) string {
	if head, err := run(ctx, repo, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); err == nil {
		if index := strings.LastIndex(head, "/"); index >= 0 {
			return head[index+1:]
		}
	}

	for _, candidate := range []string{"main", "master"} {
		if _, err := run(ctx, repo, "rev-parse", "--verify", "--quiet", candidate); err == nil {
			return candidate
		}
	}

	return "main"
}

// Commits returns the commits on this branch that are not on base.
//
// base..HEAD is the range a reviewer sees, which is the range that should be
// linted - not the whole history.
func Commits(ctx context.Context, repo, base string) ([]prlint.Commit, error) {
	output, err := run(ctx, repo, "log", "--format=%H%n%s%n%b"+recordSeparator, base+"..HEAD")
	if err != nil {
		return nil, err
	}

	var commits []prlint.Commit

	for _, record := range strings.Split(output, "\x1e") {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}

		lines := strings.SplitN(record, "\n", 3)

		commit := prlint.Commit{Hash: strings.TrimSpace(lines[0])}

		if len(lines) > 1 {
			commit.Subject = strings.TrimSpace(lines[1])
		}

		if len(lines) > 2 {
			commit.Body = strings.TrimSpace(lines[2])
		}

		commits = append(commits, commit)
	}

	return commits, nil
}

// Diff returns the changeset between base and HEAD.
//
// The three-dot form diffs against the MERGE BASE, not the tip of base. With
// two dots, every commit that landed on main since you branched counts as part
// of your change - which is how a 40-line fix reports as 3,000 lines.
func Diff(ctx context.Context, repo, base string) (changeset.Changeset, error) {
	output, err := run(ctx, repo, "diff", "--numstat", base+"...HEAD")
	if err != nil {
		return changeset.Changeset{}, err
	}

	return changeset.ParseNumstat(output)
}

// UncommittedDiff is the fallback when a branch has no commits of its own -
// the common case while the change is still being written.
func UncommittedDiff(ctx context.Context, repo string) (changeset.Changeset, error) {
	output, err := run(ctx, repo, "diff", "--numstat", "HEAD")
	if err != nil {
		return changeset.Changeset{}, err
	}

	return changeset.ParseNumstat(output)
}

// IsRepository reports whether path is inside a git work tree.
func IsRepository(ctx context.Context, repo string) bool {
	output, err := run(ctx, repo, "rev-parse", "--is-inside-work-tree")

	return err == nil && output == "true"
}

func run(ctx context.Context, repo string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = repo

	output, err := command.Output()
	if err != nil {
		// git puts the useful part on stderr, which Output() captures only in
		// the ExitError - without this the caller gets "exit status 128".
		var exitErr *exec.ExitError

		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(exitErr.Stderr)))
		}

		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}

	return strings.TrimSpace(string(output)), nil
}
