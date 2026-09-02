// Command release does the mechanical half of cutting a release.
//
//	go run ./Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-94/cmd/release next     # the version the commits imply
//	go run ./Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-94/cmd/release notes    # the notes for that version
//	go run ./Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-94/cmd/release verify   # build twice, compare the digests
//
// It reads the real repository: the tags for the current version, the commits
// since that tag for the bump and the notes.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-94/internal/releasenotes"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-94/internal/reproducible"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-94/internal/semver"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		repo       = flag.String("repo", ".", "repository path")
		remote     = flag.String("remote", "", "repository URL for commit links")
		packageDir = flag.String("package", "", "package to build for the verify command")
	)

	flag.Parse()

	command := "next"

	if args := flag.Args(); len(args) > 0 {
		command = args[0]
	}

	ctx := context.Background()

	switch command {
	case "next":
		current, next, bump, _, err := plan(ctx, *repo)
		if err != nil {
			return err
		}

		fmt.Printf("current: %s\n", current)
		fmt.Printf("bump:    %s\n", bump)
		fmt.Printf("next:    %s\n", next)

		if bump == semver.None {
			fmt.Println("\nnothing user-visible has changed; there is nothing to release")
		}

	case "notes":
		current, next, _, commits, err := plan(ctx, *repo)
		if err != nil {
			return err
		}

		notes := releasenotes.Build(current, next, commits, time.Now())

		fmt.Print(notes.Markdown(releasenotes.Options{Repository: *remote, IncludeAuthors: true}))

	case "verify":
		if *packageDir == "" {
			return errors.New("verify needs -package, e.g. -package ./cmd/api")
		}

		result, err := reproducible.Verify(ctx, reproducible.BuildSpec{
			Package:       *packageDir,
			Trimpath:      true,
			Deterministic: true,
		})
		if err != nil {
			return err
		}

		fmt.Printf("environment: %s\n", reproducible.Current())
		fmt.Printf("digest:      %s\n", result.FirstDigest)
		fmt.Printf("rebuild:     %s\n", result.SecondDigest)
		fmt.Printf("reproducible: %t (%d bytes)\n", result.Reproducible, result.Size)

		if !result.Reproducible {
			return errors.New("the same source produced two different binaries")
		}

	default:
		return fmt.Errorf("unknown command %q (use next, notes or verify)", command)
	}

	return nil
}

// plan reads the repository and works out what the next release would be.
func plan(ctx context.Context, repo string) (current, next semver.Version, bump semver.Bump, commits []semver.Commit, err error) {
	tags, err := git(ctx, repo, "tag", "--list")
	if err != nil {
		return current, next, bump, nil, err
	}

	current, found := semver.Latest(strings.Fields(tags))

	revisionRange := "HEAD"

	if found {
		revisionRange = current.String() + "..HEAD"
	}

	commits, err = commitsIn(ctx, repo, revisionRange)
	if err != nil {
		return current, next, bump, nil, err
	}

	next, bump = semver.NextFor(current, commits)

	return current, next, bump, commits, nil
}

// recordSeparator ends each commit record; a commit body contains newlines, so
// splitting on those would lose commits.
const recordSeparator = "%x1e"

func commitsIn(ctx context.Context, repo, revisionRange string) ([]semver.Commit, error) {
	output, err := git(ctx, repo, "log", "--format=%H%n%an%n%s%n%b"+recordSeparator, revisionRange)
	if err != nil {
		return nil, err
	}

	var commits []semver.Commit

	for _, record := range strings.Split(output, "\x1e") {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}

		lines := strings.SplitN(record, "\n", 4)

		if len(lines) < 3 {
			continue
		}

		body := ""

		if len(lines) > 3 {
			body = lines[3]
		}

		commits = append(commits, semver.ParseCommit(
			strings.TrimSpace(lines[0]), strings.TrimSpace(lines[2]), body, strings.TrimSpace(lines[1])))
	}

	return commits, nil
}

func git(ctx context.Context, repo string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = repo

	output, err := command.Output()
	if err != nil {
		var exitErr *exec.ExitError

		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(exitErr.Stderr)))
		}

		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}

	return strings.TrimSpace(string(output)), nil
}
