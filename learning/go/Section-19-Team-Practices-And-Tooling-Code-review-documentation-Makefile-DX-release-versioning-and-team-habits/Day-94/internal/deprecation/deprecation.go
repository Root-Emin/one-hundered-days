// Package deprecation is about removing things without breaking the people who
// use them.
//
// A deprecation is a promise with three parts, and leaving any of them out is
// how "we announced it" turns into an outage:
//
//	what replaces it   a warning with no alternative is a warning nobody can
//	                   act on
//	when it goes       a date, not "eventually". Without one there is no reason
//	                   to migrate today, so nobody does
//	how to migrate     the actual change, ideally a diff
//
// The package does two things: it lints Go doc comments for Deprecated markers
// that are missing any of the three, and it provides the runtime side - the
// Deprecation and Sunset HTTP headers from RFC 8594, plus a log line that
// fires once rather than on every call.
package deprecation

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

//
// THE LINT
//

// Notice is one parsed Deprecated marker.
type Notice struct {
	Symbol      string
	File        string
	Line        int
	Text        string
	Replacement string
	RemoveAfter time.Time
	HasDate     bool
}

// Issue is a deprecation that is missing part of the promise.
type Issue struct {
	Rule    string
	Symbol  string
	File    string
	Line    int
	Message string
}

// String renders an issue for a terminal report.
func (i Issue) String() string {
	return fmt.Sprintf("%-22s %s:%d %s: %s", i.Rule, filepath.Base(i.File), i.Line, i.Symbol, i.Message)
}

var (
	// "Deprecated: use X instead." - the convention godoc, gopls and staticcheck
	// all recognise. The colon and the capital D are required.
	deprecatedMarker = regexp.MustCompile(`(?m)^Deprecated:\s*(.+)$`)
	replacementHint  = regexp.MustCompile(`(?i)\b(use|see|replaced by|migrate to)\s+([A-Za-z0-9_.\-/]+)`)
	datePattern      = regexp.MustCompile(`(\d{4})-(\d{2})-(\d{2})`)
)

// Scan finds every Deprecated marker in a directory tree.
func Scan(root string) ([]Notice, error) {
	var notices []Notice

	fileSet := token.NewFileSet()

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

		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, err := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}

		notices = append(notices, scanFile(fileSet, path, file)...)

		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(notices, func(i, j int) bool {
		if notices[i].File != notices[j].File {
			return notices[i].File < notices[j].File
		}

		return notices[i].Line < notices[j].Line
	})

	return notices, nil
}

func scanFile(fileSet *token.FileSet, path string, file *ast.File) []Notice {
	var notices []Notice

	add := func(doc *ast.CommentGroup, name string, pos token.Pos) {
		if doc == nil {
			return
		}

		match := deprecatedMarker.FindStringSubmatch(doc.Text())
		if match == nil {
			return
		}

		notice := Notice{
			Symbol: name,
			File:   path,
			Line:   fileSet.Position(pos).Line,
			Text:   strings.TrimSpace(match[1]),
		}

		if replacement := replacementHint.FindStringSubmatch(notice.Text); replacement != nil {
			notice.Replacement = replacement[2]
		}

		if date := datePattern.FindString(doc.Text()); date != "" {
			if parsed, err := time.Parse("2006-01-02", date); err == nil {
				notice.RemoveAfter = parsed
				notice.HasDate = true
			}
		}

		notices = append(notices, notice)
	}

	for _, declaration := range file.Decls {
		switch typed := declaration.(type) {
		case *ast.FuncDecl:
			add(typed.Doc, typed.Name.Name, typed.Pos())

		case *ast.GenDecl:
			for _, spec := range typed.Specs {
				switch value := spec.(type) {
				case *ast.TypeSpec:
					doc := value.Doc
					if doc == nil {
						doc = typed.Doc
					}

					add(doc, value.Name.Name, value.Pos())

				case *ast.ValueSpec:
					doc := value.Doc
					if doc == nil {
						doc = typed.Doc
					}

					for _, ident := range value.Names {
						add(doc, ident.Name, ident.Pos())
					}
				}
			}
		}
	}

	return notices
}

// Check reports deprecations that are missing part of the promise, or overdue.
func Check(notices []Notice, now time.Time) []Issue {
	var issues []Issue

	for _, notice := range notices {
		if notice.Replacement == "" {
			issues = append(issues, Issue{
				Rule: "no_replacement", Symbol: notice.Symbol, File: notice.File, Line: notice.Line,
				Message: "no alternative named; write \"Deprecated: use X instead\"",
			})
		}

		if !notice.HasDate {
			issues = append(issues, Issue{
				Rule: "no_removal_date", Symbol: notice.Symbol, File: notice.File, Line: notice.Line,
				Message: "no removal date; without one there is no reason to migrate today, so nobody does",
			})

			continue
		}

		if now.After(notice.RemoveAfter) {
			// Not a mistake to fix by moving the date: either remove it, or
			// admit that it is not going away and drop the marker.
			issues = append(issues, Issue{
				Rule: "overdue", Symbol: notice.Symbol, File: notice.File, Line: notice.Line,
				Message: fmt.Sprintf("removal date %s has passed; remove it or extend it deliberately",
					notice.RemoveAfter.Format("2006-01-02")),
			})
		}
	}

	return issues
}

//
// THE RUNTIME SIDE
//

// Policy describes a deprecated HTTP endpoint.
type Policy struct {
	// Endpoint is what is going away, for the log line.
	Endpoint string
	// Replacement is what to use instead. It goes in the Link header.
	Replacement string
	// SunsetAt is when it stops working.
	SunsetAt time.Time
	// Docs is a URL explaining the migration.
	Docs string
}

// Middleware marks responses from a deprecated endpoint.
//
// The headers are RFC 8594: Deprecation says it is deprecated, Sunset says when
// it stops working, and Link rel="successor-version" points at the replacement.
// A client library can act on those without anyone reading a changelog - which
// matters because most clients never do.
func (p Policy) Middleware(logger *slog.Logger, next http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	var once sync.Once

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Deprecation", "true")

		if !p.SunsetAt.IsZero() {
			w.Header().Set("Sunset", p.SunsetAt.UTC().Format(http.TimeFormat))
		}

		if p.Docs != "" {
			w.Header().Add("Link", fmt.Sprintf(`<%s>; rel="deprecation"; type="text/html"`, p.Docs))
		}

		if p.Replacement != "" {
			w.Header().Add("Link", fmt.Sprintf(`<%s>; rel="successor-version"`, p.Replacement))
		}

		// Logged ONCE per process, not per request. A deprecated endpoint
		// under load would otherwise produce a log line per call, which costs
		// money and gets the warning filtered out.
		once.Do(func() {
			logger.Warn("deprecated endpoint in use",
				slog.String("endpoint", p.Endpoint),
				slog.String("replacement", p.Replacement),
				slog.String("sunset", p.SunsetAt.Format(time.RFC3339)),
				slog.String("user_agent", r.UserAgent()))
		})

		next.ServeHTTP(w, r)
	})
}

// Expired reports whether the sunset date has passed.
func (p Policy) Expired(now time.Time) bool {
	return !p.SunsetAt.IsZero() && now.After(p.SunsetAt)
}

// Timeline renders the deprecation as a line for the release notes.
func (p Policy) Timeline() string {
	return fmt.Sprintf("%s is deprecated; use %s. It stops working on %s. %s",
		p.Endpoint, p.Replacement, p.SunsetAt.Format("2006-01-02"), p.Docs)
}
