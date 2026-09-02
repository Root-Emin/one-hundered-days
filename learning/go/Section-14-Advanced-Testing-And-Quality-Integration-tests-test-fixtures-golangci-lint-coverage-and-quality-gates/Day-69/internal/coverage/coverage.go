// Package coverage parses a Go coverage profile and enforces thresholds.
//
// The profile format is documented nowhere official, so here it is:
//
//	mode: set
//	<file>:<startLine>.<startCol>,<endLine>.<endCol> <numStatements> <count>
//
// Each line is one "block" - a straight-line run of statements. count is the
// number of times it executed (mode: set records 0 or 1). Coverage is
// therefore statements-executed divided by statements-total, NOT lines and not
// branches: a fully covered if statement whose else branch never ran still
// counts every statement it contains.
//
// That distinction is why 100% coverage is not 100% tested.
package coverage

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

type Block struct {
	File       string
	StartLine  int
	EndLine    int
	Statements int
	Count      int
}

type Profile struct {
	Mode   string
	Blocks []Block
}

// PackageCoverage is the per-package rollup a gate is expressed in.
type PackageCoverage struct {
	Package    string
	Covered    int
	Total      int
	Percent    float64
	MissedFile map[string]int // file -> uncovered statements
}

// Parse reads a coverage profile produced by `go test -coverprofile`.
func Parse(reader io.Reader) (Profile, error) {
	scanner := bufio.NewScanner(reader)

	// Coverage lines are short, but a generated file can produce long ones.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	profile := Profile{}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "mode:") {
			profile.Mode = strings.TrimSpace(strings.TrimPrefix(line, "mode:"))

			continue
		}

		block, err := parseBlock(line)
		if err != nil {
			return Profile{}, err
		}

		profile.Blocks = append(profile.Blocks, block)
	}

	if err := scanner.Err(); err != nil {
		return Profile{}, fmt.Errorf("read profile: %w", err)
	}

	if profile.Mode == "" {
		return Profile{}, fmt.Errorf("not a coverage profile: no mode line")
	}

	return profile, nil
}

func ParseFile(path string) (Profile, error) {
	file, err := os.Open(path)
	if err != nil {
		return Profile{}, fmt.Errorf("open profile: %w", err)
	}

	defer func() {
		if err := file.Close(); err != nil {
			// Reading is done by this point; a close failure changes nothing.
			_ = err
		}
	}()

	return Parse(file)
}

func parseBlock(line string) (Block, error) {
	// <file>:<start>,<end> <statements> <count>
	position, rest, found := strings.Cut(line, ":")
	if !found {
		return Block{}, fmt.Errorf("malformed profile line: %q", line)
	}

	fields := strings.Fields(rest)
	if len(fields) != 3 {
		return Block{}, fmt.Errorf("malformed profile line: %q", line)
	}

	span := strings.Split(fields[0], ",")
	if len(span) != 2 {
		return Block{}, fmt.Errorf("malformed span: %q", fields[0])
	}

	startLine, err := strconv.Atoi(strings.Split(span[0], ".")[0])
	if err != nil {
		return Block{}, fmt.Errorf("malformed start line in %q: %w", line, err)
	}

	endLine, err := strconv.Atoi(strings.Split(span[1], ".")[0])
	if err != nil {
		return Block{}, fmt.Errorf("malformed end line in %q: %w", line, err)
	}

	statements, err := strconv.Atoi(fields[1])
	if err != nil {
		return Block{}, fmt.Errorf("malformed statement count in %q: %w", line, err)
	}

	count, err := strconv.Atoi(fields[2])
	if err != nil {
		return Block{}, fmt.Errorf("malformed execution count in %q: %w", line, err)
	}

	return Block{
		File:       position,
		StartLine:  startLine,
		EndLine:    endLine,
		Statements: statements,
		Count:      count,
	}, nil
}

// ByPackage rolls the blocks up per package.
func (p Profile) ByPackage() []PackageCoverage {
	packages := make(map[string]*PackageCoverage)

	for _, block := range p.Blocks {
		name := packageOf(block.File)

		entry, seen := packages[name]
		if !seen {
			entry = &PackageCoverage{Package: name, MissedFile: map[string]int{}}
			packages[name] = entry
		}

		entry.Total += block.Statements

		if block.Count > 0 {
			entry.Covered += block.Statements
		} else {
			entry.MissedFile[block.File] += block.Statements
		}
	}

	result := make([]PackageCoverage, 0, len(packages))

	for _, entry := range packages {
		if entry.Total > 0 {
			entry.Percent = float64(entry.Covered) / float64(entry.Total) * 100
		}

		result = append(result, *entry)
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Package < result[j].Package })

	return result
}

func (p Profile) Total() (covered, total int, percent float64) {
	for _, block := range p.Blocks {
		total += block.Statements

		if block.Count > 0 {
			covered += block.Statements
		}
	}

	if total > 0 {
		percent = float64(covered) / float64(total) * 100
	}

	return covered, total, percent
}

func packageOf(file string) string {
	index := strings.LastIndex(file, "/")
	if index < 0 {
		return file
	}

	return file[:index]
}

//
// GATES
//

// Gate is one rule: a package path suffix and the minimum percentage it must
// reach. Suffix matching keeps the config readable in a repository with long
// module paths.
type Gate struct {
	PathSuffix string  `json:"path_suffix"`
	Minimum    float64 `json:"minimum"`
	Reason     string  `json:"reason"`
}

type Policy struct {
	// TotalMinimum applies to the whole profile.
	TotalMinimum float64 `json:"total_minimum"`

	// Gates are per-package overrides, evaluated most-specific first.
	Gates []Gate `json:"gates"`

	// Ignore lists packages excluded from the total, with a reason. Generated
	// code and main packages are the usual honest exclusions.
	Ignore []Gate `json:"ignore"`
}

type Violation struct {
	Package string
	Percent float64
	Minimum float64
	Reason  string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s: %.1f%% < %.1f%% required (%s)", v.Package, v.Percent, v.Minimum, v.Reason)
}

// TotalFor computes the overall percentage across the packages the policy
// actually gates.
//
// Counting ignored packages in the total would make the number meaningless:
// adding a thin main package would drag the project average down without any
// change in tested behaviour.
func (p Policy) TotalFor(packages []PackageCoverage) (covered, total int, percent float64) {
	for _, coverage := range packages {
		if matches(p.Ignore, coverage.Package) != nil {
			continue
		}

		covered += coverage.Covered
		total += coverage.Total
	}

	if total > 0 {
		percent = float64(covered) / float64(total) * 100
	}

	return covered, total, percent
}

// Check evaluates the policy and returns every violation, not just the first:
// a developer should see the whole gap in one run.
func (p Policy) Check(packages []PackageCoverage, totalPercent float64) []Violation {
	var violations []Violation

	for _, coverage := range packages {
		if matches(p.Ignore, coverage.Package) != nil {
			continue
		}

		gate := matches(p.Gates, coverage.Package)
		if gate == nil {
			continue
		}

		if coverage.Percent+0.0001 < gate.Minimum {
			violations = append(violations, Violation{
				Package: coverage.Package,
				Percent: coverage.Percent,
				Minimum: gate.Minimum,
				Reason:  gate.Reason,
			})
		}
	}

	if p.TotalMinimum > 0 && totalPercent+0.0001 < p.TotalMinimum {
		violations = append(violations, Violation{
			Package: "TOTAL",
			Percent: totalPercent,
			Minimum: p.TotalMinimum,
			Reason:  "overall project threshold",
		})
	}

	return violations
}

// matches returns the most specific gate whose suffix matches.
func matches(gates []Gate, packageName string) *Gate {
	var best *Gate

	for i := range gates {
		if !strings.HasSuffix(packageName, gates[i].PathSuffix) {
			continue
		}

		if best == nil || len(gates[i].PathSuffix) > len(best.PathSuffix) {
			best = &gates[i]
		}
	}

	return best
}
