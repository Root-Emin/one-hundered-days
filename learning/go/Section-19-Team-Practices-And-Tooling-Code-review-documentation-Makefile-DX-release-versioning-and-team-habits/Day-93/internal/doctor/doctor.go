// Package doctor checks that a machine can build and run the project.
//
// It exists because "it does not work on my machine" is almost always one of
// five things - a stale Go version, a missing tool, an unset variable, a busy
// port, an unmigrated database - and finding out which one costs a newcomer
// half a morning and a teammate an interruption.
//
// Every check answers with a FIX, not a verdict. A diagnostic that says
// "golangci-lint: not found" and stops has done half the job; the useful half
// is "run make tools".
package doctor

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Status is the outcome of one check.
type Status int

const (
	// OK means the check passed.
	OK Status = iota
	// Warn means the project works, but something is not ideal.
	Warn
	// Fail means the project will not build or run until this is fixed.
	Fail
)

// String renders a status for a terminal report.
func (s Status) String() string {
	switch s {
	case OK:
		return "ok"
	case Warn:
		return "warn"
	case Fail:
		return "FAIL"
	default:
		return "?"
	}
}

// Result is one check's outcome.
type Result struct {
	Name   string
	Status Status
	Detail string
	// Fix is the command or action that resolves it. Empty when Status is OK.
	Fix string
}

// String renders a result as one aligned line.
func (r Result) String() string {
	line := fmt.Sprintf("  %-5s %-22s %s", r.Status, r.Name, r.Detail)

	if r.Fix != "" {
		line += "\n        fix: " + r.Fix
	}

	return line
}

// Report is every check.
type Report struct {
	Results []Result
}

// Failed reports whether anything must be fixed.
func (r Report) Failed() bool {
	for _, result := range r.Results {
		if result.Status == Fail {
			return true
		}
	}

	return false
}

// Counts returns how many results fall into each status.
func (r Report) Counts() (ok, warn, fail int) {
	for _, result := range r.Results {
		switch result.Status {
		case OK:
			ok++
		case Warn:
			warn++
		case Fail:
			fail++
		}
	}

	return ok, warn, fail
}

// Options configure the checks.
type Options struct {
	// MinGoMinor is the minimum Go 1.x minor version.
	MinGoMinor int
	// Tools are executables that should be on PATH, mapped to the fix.
	Tools map[string]string
	// EnvVars are variables worth setting, mapped to why.
	EnvVars map[string]string
	// Port is the address the service will bind, checked for availability.
	Port string
	// Database is the SQLite file the service uses; empty skips the check.
	Database string
}

// DefaultOptions are the checks this project needs.
func DefaultOptions() Options {
	return Options{
		MinGoMinor: 22, // net/http's method patterns and ServeMux wildcards
		Tools: map[string]string{
			"git":           "install git from your package manager",
			"golangci-lint": "make tools",
			"gofmt":         "it ships with Go; check your PATH",
		},
		EnvVars: map[string]string{
			"NOTES_ADDR": "the listen address; defaults to :8093 without it",
			"NOTES_DB":   "the database file; defaults to notes.db without it",
		},
		Port:     ":8093",
		Database: "notes.db",
	}
}

// Run executes every check.
func Run(ctx context.Context, options Options) Report {
	var report Report

	report.Results = append(report.Results, checkGoVersion(options.MinGoMinor))
	report.Results = append(report.Results, checkTools(options.Tools)...)
	report.Results = append(report.Results, checkEnv(options.EnvVars)...)

	if options.Port != "" {
		report.Results = append(report.Results, checkPort(options.Port))
	}

	if options.Database != "" {
		report.Results = append(report.Results, checkDatabase(ctx, options.Database))
	}

	return report
}

func checkGoVersion(minMinor int) Result {
	version := runtime.Version() // e.g. go1.26.5

	minor := 0

	if parts := strings.SplitN(strings.TrimPrefix(version, "go"), ".", 3); len(parts) >= 2 {
		minor, _ = strconv.Atoi(parts[1])
	}

	if minor < minMinor {
		return Result{
			Name: "go version", Status: Fail,
			Detail: fmt.Sprintf("%s is too old; this project needs go1.%d or newer", version, minMinor),
			Fix:    "install a newer Go from https://go.dev/dl/",
		}
	}

	return Result{Name: "go version", Status: OK, Detail: version}
}

func checkTools(tools map[string]string) []Result {
	names := make([]string, 0, len(tools))

	for name := range tools {
		names = append(names, name)
	}

	sortStrings(names)

	results := make([]Result, 0, len(names))

	for _, name := range names {
		path, err := exec.LookPath(name)
		if err != nil {
			// A missing tool is a warning, not a failure: the code still
			// builds and the tests still run. Only the lint step is affected.
			results = append(results, Result{
				Name: name, Status: Warn, Detail: "not on PATH", Fix: tools[name],
			})

			continue
		}

		results = append(results, Result{Name: name, Status: OK, Detail: path})
	}

	return results
}

func checkEnv(vars map[string]string) []Result {
	names := make([]string, 0, len(vars))

	for name := range vars {
		names = append(names, name)
	}

	sortStrings(names)

	results := make([]Result, 0, len(names))

	for _, name := range names {
		value := os.Getenv(name)

		if value == "" {
			results = append(results, Result{
				Name: name, Status: Warn, Detail: "not set - " + vars[name],
				Fix: "direnv allow (loads .envrc), or export it in your shell",
			})

			continue
		}

		results = append(results, Result{Name: name, Status: OK, Detail: value})
	}

	return results
}

func checkPort(address string) Result {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return Result{
			Name: "port " + address, Status: Warn,
			Detail: "already in use",
			Fix:    fmt.Sprintf("stop whatever is listening, or run with ADDR=:8094 (make run ADDR=:8094)"),
		}
	}

	if err := listener.Close(); err != nil {
		return Result{Name: "port " + address, Status: Warn, Detail: err.Error()}
	}

	return Result{Name: "port " + address, Status: OK, Detail: "free"}
}

func checkDatabase(ctx context.Context, path string) Result {
	if _, err := os.Stat(path); err != nil {
		return Result{
			Name: "database", Status: Warn,
			Detail: path + " does not exist yet",
			Fix:    "make migrate",
		}
	}

	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(1000)")
	if err != nil {
		return Result{Name: "database", Status: Fail, Detail: err.Error(), Fix: "make clean && make setup"}
	}

	defer func() {
		if err := db.Close(); err != nil {
			_ = err
		}
	}()

	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		return Result{Name: "database", Status: Fail, Detail: err.Error(), Fix: "make clean && make setup"}
	}

	var applied int

	err = db.QueryRowContext(pingCtx, `SELECT COUNT(*) FROM schema_migrations;`).Scan(&applied)
	if err != nil {
		return Result{
			Name: "database", Status: Warn,
			Detail: path + " exists but has no schema_migrations table",
			Fix:    "make migrate",
		}
	}

	return Result{Name: "database", Status: OK, Detail: fmt.Sprintf("%s, %d migration(s) applied", path, applied)}
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
