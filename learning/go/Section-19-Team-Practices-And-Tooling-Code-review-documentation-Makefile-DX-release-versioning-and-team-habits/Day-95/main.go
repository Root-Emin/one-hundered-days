// Day 95 - Team practices: putting the week together.
//
// Days 91-94 built four habits and the tools that check them. Today applies
// all of them to one repository - this one - and reports honestly on what is
// still missing:
//
//  1. polish the repository    internal/repoaudit: the files a newcomer needs
//  2. review it as a teammate  internal/selfreview: the mechanical checklist
//  3. cut a release            Day 94's semver and release notes, end to end
//  4. write down the gaps      docs/GAPS.md, with triggers, not wishes
//
// Run: go run ./Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-95
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-95/internal/repoaudit"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-95/internal/selfreview"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-95/internal/tasks"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	demoMVP()

	if err := demoAudit(); err != nil {
		return err
	}

	if err := demoSelfReview(); err != nil {
		return err
	}

	demoRelease()
	demoGaps()

	return nil
}

//
// 0. THE MVP
//

func demoMVP() {
	section("0. The MVP being reviewed")

	store := tasks.New()

	for _, title := range []string{"write the release notes", "audit the repository", "fix the 409"} {
		if _, err := store.Create(title, "ada"); err != nil {
			fmt.Println("  unexpected:", err)
		}
	}

	if _, err := store.Advance(1, tasks.Doing); err != nil {
		fmt.Println("  unexpected:", err)
	}

	if _, err := store.Advance(2, tasks.Done); err != nil {
		fmt.Println("  unexpected:", err)
	}

	for _, task := range store.List("") {
		fmt.Printf("  %d  %-10s %s\n", task.ID, task.Status, task.Title)
	}

	// The invariant, demonstrated rather than asserted.
	_, err := store.Advance(2, tasks.Doing)

	fmt.Printf("\n  moving a done task back to doing: %v\n", err)
	fmt.Println("  forward only, and never to the same state: a repeated advance is")
	fmt.Println("  almost always a double submit, and accepting it hides the caller's bug.")
}

//
// 1. THE AUDIT
//

func demoAudit() error {
	section("1. Can a stranger use this repository?")

	checks := repoaudit.DefaultChecks()

	report, err := repoaudit.Audit(dayDir(), checks)
	if err != nil {
		return err
	}

	fmt.Printf("  score %d%%, %d file(s) present, %d finding(s)\n\n",
		report.Score(checks), len(report.Present), len(report.Findings))

	for _, present := range report.Present {
		fmt.Printf("    ok   %s\n", present)
	}

	for _, finding := range report.Findings {
		fmt.Println("    " + finding.String())
	}

	fmt.Println()
	fmt.Println("  every check maps to a question that otherwise costs an interruption:")

	for _, check := range checks[:4] {
		fmt.Printf("    %-22s %s\n", check.Name, check.Question)
	}

	fmt.Println()
	fmt.Println("  and the limit, which is permanent: this checks that the files EXIST,")
	fmt.Println("  not that they are TRUE. A README can score 100% and be wrong.")

	// What the same audit says about a repository that has only code.
	fmt.Println("\n  the same audit against a repository that is only source code:")

	bare, err := os.MkdirTemp("", "bare-repo")
	if err != nil {
		return err
	}

	defer func() {
		if err := os.RemoveAll(bare); err != nil {
			_ = err
		}
	}()

	if err := os.WriteFile(filepath.Join(bare, "main.go"), []byte("package main\n"), 0o600); err != nil {
		return err
	}

	bareReport, err := repoaudit.Audit(bare, checks)
	if err != nil {
		return err
	}

	fmt.Printf("    score %d%%, blocked: %t\n", bareReport.Score(checks), bareReport.Blocked())

	for _, finding := range bareReport.Findings {
		if finding.Severity == repoaudit.Required {
			fmt.Printf("    %s\n", finding)
		}
	}

	return nil
}

//
// 2. THE SELF REVIEW
//

func demoSelfReview() error {
	section("2. Reviewing my own code as if it were a teammate's")

	comments, err := selfreview.Review(dayDir(), selfreview.DefaultOptions())
	if err != nil {
		return err
	}

	fmt.Printf("  %d comment(s)\n\n", len(comments))

	for _, comment := range comments {
		fmt.Println("    " + comment.String())
	}

	counts := selfreview.Summary(comments)

	fmt.Printf("\n  by category: ")

	for _, category := range []selfreview.Category{
		selfreview.Correctness, selfreview.Tests, selfreview.Design, selfreview.Readability,
	} {
		if count := counts[category]; count > 0 {
			fmt.Printf("%s=%d ", category, count)
		}
	}

	fmt.Println()
	fmt.Println()
	fmt.Println("  this run found three blocking comments the first time - three packages")
	fmt.Println("  with no test file - and they were real. The tests exist now, which is")
	fmt.Println("  the point: a checklist a machine applies does not get bored on the")
	fmt.Println("  fourth file, and reviewing your own work without one is famously")
	fmt.Println("  ineffective because you read what you meant, not what you wrote.")
	fmt.Println()
	fmt.Println("  what remains is nits, and one of them is a judgement call the tool")
	fmt.Println("  cannot make: DefaultChecks is 61 lines because it is a TABLE, and")
	fmt.Println("  splitting it would make it harder to read. The number's job is to make")
	fmt.Println("  the outlier visible, not to be obeyed. That decision is recorded in")
	fmt.Println("  docs/GAPS.md rather than argued about again next month.")

	return nil
}

//
// 3. THE RELEASE
//

func demoRelease() {
	section("3. Cutting v0.2.0")

	fmt.Println("  what went in, from CHANGELOG.md:")
	fmt.Println("    Added:   POST /tasks/{id}/advance, GET /tasks?status=")
	fmt.Println("    Changed: error bodies are now {error, message}  <- BREAKING")
	fmt.Println("    Fixed:   advancing twice returned 200 and did nothing; now 409")
	fmt.Println()
	fmt.Println("  0.1.0 -> 0.2.0, not 0.1.1, because of the breaking change. Below")
	fmt.Println("  1.0.0 a breaking change is a MINOR bump: 0.y.z is defined as unstable,")
	fmt.Println("  and Go modules treat every 0.x as compatible with every other.")
	fmt.Println()
	fmt.Println("  the procedure, in docs/RELEASING.md:")

	for i, step := range []string{
		"release next    - the version the commits imply",
		"release notes   - the notes for it",
		"make check      - everything CI runs, before the tag exists",
		"make audit      - the repository itself",
		"git tag -a v0.2.0 - on the commit that was tested, clean tree",
		"release verify  - prove the artifact can be rebuilt byte for byte",
	} {
		fmt.Printf("    %d. %s\n", i+1, step)
	}

	fmt.Println()
	fmt.Println("  the tag command is written out rather than run: this repository's")
	fmt.Println("  history belongs to its owner, and a tool that creates tags on someone's")
	fmt.Println("  behalf will eventually create one nobody wanted.")
}

//
// 4. THE GAPS
//

func demoGaps() {
	section("4. What is still missing")

	fmt.Println("  docs/GAPS.md, and every entry has two columns that matter:")
	fmt.Println("  WHY NOT YET, and WHAT WOULD TRIGGER IT.")
	fmt.Println()

	for _, gap := range [][2]string{
		{"no persistence", "the first time anything depends on surviving a restart"},
		{"no pagination", "~10,000 tasks, or the first client that times out"},
		{"no authentication", "the moment it is exposed without a gateway"},
		{"no licence", "extracting this into a repository of its own"},
		{"size nits left open", "a reviewer disagreeing - they are tables and walk functions"},
	} {
		fmt.Printf("    %-22s -> %s\n", gap[0], gap[1])
	}

	fmt.Println()
	fmt.Println("  the trigger column is what stops the list being a wish nobody reads.")
	fmt.Println("  \"we chose not to paginate, revisit at ~10,000\" ends an argument that")
	fmt.Println("  otherwise restarts every six months with a new engineer.")

	section("5. The honest summary")

	fmt.Println("  the PROCESS in this repository is in better shape than the PRODUCT.")
	fmt.Println("  that is the right order for a 100-day exercise and the wrong order for")
	fmt.Println("  a real service - if this became real, persistence and authentication")
	fmt.Println("  come before anything else on the gaps page.")
	fmt.Println()
	fmt.Println("  and the audit deliberately still reports one finding. A report with")
	fmt.Println("  zero findings is a report nobody reads carefully.")
}

func dayDir() string {
	path := filepath.Join(
		"Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits",
		"Day-95")

	if _, err := os.Stat(path); err != nil {
		return "."
	}

	return path
}

func section(title string) {
	fmt.Printf("\n%s\n%s\n", title, strings.Repeat("=", len(title)))
}
