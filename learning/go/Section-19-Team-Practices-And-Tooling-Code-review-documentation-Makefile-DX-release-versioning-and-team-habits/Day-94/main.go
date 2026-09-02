// Day 94 - Team practices: release and versioning.
//
// A release is a promise. The version number says how much of it a caller has
// to read; the notes say what to do about it; the build says which bytes are
// running; the deprecation policy says what will still work next quarter.
//
//	internal/semver         derives the version from the commits
//	internal/releasenotes   turns the commits into notes for a deployer
//	internal/reproducible   builds twice and compares the digests
//	internal/deprecation    lints the promise, and marks responses at runtime
//
// Run: go run ./Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-94
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-94/internal/deprecation"
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
	commits := sampleCommits()

	demoVersioning(commits)
	demoNotes(commits)

	if err := demoReproducible(); err != nil {
		return err
	}

	return demoDeprecation()
}

// sampleCommits is one release's worth of history.
func sampleCommits() []semver.Commit {
	return []semver.Commit{
		semver.ParseCommit("a1b2c3d4e5f6", "feat(api): add GET /products/{sku}/reservations", "", "ada"),
		semver.ParseCommit("b2c3d4e5f6a7", "fix(store): close rows on the error path",
			"A burst of errors exhausted the connection pool.", "grace"),
		semver.ParseCommit("c3d4e5f6a7b8", "refactor(store): extract the query builder", "", "ada"),
		semver.ParseCommit("d4e5f6a7b8c9", "test(store): cover the error path", "", "grace"),
		semver.ParseCommit("e5f6a7b8c9d0", "feat(api)!: error bodies are now {error, message}",
			"BREAKING CHANGE: error responses used to be a bare string on some\n"+
				"endpoints. Switch on the \"error\" field, which is a stable code;\n"+
				"\"message\" is for humans and will keep changing.\n\n"+
				"Migration: replace string comparisons on the body with a check on\n"+
				"the error field.", "ada"),
		semver.ParseCommit("f6a7b8c9d0e1", "perf(api): reuse the response buffer", "", "linus"),
		semver.ParseCommit("a7b8c9d0e1f2", "chore(deps): bump modernc.org/sqlite", "", "grace"),
	}
}

//
// 1. VERSIONING
//

func demoVersioning(commits []semver.Commit) {
	section("1. The version number is a promise")

	fmt.Println("  PATCH  bug fixes; upgrade blind")
	fmt.Println("  MINOR  new features, nothing removed; upgrade blind")
	fmt.Println("  MAJOR  something you depend on changed or is gone; read the notes")
	fmt.Println()
	fmt.Println("  break that promise once and every consumer pins an exact version and")
	fmt.Println("  reads every diff - which costs them the whole benefit of versioning.")

	fmt.Println("\n  deriving the bump from the commits, so nobody has to remember:")
	fmt.Printf("  %-12s %-9s %-9s %s\n", "commit", "type", "breaking", "subject")

	for _, commit := range commits {
		commitType := commit.Type

		if commitType == "" {
			commitType = "-"
		}

		fmt.Printf("  %-12s %-9s %-9t %s\n",
			commit.Hash[:8], commitType, commit.Breaking, truncate(commit.Subject, 46))
	}

	current, _ := semver.Parse("v1.2.3")

	next, bump := semver.NextFor(current, commits)

	fmt.Printf("\n  %s + %s bump -> %s\n", current, bump, next)
	fmt.Println("  one commit marked feat! makes this a major release, whether or not")
	fmt.Println("  anyone remembers it three weeks later at tagging time.")

	fmt.Println("\n  the same commits against a 0.x version:")

	unstable, _ := semver.Parse("v0.4.1")

	unstableNext, _ := semver.NextFor(unstable, commits)

	fmt.Printf("  %s + major bump -> %s\n", unstable, unstableNext)
	fmt.Println("  below 1.0.0 a breaking change bumps the MINOR: 0.y.z is defined as")
	fmt.Println("  unstable, and Go modules treat every 0.x as compatible with every")
	fmt.Println("  other. Shipping 1.0.0 is the act of promising stability - it should be")
	fmt.Println("  deliberate, not the side effect of a \"!\" in a commit message.")

	fmt.Println("\n  ordering, including the case a string comparison gets wrong:")

	for _, pair := range [][2]string{
		{"v1.9.0", "v1.10.0"},
		{"v1.0.0-rc.1", "v1.0.0"},
		{"v1.0.0-rc.2", "v1.0.0-rc.10"},
		{"v2.0.0", "v1.99.99"},
	} {
		left, _ := semver.Parse(pair[0])
		right, _ := semver.Parse(pair[1])

		relation := map[int]string{-1: "<", 0: "=", 1: ">"}[semver.Compare(left, right)]

		fmt.Printf("    %-12s %s %s\n", pair[0], relation, pair[1])
	}
}

//
// 2. RELEASE NOTES
//

func demoNotes(commits []semver.Commit) {
	section("2. Release notes, for the person deploying at 4pm on a Thursday")

	previous, _ := semver.Parse("v1.2.3")
	version, _ := semver.NextFor(previous, commits)

	notes := releasenotes.Build(previous, version, commits, time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC))

	fmt.Println("  " + notes.Summary())
	fmt.Println()

	rendered := notes.Markdown(releasenotes.Options{
		Repository:     "https://github.com/acme/catalog",
		IncludeAuthors: true,
	})

	for _, line := range strings.Split(strings.TrimRight(rendered, "\n"), "\n") {
		fmt.Println("  " + line)
	}

	fmt.Println()
	fmt.Println("  breaking changes come FIRST and carry their migration note, because a")
	fmt.Println("  deployer who reads nothing else must still see them. The refactor and")
	fmt.Println("  the test commit are counted and omitted: they are real work, and")
	fmt.Println("  invisible to whoever is deciding whether to deploy.")
}

//
// 3. REPRODUCIBLE BUILDS
//

func demoReproducible() error {
	section("3. Reproducible builds: which bytes are actually running")

	fmt.Printf("  environment: %s\n\n", reproducible.Current())

	packagePath := "./" + filepath.ToSlash(filepath.Join(dayDir(), "cmd", "demoapp"))

	spec := reproducible.BuildSpec{
		Package:       packagePath,
		Version:       "v1.3.0",
		Commit:        "e5f6a7b8c9d0",
		CommitTime:    time.Date(2026, 9, 1, 18, 30, 0, 0, time.UTC),
		LDFlagsVar:    "example.com/onehundredday/" + strings.ReplaceAll(dayDir(), string(filepath.Separator), "/") + "/internal/buildinfo",
		Trimpath:      true,
		Deterministic: true,
	}

	result, err := reproducible.Verify(context.Background(), spec)
	if err != nil {
		return err
	}

	// Printed from the result, so what is shown is what actually ran -
	// including the output path Verify chose.
	fmt.Printf("  go %s\n\n", strings.Join(result.Flags, " "))

	fmt.Printf("  build 1: %s\n", result.FirstDigest)
	fmt.Printf("  build 2: %s\n", result.SecondDigest)
	fmt.Printf("  identical: %t  (%d bytes, %s for both builds)\n",
		result.Reproducible, result.Size, result.Elapsed.Round(time.Millisecond))

	if !result.Reproducible {
		return fmt.Errorf("the same source produced two different binaries")
	}

	fmt.Println()
	fmt.Println("  the two builds ran in DIFFERENT temporary directories on purpose:")
	fmt.Println("  building twice in one place hides a path dependency that a CI runner")
	fmt.Println("  would expose.")

	// And the counter-example: a build stamped with the current time.
	fmt.Println("\n  now the same build, stamped with the BUILD time instead of the commit time:")

	first := spec
	first.CommitTime = time.Now()

	second := spec
	second.CommitTime = time.Now().Add(time.Second)

	firstDir, err := os.MkdirTemp("", "repro-time-a")
	if err != nil {
		return err
	}

	defer func() {
		if err := os.RemoveAll(firstDir); err != nil {
			_ = err
		}
	}()

	secondDir, err := os.MkdirTemp("", "repro-time-b")
	if err != nil {
		return err
	}

	defer func() {
		if err := os.RemoveAll(secondDir); err != nil {
			_ = err
		}
	}()

	first.Output = filepath.Join(firstDir, "binary")
	second.Output = filepath.Join(secondDir, "binary")

	firstDigest, err := reproducible.Build(context.Background(), first)
	if err != nil {
		return err
	}

	secondDigest, err := reproducible.Build(context.Background(), second)
	if err != nil {
		return err
	}

	fmt.Printf("    %s\n    %s\n    identical: %t\n",
		firstDigest, secondDigest, firstDigest == secondDigest)
	fmt.Println("    one second apart, two different binaries. A build timestamp makes")
	fmt.Println("    reproducibility impossible by construction - stamp the COMMIT time.")

	fmt.Println("\n  the other sources of drift, in the order they bite:")

	for _, line := range []string{
		"absolute paths      -> -trimpath",
		"VCS stamping        -> -buildvcs=false (a dirty tree is unreproducible by definition)",
		"the C toolchain     -> CGO_ENABLED=0 where possible",
		"the Go version      -> pin it; a different compiler emits different code",
		"dependency versions -> go.sum, and never a floating version",
	} {
		fmt.Println("    " + line)
	}

	return nil
}

//
// 4. DEPRECATION
//

func demoDeprecation() error {
	section("4. Deprecating safely")

	fmt.Println("  a deprecation is a promise with three parts:")
	fmt.Println("    what replaces it   a warning with no alternative cannot be acted on")
	fmt.Println("    when it goes       a date, not \"eventually\"")
	fmt.Println("    how to migrate     the actual change, ideally a diff")

	notices, err := deprecation.Scan(filepath.Join(dayDir(), "internal"))
	if err != nil {
		return err
	}

	fmt.Printf("\n  scanning this day's packages: %d Deprecated marker(s)\n", len(notices))

	for _, notice := range notices {
		fmt.Printf("    %s (%s:%d)\n", notice.Symbol, filepath.Base(notice.File), notice.Line)
		fmt.Printf("      %s\n", notice.Text)

		if notice.HasDate {
			fmt.Printf("      removal: %s\n", notice.RemoveAfter.Format("2006-01-02"))
		}
	}

	issues := deprecation.Check(notices, time.Now())

	fmt.Printf("\n  lint: %d issue(s)\n", len(issues))

	for _, issue := range issues {
		fmt.Println("    " + issue.String())
	}

	// The runtime half.
	section("4b. Telling the client, not just the changelog")

	policy := deprecation.Policy{
		Endpoint:    "GET /product?sku=",
		Replacement: "https://api.example.com/products/{sku}",
		SunsetAt:    time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		Docs:        "https://docs.example.com/migrations/products",
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelWarn,
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey {
				return slog.Attr{}
			}

			return attr
		},
	}))

	handler := policy.Middleware(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(`{"sku":"KB-01"}`)); err != nil {
			_ = err
		}
	}))

	server := httptest.NewServer(handler)

	defer server.Close()

	fmt.Println("  three calls to the deprecated endpoint:")

	for i := 0; i < 3; i++ {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
		if err != nil {
			return err
		}

		request.Header.Set("User-Agent", "legacy-client/0.9")

		response, err := server.Client().Do(request)
		if err != nil {
			return err
		}

		if i == 0 {
			fmt.Println("\n  response headers (RFC 8594):")

			for _, key := range []string{"Deprecation", "Sunset", "Link"} {
				for _, value := range response.Header.Values(key) {
					fmt.Printf("    %s: %s\n", key, value)
				}
			}
		}

		if err := response.Body.Close(); err != nil {
			return err
		}
	}

	fmt.Println()
	fmt.Println("  the log line above fired ONCE, not three times: a deprecated endpoint")
	fmt.Println("  under load would otherwise produce a line per request, which costs")
	fmt.Println("  money and gets the warning filtered out.")
	fmt.Println()
	fmt.Println("  the headers matter because most clients never read a changelog. A")
	fmt.Println("  client library can act on Deprecation and Sunset without anyone")
	fmt.Println("  reading anything.")

	fmt.Println("\n  the timeline, for the release notes:")
	fmt.Println("    " + policy.Timeline())

	section("5. The release checklist")

	for i, step := range []string{
		"the commits carry their type, and breaking ones carry a migration note",
		"the version comes from the commits, not from someone's memory",
		"the notes lead with the breaking changes",
		"the tag is on the commit that was tested, and the tree is clean",
		"the build is reproducible: -trimpath, -buildvcs=false, commit time, pinned Go",
		"the digest is recorded with the artifact, so a rebuild can be checked",
		"anything removed was deprecated first, with a date that has passed",
	} {
		fmt.Printf("  %d. %s\n", i+1, step)
	}

	fmt.Println("\n  the full procedure is in docs/RELEASING.md")

	return nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}

	return value[:limit-3] + "..."
}

func dayDir() string {
	path := filepath.Join(
		"Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits",
		"Day-94")

	if _, err := os.Stat(path); err != nil {
		return "."
	}

	return path
}

func section(title string) {
	fmt.Printf("\n%s\n%s\n", title, strings.Repeat("=", len(title)))
}
