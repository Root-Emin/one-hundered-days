// Package docslint checks that Go source is documented the way godoc expects.
//
// go vet does not check this, golangci-lint's revive can, and neither explains
// why the convention exists - so the rules here carry their reasons.
//
// The conventions, and what each one buys:
//
//   - every package has a package comment: it is the first thing on the
//     package's godoc page and in `go doc`, and without it the reader's first
//     question ("what is this for?") is answered by a list of function names
//   - a doc comment starts with the name of the thing it documents: godoc and
//     `go doc` render the first sentence as a summary, and "Returns the user"
//     next to "GetUser" reads as a fragment while "GetUser returns the user"
//     reads as documentation
//   - exported symbols are documented at all: an exported name is a promise to
//     someone outside the package, and they cannot read the body
package docslint

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

// Issue is one undocumented or badly documented declaration.
type Issue struct {
	// Rule is machine-readable: missing_package_doc, missing_doc, bad_prefix.
	Rule    string
	File    string
	Line    int
	Symbol  string
	Message string
}

// String renders an issue as file:line, the form an editor can jump to.
func (i Issue) String() string {
	location := i.File

	if i.Line > 0 {
		location = fmt.Sprintf("%s:%d", i.File, i.Line)
	}

	return fmt.Sprintf("%-20s %s: %s", i.Rule, location, i.Message)
}

// Options tunes what is checked.
type Options struct {
	// SkipTests leaves _test.go files out, which is usually what you want:
	// test helpers are not API.
	SkipTests bool
	// RequirePackageDoc reports a package with no package comment.
	RequirePackageDoc bool
	// RequireExportedDoc reports an exported symbol with no doc comment.
	RequireExportedDoc bool
	// RequireNamePrefix reports a doc comment that does not start with the
	// symbol's name.
	RequireNamePrefix bool
}

// DefaultOptions enables every check.
func DefaultOptions() Options {
	return Options{
		SkipTests:          true,
		RequirePackageDoc:  true,
		RequireExportedDoc: true,
		RequireNamePrefix:  true,
	}
}

// CheckDirectory checks every Go package under root, recursively.
func CheckDirectory(root string, options Options) ([]Issue, error) {
	var issues []Issue

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !entry.IsDir() {
			return nil
		}

		base := filepath.Base(path)

		// testdata is excluded by the go tool itself, and vendored code is
		// somebody else's documentation problem.
		if base == "testdata" || base == "vendor" || strings.HasPrefix(base, ".") && base != "." {
			return filepath.SkipDir
		}

		packageIssues, err := CheckPackage(path, options)
		if err != nil {
			return err
		}

		issues = append(issues, packageIssues...)

		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(issues, func(i, j int) bool {
		if issues[i].File != issues[j].File {
			return issues[i].File < issues[j].File
		}

		return issues[i].Line < issues[j].Line
	})

	return issues, nil
}

// CheckPackage checks the Go files in one directory (not recursive).
//
// This uses parser.ParseDir and ast.Package, both of which are deprecated in
// favour of golang.org/x/tools/go/packages. The trade is deliberate: ParseDir
// needs nothing but the standard library and works on a directory of files,
// while go/packages runs the build system to resolve imports. For a lint that
// only reads comments, type information is not worth the dependency.
func CheckPackage(directory string, options Options) ([]Issue, error) {
	fileSet := token.NewFileSet()

	packages, err := parser.ParseDir(fileSet, directory, func(info os.FileInfo) bool {
		if options.SkipTests && strings.HasSuffix(info.Name(), "_test.go") {
			return false
		}

		return true
	}, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", directory, err)
	}

	var issues []Issue

	names := make([]string, 0, len(packages))

	for name := range packages {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		issues = append(issues, checkPackage(fileSet, name, packages[name], options)...)
	}

	return issues, nil
}

func checkPackage(fileSet *token.FileSet, name string, pkg *ast.Package, options Options) []Issue {
	var issues []Issue

	if options.RequirePackageDoc && !hasPackageDoc(pkg) {
		// Reported against the first file alphabetically, which is where the
		// package comment conventionally goes (or doc.go).
		issues = append(issues, Issue{
			Rule:    "missing_package_doc",
			File:    firstFile(fileSet, pkg),
			Symbol:  name,
			Message: fmt.Sprintf("package %s has no package comment", name),
		})
	}

	fileNames := make([]string, 0, len(pkg.Files))

	for path := range pkg.Files {
		fileNames = append(fileNames, path)
	}

	sort.Strings(fileNames)

	for _, path := range fileNames {
		issues = append(issues, checkFile(fileSet, path, pkg.Files[path], options)...)
	}

	return issues
}

func hasPackageDoc(pkg *ast.Package) bool {
	for _, file := range pkg.Files {
		if file.Doc != nil && strings.TrimSpace(file.Doc.Text()) != "" {
			return true
		}
	}

	return false
}

func firstFile(fileSet *token.FileSet, pkg *ast.Package) string {
	names := make([]string, 0, len(pkg.Files))

	for path := range pkg.Files {
		names = append(names, path)
	}

	if len(names) == 0 {
		return ""
	}

	sort.Strings(names)

	_ = fileSet

	return names[0]
}

func checkFile(fileSet *token.FileSet, path string, file *ast.File, options Options) []Issue {
	var issues []Issue

	for _, declaration := range file.Decls {
		switch typed := declaration.(type) {
		case *ast.FuncDecl:
			if !typed.Name.IsExported() {
				continue
			}

			name := typed.Name.Name

			// A method is documented under its receiver type in godoc, so the
			// prefix rule applies to the method name alone.
			issues = append(issues, checkDoc(fileSet, path, typed.Doc, name, typed.Pos(), options)...)

		case *ast.GenDecl:
			issues = append(issues, checkGenDecl(fileSet, path, typed, options)...)
		}
	}

	return issues
}

func checkGenDecl(fileSet *token.FileSet, path string, declaration *ast.GenDecl, options Options) []Issue {
	var issues []Issue

	// A grouped declaration - var ( ... ) - may be documented as a block, in
	// which case the individual names do not each need a comment.
	groupDocumented := declaration.Doc != nil && strings.TrimSpace(declaration.Doc.Text()) != ""

	for _, spec := range declaration.Specs {
		switch typed := spec.(type) {
		case *ast.TypeSpec:
			if !typed.Name.IsExported() {
				continue
			}

			doc := typed.Doc
			if doc == nil {
				doc = declaration.Doc
			}

			issues = append(issues, checkDoc(fileSet, path, doc, typed.Name.Name, typed.Pos(), options)...)

		case *ast.ValueSpec:
			for _, ident := range typed.Names {
				if !ident.IsExported() {
					continue
				}

				doc := typed.Doc
				if doc == nil && groupDocumented && len(declaration.Specs) > 1 {
					// Documented by the group comment; godoc shows it.
					continue
				}

				if doc == nil {
					doc = declaration.Doc
				}

				issues = append(issues, checkDoc(fileSet, path, doc, ident.Name, ident.Pos(), options)...)
			}
		}
	}

	return issues
}

func checkDoc(fileSet *token.FileSet, path string, doc *ast.CommentGroup, name string, pos token.Pos, options Options) []Issue {
	position := fileSet.Position(pos)

	text := ""
	if doc != nil {
		text = strings.TrimSpace(doc.Text())
	}

	if text == "" {
		if !options.RequireExportedDoc {
			return nil
		}

		return []Issue{{
			Rule:    "missing_doc",
			File:    path,
			Line:    position.Line,
			Symbol:  name,
			Message: fmt.Sprintf("exported %s has no doc comment", name),
		}}
	}

	if !options.RequireNamePrefix {
		return nil
	}

	// Deprecation markers and build-tag style prefixes come before the name.
	first := strings.SplitN(text, " ", 2)[0]

	if first != name && !strings.HasPrefix(text, "Deprecated:") {
		return []Issue{{
			Rule:   "bad_prefix",
			File:   path,
			Line:   position.Line,
			Symbol: name,
			Message: fmt.Sprintf("the doc comment for %s starts with %q; godoc renders the first sentence as the summary",
				name, first),
		}}
	}

	return nil
}

// Summary counts issues by rule, for a one-line report.
func Summary(issues []Issue) map[string]int {
	counts := make(map[string]int)

	for _, issue := range issues {
		counts[issue.Rule]++
	}

	return counts
}
