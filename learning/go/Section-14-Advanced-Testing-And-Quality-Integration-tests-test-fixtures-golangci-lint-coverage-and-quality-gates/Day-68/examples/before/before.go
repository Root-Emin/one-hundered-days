//go:build lintdemo

// Package before is deliberately wrong.
//
// Every function here trips at least one linter. Read it next to
// examples/after/after.go, which fixes the same code, and run:
//
//	golangci-lint run --no-config --default=none --build-tags=lintdemo \
//	  -E errcheck -E staticcheck -E govet -E ineffassign -E bodyclose -E errorlint \
//	  ./examples/before/
//
// The 'lintdemo' build tag keeps this file out of ordinary builds, so
// `go vet ./...` across the repository stays green while the example remains
// real, compilable code rather than a comment block. .golangci.yml excludes
// the directory as well, because its whole purpose is to fail.
package before

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

var ErrNotFound = errors.New("not found")

// errcheck: the error from Close is dropped, so a failed flush is invisible.
func WriteReport(path, content string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}

	defer file.Close()

	_, err = file.WriteString(content)

	return err
}

// bodyclose + noctx: the response body is never closed (a leaked connection
// per call), and the request carries no context, so it cannot be cancelled.
func Fetch(url string) (int, error) {
	response, err := http.Get(url)
	if err != nil {
		return 0, err
	}

	return response.StatusCode, nil
}

// errorlint: comparing errors with == breaks the moment anyone wraps one, and
// %v in a "wrapping" message wraps nothing at all.
func Lookup(id int) error {
	err := find(id)

	if err == ErrNotFound {
		return fmt.Errorf("lookup %d: %v", id, err)
	}

	return err
}

func find(id int) error {
	if id == 0 {
		return ErrNotFound
	}

	return nil
}

// ineffassign: the first assignment to total is thrown away without ever being
// read - almost always a sign the wrong variable was used.
func Total(values []int) int {
	total := 0

	total = len(values)

	for _, value := range values {
		total += value
	}

	return total
}

// govet (printf): the verb and the argument do not match, so the message is
// garbage at runtime and nothing complains at compile time.
func Describe(count int) string {
	return fmt.Sprintf("processed %s items", count)
}

// staticcheck: strings.Replace with n = -1 is strings.ReplaceAll, and the
// comparison against an empty string is a longer way to ask for len == 0.
func Normalise(value string) string {
	value = strings.Replace(value, "\t", " ", -1)

	if strings.Compare(value, "") == 0 {
		return "empty"
	}

	return value
}

// unused: never called from anywhere, so it is dead weight in every review.
func unusedHelper(value string) string {
	return strings.ToUpper(value)
}

// staticcheck SA4006 / misspell: the assignment is overwritten immediately,
// and the comment below has a typo the linter will point at.
//
// The two assignments are seperate on purpose (they are not).
func Greeting(name string) string {
	message := "hello"

	message = "hello " + name

	return message
}
