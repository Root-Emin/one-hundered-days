package review_test

import (
	"strings"
	"testing"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-91/internal/review"
)

// The checklist's ORDER is the part that matters: correctness before
// readability, so a reviewer's attention is spent where defects are.
func TestChecklistIsOrderedByCost(t *testing.T) {
	items := review.Checklist()

	if len(items) == 0 {
		t.Fatal("the checklist is empty")
	}

	for i := 1; i < len(items); i++ {
		if items[i].Category < items[i-1].Category {
			t.Fatalf("item %d (%s) comes after %s - the checklist is out of cost order",
				i, items[i].Category, items[i-1].Category)
		}
	}

	if items[0].Category != review.Correctness {
		t.Errorf("the checklist starts with %s, want correctness", items[0].Category)
	}
}

// Every item needs its reason. A checklist that says what to do but not why
// gets applied mechanically and then argued with.
func TestEveryChecklistItemExplainsItself(t *testing.T) {
	for _, item := range review.Checklist() {
		if strings.TrimSpace(item.Question) == "" {
			t.Errorf("%s: empty question", item.Category)
		}

		if strings.TrimSpace(item.Why) == "" {
			t.Errorf("%s: %q has no reason", item.Category, item.Question)
		}
	}
}

func TestCommentFormatMarksSeverity(t *testing.T) {
	blocking := review.Comment{
		File: "store.go", Line: 42, Category: review.Correctness, Blocking: true,
		Body: "rows is not closed on the error path.",
	}

	nit := review.Comment{
		File: "store.go", Line: 51, Category: review.Readability,
		Body: "customerIDs reads better than ids.",
	}

	if got := blocking.Format(); !strings.Contains(got, "blocking:") {
		t.Errorf("blocking comment = %q", got)
	}

	if got := nit.Format(); !strings.Contains(got, "nit:") {
		t.Errorf("nit comment = %q", got)
	}

	if got := blocking.Format(); !strings.Contains(got, "store.go:42") {
		t.Errorf("comment does not carry its location: %q", got)
	}
}

func TestCommentWithoutALineOmitsIt(t *testing.T) {
	comment := review.Comment{File: "store.go", Category: review.Design, Body: "..."}

	if got := comment.Format(); strings.Contains(got, "store.go:0") {
		t.Errorf("a comment with no line should not render :0, got %q", got)
	}
}

func TestToneCheckFlagsPersonDirectedPhrasing(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{"Why did you use a mutex here?", "Why did you"},
		{"You always forget the error path.", "You always"},
		{"Just use a map.", "Just use"},
		{"This is wrong.", "This is wrong"},
		{"Did you even run the tests?", "Did you even"},
		{"Please fix this.", "Please fix this"},
	}

	for _, testCase := range cases {
		t.Run(testCase.body, func(t *testing.T) {
			findings := review.CheckTone(testCase.body)

			if len(findings) == 0 {
				t.Fatalf("expected a tone finding for %q", testCase.body)
			}

			if !strings.EqualFold(findings[0].Phrase, testCase.want) {
				t.Errorf("phrase = %q, want %q", findings[0].Phrase, testCase.want)
			}

			if findings[0].Suggestion == "" {
				t.Error("a tone finding without a suggestion is just a complaint")
			}
		})
	}
}

// The check has to stay quiet on comments that are direct but fine, or it
// becomes noise and gets ignored.
func TestGoodCommentsAreNotFlagged(t *testing.T) {
	comments := []string{
		"blocking: this returns nil when the slice is empty, and line 40 dereferences it.",
		"nit: customerIDs reads better than ids here.",
		"question: what does the mutex protect? I may be missing an invariant.",
		"This handles the retry case well - worth a comment saying why 3 attempts.",
	}

	for _, comment := range comments {
		if findings := review.CheckTone(comment); len(findings) != 0 {
			t.Errorf("%q was flagged: %+v", comment, findings)
		}
	}
}

// Each rewrite has to be a genuine improvement, not just longer: the "after"
// must pass the tone check that the "before" fails.
func TestRewritesImproveOnTheirBefore(t *testing.T) {
	rewrites := review.Rewrites()

	if len(rewrites) == 0 {
		t.Fatal("no rewrites")
	}

	flagged := 0

	for _, rewrite := range rewrites {
		// Not every weak comment is machine-detectable - "Missing tests." is
		// unhelpful and passes every pattern here, which is precisely the
		// limitation CheckTone documents. So the assertion is that the check
		// catches SOME of them, and never fires on a rewrite.
		if findings := review.CheckTone(rewrite.Before); len(findings) > 0 {
			flagged++
		}

		if findings := review.CheckTone(rewrite.After); len(findings) != 0 {
			t.Errorf("the \"after\" %q is still flagged: %+v", rewrite.After, findings)
		}

		if len(rewrite.After) <= len(rewrite.Before) {
			t.Errorf("the rewrite of %q adds no information", rewrite.Before)
		}

		if strings.TrimSpace(rewrite.Note) == "" {
			t.Errorf("the rewrite of %q has no explanation", rewrite.Before)
		}
	}

	if flagged == 0 {
		t.Error("no \"before\" example is caught by CheckTone - the examples and the check have drifted apart")
	}
}

func TestToneFindingsAreDeduplicated(t *testing.T) {
	findings := review.CheckTone("Just do this. Just do that. Just simplify it.")

	if len(findings) != 1 {
		t.Errorf("findings = %d, want 1 - repeating the same finding is noise", len(findings))
	}
}

func TestCategoryNames(t *testing.T) {
	for _, category := range []review.Category{
		review.Correctness, review.Security, review.Tests,
		review.Design, review.Readability, review.Performance,
	} {
		if category.String() == "" {
			t.Errorf("category %d has no name", category)
		}
	}
}
