// Package review is about the comments themselves: what to look for, and how
// to say it.
//
// The checklist half is mechanical - a list of questions, ordered so the
// expensive ones come first. The feedback half is not mechanical at all, and
// the detector here is deliberately modest: it flags a handful of phrasings
// that reliably land badly, and it cannot tell you whether a comment is
// *right*. It is a spell-checker for tone, not a judge.
//
// Why bother automating any of it: a review comment is asynchronous, written
// by someone under time pressure and read by someone whose work is being
// criticised. Text loses every softening signal a conversation has. The habits
// below exist to put those signals back.
package review

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Category orders the checklist by what is most expensive to get wrong.
//
// The ordering is the useful part. A reviewer who starts with naming and
// formatting runs out of attention before reaching the concurrency bug, and
// the author gets three rounds of style comments on code that does not work.
type Category int

const (
	// Correctness: does it do the right thing, including at the edges?
	Correctness Category = iota
	// Security: what happens when the input is hostile?
	Security
	// Tests: what proves it works, and what would catch it breaking?
	Tests
	// Design: will this be easy to change in six months?
	Design
	// Readability: can the next person follow it?
	Readability
	// Performance: only where it matters, and only with a measurement.
	Performance
)

func (c Category) String() string {
	return [...]string{"correctness", "security", "tests", "design", "readability", "performance"}[c]
}

// Item is one checklist question.
type Item struct {
	Category Category
	Question string
	Why      string
}

// Checklist is what a reviewer actually walks through.
//
// It is short on purpose. A forty-item checklist is read once and then
// ignored; this one fits on a screen and covers what reviews actually catch.
func Checklist() []Item {
	return []Item{
		{Correctness, "What happens on the error path?",
			"the happy path is the one the author tested; the error path is where reviews earn their keep"},
		{Correctness, "What are the boundaries - empty, nil, zero, one, maximum?",
			"off-by-one and nil-map bugs cluster at the edges of the input space"},
		{Correctness, "If this runs twice, or concurrently, is it still right?",
			"retries and concurrency are the default in a service, not an exception"},
		{Security, "Is any of this input attacker-controlled, and is it validated at the boundary?",
			"trust decisions made deep in a call stack are invisible to the next reader"},
		{Security, "Does anything secret reach a log, an error message or a response?",
			"a token in a log line is a token in your log aggregator, forever"},
		{Tests, "Does a test fail if this change is reverted?",
			"a test that passes either way is documentation, not a test"},
		{Tests, "Is the failure mode tested, not just the success?",
			"most production incidents are the untested branch"},
		{Design, "Is this the smallest change that solves the problem?",
			"an unrelated refactor in the same pull request costs the review its focus"},
		{Design, "What will this look like when the next feature lands on top of it?",
			"the cost of a design shows up in the change after this one"},
		{Readability, "Would a new team member understand why, not just what?",
			"the code says what it does; only a comment can say why it does it that way"},
		{Readability, "Are the names accurate now, after the code moved?",
			"a stale name is worse than no name - it actively misleads"},
		{Performance, "Is there a measurement, or is this a guess?",
			"an optimization without a benchmark is a readability cost with no proven benefit"},
	}
}

// Comment is one review comment.
type Comment struct {
	File     string
	Line     int
	Category Category
	// Blocking separates "I will not approve this" from "here is a thought".
	// Saying which one you mean, explicitly, saves the author guessing.
	Blocking bool
	Body     string
}

// Format renders a comment with the convention that makes severity obvious.
//
// "nit:" is a real protocol, not decoration: it tells the author "this is
// optional, merge without it if you disagree". Teams that use it consistently
// argue less, because the author knows which comments are negotiable.
func (c Comment) Format() string {
	prefix := "nit: "

	if c.Blocking {
		prefix = "blocking: "
	}

	location := c.File

	if c.Line > 0 {
		location = fmt.Sprintf("%s:%d", c.File, c.Line)
	}

	return fmt.Sprintf("%s  %s[%s] %s", location, prefix, c.Category, c.Body)
}

//
// TONE
//

// ToneFinding is a phrasing worth rewording, with a suggested rewrite.
type ToneFinding struct {
	Phrase     string
	Why        string
	Suggestion string
}

// tonePatterns are the phrasings that reliably land badly in review.
//
// Every one of them has the same shape: it comments on the PERSON or their
// judgement rather than the code. The fix is always the same too - describe
// the code, ask about the intent, or state the consequence.
var tonePatterns = []struct {
	pattern    *regexp.Regexp
	why        string
	suggestion string
}{
	{
		pattern:    regexp.MustCompile(`(?i)\bwhy (did|would) you\b`),
		why:        "addresses the person's decision rather than the code",
		suggestion: "\"what is this handling?\" or \"I expected X here - what am I missing?\"",
	},
	{
		pattern:    regexp.MustCompile(`(?i)\byou (always|never|clearly|obviously)\b`),
		why:        "generalises about the author, not this change",
		suggestion: "talk about this specific line and what it does",
	},
	{
		pattern:    regexp.MustCompile(`(?i)\b(obviously|clearly|just|simply) \w+`),
		why:        "implies the answer is obvious, which makes asking feel expensive",
		suggestion: "drop the word; if it were obvious the code would already do it",
	},
	{
		pattern:    regexp.MustCompile(`(?i)\bthis is (wrong|bad|terrible|awful|nonsense)\b`),
		why:        "a verdict with no information in it",
		suggestion: "say what breaks and when: \"this returns nil when the slice is empty, and the caller dereferences it\"",
	},
	{
		pattern:    regexp.MustCompile(`(?i)\b(did you even|didn't you)\b`),
		why:        "rhetorical, and reads as an accusation",
		suggestion: "ask the direct question instead",
	},
	{
		pattern:    regexp.MustCompile(`(?i)\bplease (fix|change|rewrite) (this|it)\b\s*\.?\s*$`),
		why:        "an instruction with no reason, so the author cannot evaluate or push back on it",
		suggestion: "say what to change it to, and why",
	},
}

// CheckTone flags phrasings worth rewording.
//
// The limits are worth stating plainly: this cannot tell a blunt-but-kind
// comment from a cruel one, it has no idea whether the technical point is
// correct, and a comment that passes every check here can still be unhelpful.
// It catches the handful of habits that are easy to fall into and easy to fix.
func CheckTone(body string) []ToneFinding {
	var findings []ToneFinding

	seen := make(map[string]bool)

	for _, pattern := range tonePatterns {
		match := pattern.pattern.FindString(body)
		if match == "" || seen[pattern.why] {
			continue
		}

		seen[pattern.why] = true

		findings = append(findings, ToneFinding{
			// Trim trailing punctuation the anchor pulled in, so the phrase
			// quoted back to the author is the phrase, not the sentence.
			Phrase:     strings.TrimRight(strings.TrimSpace(match), ".!?"),
			Why:        pattern.why,
			Suggestion: pattern.suggestion,
		})
	}

	sort.Slice(findings, func(i, j int) bool { return findings[i].Phrase < findings[j].Phrase })

	return findings
}

// Rewrite pairs a poor comment with a better one, for the demo and the docs.
type Rewrite struct {
	Before string
	After  string
	Note   string
}

// Rewrites are the four moves that turn a complaint into a review comment.
func Rewrites() []Rewrite {
	return []Rewrite{
		{
			Before: "This is wrong.",
			After:  "blocking: [correctness] this returns nil when orders is empty, and the caller dereferences it on line 40 - a panic on the first customer with no orders.",
			Note:   "say what breaks, and when. A verdict carries no information the author can act on.",
		},
		{
			Before: "Why did you use a mutex here?",
			After:  "question: [design] what does the mutex protect? If it is only the counter, atomic.Int64 would drop the lock entirely - but I may be missing a second invariant.",
			Note:   "ask about the code, and leave room to be wrong. You usually are, about a third of the time.",
		},
		{
			Before: "Just use a map.",
			After:  "nit: [performance] a map would make the lookup O(1) - though with n<10 the slice is likely faster. Fine either way; flagging in case it grows.",
			Note:   "drop \"just\". Mark it as optional so the author can disagree cheaply.",
		},
		{
			Before: "Missing tests.",
			After:  "blocking: [tests] the retry path (client.go:88) has no test - if I invert that condition, does anything fail? That is the branch most likely to break silently.",
			Note:   "name the specific gap, and give the author the test that would close it.",
		},
	}
}
