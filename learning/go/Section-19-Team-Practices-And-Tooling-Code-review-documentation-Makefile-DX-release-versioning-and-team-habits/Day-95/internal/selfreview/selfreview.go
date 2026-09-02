// Package selfreview runs a review checklist over your own code.
//
// Reviewing your own work is famously ineffective - you read what you meant,
// not what you wrote. A checklist helps, and a checklist a machine applies
// helps more, because it does not get bored on the fourth file.
//
// These are the mechanical items from the Day 91 checklist: the ones that can
// be decided by looking at the syntax tree. The judgement items - is this the
// right design, will the next feature fit, is the test asserting anything real
// - are exactly what remains for a human, which is the point.
package selfreview

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Category groups a finding, matching the review checklist's ordering.
type Category string

// The categories, in cost order.
const (
	// Correctness covers error handling and control flow.
	Correctness Category = "correctness"
	// Tests covers what is and is not exercised.
	Tests Category = "tests"
	// Design covers size and shape.
	Design Category = "design"
	// Readability covers what the next reader will struggle with.
	Readability Category = "readability"
)

// Comment is one review comment, in the format a reviewer would post.
type Comment struct {
	Category Category
	// Blocking separates "I will not approve this" from "here is a thought".
	Blocking bool
	File     string
	Line     int
	Body     string
}

// String renders the comment the way a review tool would show it.
func (c Comment) String() string {
	prefix := "nit"

	if c.Blocking {
		prefix = "blocking"
	}

	return fmt.Sprintf("%s:%d  %s: [%s] %s", filepath.Base(c.File), c.Line, prefix, c.Category, c.Body)
}

// Options tune the checks.
type Options struct {
	// MaxFunctionLines is where a function becomes hard to hold in your head.
	MaxFunctionLines int
	// MaxParameters is where a signature starts asking for a struct.
	MaxParameters int
	// RequireContextFirst reports a context.Context that is not the first
	// parameter.
	RequireContextFirst bool
}

// DefaultOptions are reasonable thresholds.
//
// They are conventions, not laws: a 70-line function that is a single switch
// statement is fine, and the reviewer says so. The value of the number is that
// it makes the outlier visible.
func DefaultOptions() Options {
	return Options{
		MaxFunctionLines:    60,
		MaxParameters:       5,
		RequireContextFirst: true,
	}
}

// Review walks a directory and returns the comments a reviewer would leave.
func Review(root string, options Options) ([]Comment, error) {
	var comments []Comment

	fileSet := token.NewFileSet()

	testedPackages := make(map[string]bool)
	exported := make(map[string][]exportedFunc)

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			base := filepath.Base(path)

			if base == "testdata" || base == "vendor" || (strings.HasPrefix(base, ".") && len(base) > 1) {
				return filepath.SkipDir
			}

			return nil
		}

		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		file, err := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}

		directory := filepath.Dir(path)

		if strings.HasSuffix(path, "_test.go") {
			testedPackages[directory] = true

			// A test file is still code, but the size and shape rules do not
			// apply the same way - a table-driven test is legitimately long.
			return nil
		}

		comments = append(comments, reviewFile(fileSet, path, file, options)...)

		exported[directory] = append(exported[directory], exportedFuncs(fileSet, path, file)...)

		return nil
	})
	if err != nil {
		return nil, err
	}

	// "Is there a test?" is the highest-value question on the checklist, and
	// the only one here that spans files.
	for directory, functions := range exported {
		if testedPackages[directory] || len(functions) == 0 {
			continue
		}

		comments = append(comments, Comment{
			Category: Tests,
			Blocking: true,
			File:     functions[0].file,
			Line:     functions[0].line,
			Body: fmt.Sprintf("package %s exports %d symbol(s) and has no test file: what proves this works, "+
				"and what would catch it breaking?", filepath.Base(directory), len(functions)),
		})
	}

	sort.Slice(comments, func(i, j int) bool {
		if comments[i].File != comments[j].File {
			return comments[i].File < comments[j].File
		}

		return comments[i].Line < comments[j].Line
	})

	return comments, nil
}

type exportedFunc struct {
	name string
	file string
	line int
}

func exportedFuncs(fileSet *token.FileSet, path string, file *ast.File) []exportedFunc {
	var functions []exportedFunc

	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || !function.Name.IsExported() {
			continue
		}

		functions = append(functions, exportedFunc{
			name: function.Name.Name,
			file: path,
			line: fileSet.Position(function.Pos()).Line,
		})
	}

	return functions
}

// unfinishedMarkers are the conventional prefixes for work left undone.
var unfinishedMarkers = []string{"TODO", "FIXME", "XXX", "HACK"}

func reviewFile(fileSet *token.FileSet, path string, file *ast.File, options Options) []Comment {
	var comments []Comment

	// Unfinished-work markers: a note to yourself is a note nobody else can
	// act on, and it outlives whoever wrote it.
	//
	// The marker must START the comment - the "// TODO(name): ..." convention -
	// rather than merely appear in it. Matching anywhere flags prose that
	// happens to mention the word, and this file's own explanation was the
	// first false positive it produced.
	for _, group := range file.Comments {
		for _, comment := range group.List {
			text := strings.TrimSpace(strings.TrimLeft(comment.Text, "/* \t"))

			for _, marker := range unfinishedMarkers {
				if !strings.HasPrefix(text, marker) {
					continue
				}

				comments = append(comments, Comment{
					Category: Readability,
					File:     path,
					Line:     fileSet.Position(comment.Pos()).Line,
					Body: fmt.Sprintf("%s left in the code: link an issue or delete it - "+
						"an unowned marker outlives whoever wrote it", marker),
				})

				break
			}
		}
	}

	ast.Inspect(file, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.FuncDecl:
			comments = append(comments, reviewFunc(fileSet, path, typed, options)...)

		}

		return true
	})

	return comments
}

func reviewFunc(fileSet *token.FileSet, path string, function *ast.FuncDecl, options Options) []Comment {
	var comments []Comment

	position := fileSet.Position(function.Pos())

	if function.Body != nil {
		lines := fileSet.Position(function.End()).Line - position.Line

		if options.MaxFunctionLines > 0 && lines > options.MaxFunctionLines {
			comments = append(comments, Comment{
				Category: Design,
				File:     path,
				Line:     position.Line,
				Body: fmt.Sprintf("%s is %d lines (over %d): can a reader hold it in their head, "+
					"or is there a step in the middle that wants a name?",
					function.Name.Name, lines, options.MaxFunctionLines),
			})
		}
	}

	if function.Type.Params == nil {
		return comments
	}

	count := 0

	for _, field := range function.Type.Params.List {
		count += max(len(field.Names), 1)
	}

	if options.MaxParameters > 0 && count > options.MaxParameters {
		comments = append(comments, Comment{
			Category: Design,
			File:     path,
			Line:     position.Line,
			Body: fmt.Sprintf("%s takes %d parameters: at this width callers start passing them "+
				"in the wrong order, and the compiler cannot help when several share a type",
				function.Name.Name, count),
		})
	}

	if !options.RequireContextFirst {
		return comments
	}

	for i, field := range function.Type.Params.List {
		if !isContext(field.Type) || i == 0 {
			continue
		}

		comments = append(comments, Comment{
			Category: Readability,
			File:     path,
			Line:     position.Line,
			Body: fmt.Sprintf("%s takes a context.Context as parameter %d: convention is first, "+
				"and conventions are what let a reader skim", function.Name.Name, i+1),
		})
	}

	return comments
}

func isContext(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	packageName, ok := selector.X.(*ast.Ident)

	return ok && packageName.Name == "context" && selector.Sel.Name == "Context"
}

// Summary counts comments by category.
func Summary(comments []Comment) map[Category]int {
	counts := make(map[Category]int)

	for _, comment := range comments {
		counts[comment.Category]++
	}

	return counts
}

// Blocking returns only the comments that would block an approval.
func Blocking(comments []Comment) []Comment {
	var blocking []Comment

	for _, comment := range comments {
		if comment.Blocking {
			blocking = append(blocking, comment)
		}
	}

	return blocking
}
