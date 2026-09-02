package selfreview_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-95/internal/selfreview"
)

func write(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()

	for name, content := range files {
		full := filepath.Join(root, name)

		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	return root
}

func review(t *testing.T, root string, options selfreview.Options) []selfreview.Comment {
	t.Helper()

	comments, err := selfreview.Review(root, options)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	return comments
}

// The highest-value item on the checklist: is there a test at all?
func TestPackageWithoutTestsIsBlocking(t *testing.T) {
	root := write(t, map[string]string{
		"thing/thing.go": "package thing\n\n// Do does it.\nfunc Do() {}\n",
	})

	comments := review(t, root, selfreview.DefaultOptions())

	blocking := selfreview.Blocking(comments)

	if len(blocking) != 1 {
		t.Fatalf("blocking = %d, want 1: %v", len(blocking), comments)
	}

	if blocking[0].Category != selfreview.Tests {
		t.Errorf("category = %s, want tests", blocking[0].Category)
	}
}

func TestPackageWithTestsIsNotFlagged(t *testing.T) {
	root := write(t, map[string]string{
		"thing/thing.go":      "package thing\n\n// Do does it.\nfunc Do() {}\n",
		"thing/thing_test.go": "package thing\n\nimport \"testing\"\n\nfunc TestDo(t *testing.T) { Do() }\n",
	})

	if comments := selfreview.Blocking(review(t, root, selfreview.DefaultOptions())); len(comments) != 0 {
		t.Errorf("expected no blocking comments, got %v", comments)
	}
}

func TestLongFunctionIsANit(t *testing.T) {
	body := strings.Repeat("\tprintln(1)\n", 30)

	root := write(t, map[string]string{
		"thing/thing.go":      "package thing\n\n// Do does it.\nfunc Do() {\n" + body + "}\n",
		"thing/thing_test.go": "package thing\n",
	})

	options := selfreview.DefaultOptions()
	options.MaxFunctionLines = 10

	comments := review(t, root, options)

	if len(comments) != 1 {
		t.Fatalf("comments = %v, want 1", comments)
	}

	if comments[0].Blocking {
		t.Error("function length should be a nit, not blocking")
	}

	if comments[0].Category != selfreview.Design {
		t.Errorf("category = %s, want design", comments[0].Category)
	}
}

func TestTooManyParameters(t *testing.T) {
	root := write(t, map[string]string{
		"thing/thing.go": "package thing\n\n// Do does it.\n" +
			"func Do(a, b, c int, d, e, f string) {}\n",
		"thing/thing_test.go": "package thing\n",
	})

	options := selfreview.DefaultOptions()
	options.MaxParameters = 3

	comments := review(t, root, options)

	if len(comments) == 0 || !strings.Contains(comments[0].Body, "parameters") {
		t.Errorf("comments = %v, want a parameter-count comment", comments)
	}
}

func TestContextMustComeFirst(t *testing.T) {
	root := write(t, map[string]string{
		"thing/thing.go": "package thing\n\nimport \"context\"\n\n" +
			"// Do does it.\nfunc Do(id int, ctx context.Context) {}\n",
		"thing/thing_test.go": "package thing\n",
	})

	comments := review(t, root, selfreview.DefaultOptions())

	found := false

	for _, comment := range comments {
		if strings.Contains(comment.Body, "context.Context as parameter") {
			found = true
		}
	}

	if !found {
		t.Errorf("comments = %v, want a context-placement comment", comments)
	}

	// And the conventional signature is not flagged.
	clean := write(t, map[string]string{
		"thing/thing.go": "package thing\n\nimport \"context\"\n\n" +
			"// Do does it.\nfunc Do(ctx context.Context, id int) {}\n",
		"thing/thing_test.go": "package thing\n",
	})

	for _, comment := range review(t, clean, selfreview.DefaultOptions()) {
		if strings.Contains(comment.Body, "context.Context as parameter") {
			t.Errorf("a conventional signature was flagged: %s", comment)
		}
	}
}

// The marker must START the comment - the "// TODO(name):" convention -
// because matching anywhere flags prose that merely mentions the word. This
// package's own explanation was the first false positive it produced.
func TestUnfinishedMarkers(t *testing.T) {
	root := write(t, map[string]string{
		"thing/thing.go": "package thing\n\n" +
			"// TODO(ada): handle the empty case\n" +
			"// Do does it.\nfunc Do() {}\n\n" +
			"// Note: this mentions a TODO in prose and must not be flagged.\n" +
			"func helper() {}\n",
		"thing/thing_test.go": "package thing\n",
	})

	comments := review(t, root, selfreview.DefaultOptions())

	markers := 0

	for _, comment := range comments {
		if strings.Contains(comment.Body, "left in the code") {
			markers++
		}
	}

	if markers != 1 {
		t.Errorf("marker comments = %d, want exactly 1 (%v)", markers, comments)
	}
}

func TestCommentFormatsLikeAReviewComment(t *testing.T) {
	comment := selfreview.Comment{
		Category: selfreview.Tests, Blocking: true,
		File: "internal/tasks/tasks.go", Line: 42, Body: "no test",
	}

	rendered := comment.String()

	for _, want := range []string{"tasks.go:42", "blocking:", "[tests]", "no test"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("%q is missing %q", rendered, want)
		}
	}
}

func TestSummaryCountsByCategory(t *testing.T) {
	counts := selfreview.Summary([]selfreview.Comment{
		{Category: selfreview.Tests}, {Category: selfreview.Design}, {Category: selfreview.Design},
	})

	if counts[selfreview.Design] != 2 || counts[selfreview.Tests] != 1 {
		t.Errorf("counts = %v", counts)
	}
}

// Test files are not held to the size rules: a table-driven test is
// legitimately long.
func TestTestFilesAreNotReviewedForSize(t *testing.T) {
	body := strings.Repeat("\tprintln(1)\n", 100)

	root := write(t, map[string]string{
		"thing/thing.go":      "package thing\n\n// Do does it.\nfunc Do() {}\n",
		"thing/thing_test.go": "package thing\n\nimport \"testing\"\n\nfunc TestLong(t *testing.T) {\n" + body + "}\n",
	})

	options := selfreview.DefaultOptions()
	options.MaxFunctionLines = 10

	for _, comment := range review(t, root, options) {
		if strings.Contains(comment.File, "_test.go") {
			t.Errorf("a test file was reviewed for size: %s", comment)
		}
	}
}
