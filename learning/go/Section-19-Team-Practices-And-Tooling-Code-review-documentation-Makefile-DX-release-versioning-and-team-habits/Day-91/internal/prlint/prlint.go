// Package prlint checks the two artefacts a reviewer reads before the code:
// the commit messages and the pull request description.
//
// Both are checkable by a machine, which is the point. "Write good PR
// descriptions" is advice nobody can act on at 6pm on a Friday; "this PR is
// missing a Test plan section" is a fixable, unarguable finding that takes
// thirty seconds.
//
// What a tool CANNOT check is whether the description is honest or the commit
// message is meaningful. Those are what human review is for. Automating the
// mechanical half is what leaves room for the other half.
package prlint

import (
	"fmt"
	"regexp"
	"strings"
)

// Severity separates what blocks a merge from what does not.
//
// The distinction matters more than the rules: a checklist where everything
// blocks gets bypassed, and then none of it applies.
type Severity int

const (
	// Nit is optional. The author may ignore it and merge.
	Nit Severity = iota
	// Warning should be addressed but does not block.
	Warning
	// Blocking must be fixed before merge.
	Blocking
)

func (s Severity) String() string {
	switch s {
	case Nit:
		return "nit"
	case Warning:
		return "warning"
	case Blocking:
		return "blocking"
	default:
		return "unknown"
	}
}

// Finding is one problem, with a suggestion. A finding without a suggestion is
// a complaint.
type Finding struct {
	Severity Severity
	Rule     string
	Message  string
	Fix      string
}

func (f Finding) String() string {
	line := fmt.Sprintf("[%s] %s: %s", f.Severity, f.Rule, f.Message)

	if f.Fix != "" {
		line += "\n    fix: " + f.Fix
	}

	return line
}

// Findings is a list with a verdict.
type Findings []Finding

// Blocking reports whether anything must be fixed before merge.
func (f Findings) Blocking() bool {
	for _, finding := range f {
		if finding.Severity == Blocking {
			return true
		}
	}

	return false
}

func (f Findings) Count(severity Severity) int {
	count := 0

	for _, finding := range f {
		if finding.Severity == severity {
			count++
		}
	}

	return count
}

//
// COMMIT MESSAGES
//

// Commit is one commit as git reports it.
type Commit struct {
	Hash    string
	Subject string
	Body    string
}

// conventionalPattern matches the Conventional Commits prefix:
//
//	type(optional scope)!: subject
//
// The value is not the format itself - it is that a machine can then derive
// the changelog and the version bump from the history, which is what Day 94
// does with it.
var conventionalPattern = regexp.MustCompile(`^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)(\([a-z0-9\-/,. ]+\))?(!)?: .+`)

// MaxSubjectLength is the git convention: 50 is the target, 72 is where tools
// start truncating.
const MaxSubjectLength = 72

// pastTenseVerbs are the openings that signal a description of what happened
// rather than an instruction.
//
// The convention is the imperative - "add", not "added" - because the subject
// completes the sentence "if applied, this commit will ___". That reads
// correctly in a revert, a cherry-pick and a changelog.
var pastTenseVerbs = []string{
	"added", "adds", "fixed", "fixes", "updated", "updates", "removed", "removes",
	"changed", "changes", "refactored", "implemented", "created", "deleted",
}

// CheckCommit lints one commit message.
func CheckCommit(commit Commit) Findings {
	var findings Findings

	subject := strings.TrimSpace(commit.Subject)

	if subject == "" {
		return append(findings, Finding{
			Severity: Blocking,
			Rule:     "commit/empty",
			Message:  "the commit has no subject line",
			Fix:      "write one line describing what applying this commit does",
		})
	}

	if !conventionalPattern.MatchString(subject) {
		findings = append(findings, Finding{
			Severity: Warning,
			Rule:     "commit/conventional",
			Message:  fmt.Sprintf("%q does not follow Conventional Commits", truncate(subject, 50)),
			Fix:      "use type(scope): subject, e.g. fix(store): close rows on the error path",
		})
	}

	if len(subject) > MaxSubjectLength {
		findings = append(findings, Finding{
			Severity: Warning,
			Rule:     "commit/subject-length",
			Message:  fmt.Sprintf("the subject is %d characters (limit %d)", len(subject), MaxSubjectLength),
			Fix:      "move the detail into the body; the subject is a headline",
		})
	}

	if strings.HasSuffix(subject, ".") {
		findings = append(findings, Finding{
			Severity: Nit,
			Rule:     "commit/trailing-period",
			Message:  "the subject ends with a period",
			Fix:      "drop it - a subject is a title, not a sentence",
		})
	}

	// Check the verb after the "type(scope): " prefix, not the type itself.
	description := subject

	if index := strings.Index(subject, ": "); index >= 0 {
		description = subject[index+2:]
	}

	firstWord := strings.ToLower(strings.SplitN(strings.TrimSpace(description), " ", 2)[0])

	for _, verb := range pastTenseVerbs {
		if firstWord == verb {
			findings = append(findings, Finding{
				Severity: Nit,
				Rule:     "commit/imperative",
				Message:  fmt.Sprintf("%q is not imperative", firstWord),
				Fix:      "write it so it completes \"if applied, this commit will ___\"",
			})

			break
		}
	}

	// A body line longer than 72 characters wraps badly in `git log`, which
	// does not wrap for you.
	for i, line := range strings.Split(commit.Body, "\n") {
		if len(line) > 72 && !strings.Contains(line, "http") {
			findings = append(findings, Finding{
				Severity: Nit,
				Rule:     "commit/body-wrap",
				Message:  fmt.Sprintf("body line %d is %d characters", i+1, len(line)),
				Fix:      "wrap the body at 72 characters; git log does not wrap for you",
			})

			break
		}
	}

	return findings
}

//
// PULL REQUEST DESCRIPTION
//

// RequiredSections is the template a description has to fill in.
//
// Each one answers a question the reviewer would otherwise have to ask, and
// asking costs a round trip measured in hours:
//
//	Why       - the motivation. A diff shows what changed, never why.
//	What      - the summary, so a reviewer knows what to expect before reading.
//	Test plan - how the author verified it, and how a reviewer can.
//	Risk      - what could break, and how it would be noticed.
var RequiredSections = []string{"Why", "What", "Test plan", "Risk"}

// PullRequest is the description under review.
type PullRequest struct {
	Title       string
	Description string
	Author      string
}

// minSectionContent is the shortest body that can plausibly say anything. The
// failure mode this catches is "## Test plan\nn/a".
const minSectionContent = 12

// CheckDescription lints a pull request description.
func CheckDescription(pr PullRequest) Findings {
	var findings Findings

	title := strings.TrimSpace(pr.Title)

	switch {
	case title == "":
		findings = append(findings, Finding{
			Severity: Blocking,
			Rule:     "pr/title-empty",
			Message:  "the pull request has no title",
			Fix:      "summarise the change in one line",
		})

	case len(title) < 12:
		findings = append(findings, Finding{
			Severity: Warning,
			Rule:     "pr/title-vague",
			Message:  fmt.Sprintf("the title %q is too short to be informative", title),
			Fix:      "say what changed and where, e.g. \"fix(store): close rows on the error path\"",
		})
	}

	for _, vague := range []string{"wip", "fixes", "updates", "changes", "misc", "stuff"} {
		if strings.EqualFold(title, vague) {
			findings = append(findings, Finding{
				Severity: Warning,
				Rule:     "pr/title-vague",
				Message:  fmt.Sprintf("%q says nothing about the change", title),
				Fix:      "a reviewer decides whether to pick up the review from the title alone",
			})

			break
		}
	}

	sections := ParseSections(pr.Description)

	for _, required := range RequiredSections {
		content, found := sections[strings.ToLower(required)]

		if !found {
			findings = append(findings, Finding{
				Severity: Blocking,
				Rule:     "pr/missing-section",
				Message:  fmt.Sprintf("the description has no %q section", required),
				Fix:      "use .github/pull_request_template.md",
			})

			continue
		}

		if len(strings.TrimSpace(content)) < minSectionContent {
			findings = append(findings, Finding{
				Severity: Warning,
				Rule:     "pr/empty-section",
				Message:  fmt.Sprintf("the %q section is empty or a placeholder", required),
				Fix:      "if there is genuinely nothing to say, say why in one line",
			})
		}
	}

	return findings
}

// sectionHeading matches a markdown heading at any level.
var sectionHeading = regexp.MustCompile(`(?m)^#{1,6}\s+(.+?)\s*$`)

// ParseSections splits a markdown description into heading -> body.
//
// Headings are lower-cased so "## Test Plan" and "## test plan" are the same
// section - a template nobody can typo is worth more than a strict one.
func ParseSections(description string) map[string]string {
	sections := make(map[string]string)

	matches := sectionHeading.FindAllStringSubmatchIndex(description, -1)

	for i, match := range matches {
		heading := strings.ToLower(strings.TrimSpace(description[match[2]:match[3]]))

		// Strip any markdown emphasis or emoji padding around the heading.
		heading = strings.Trim(heading, "*_ ")

		start := match[1]

		end := len(description)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}

		sections[heading] = strings.TrimSpace(description[start:end])
	}

	return sections
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}

	return value[:limit] + "..."
}
