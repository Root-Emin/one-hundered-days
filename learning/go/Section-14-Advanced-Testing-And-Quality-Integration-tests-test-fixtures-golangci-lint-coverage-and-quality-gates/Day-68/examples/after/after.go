// Package after is examples/before with every finding fixed.
//
// The point is not that the code is prettier: each change removes a real
// failure mode, listed in the comment above it.
package after

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

var ErrNotFound = errors.New("not found")

// WriteReport writes content to path.
//
// Fixed: the deferred Close error is captured. A failed close means the write
// may not have reached the disk, which is exactly the error worth reporting.
func WriteReport(path, content string) (err error) {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}

	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", path, closeErr)
		}
	}()

	if _, err := file.WriteString(content); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}

// Fetch returns the status code of a GET request.
//
// Fixed: a context (so the call can be cancelled and has a deadline) and a
// closed body (so the connection returns to the pool instead of leaking).
func Fetch(ctx context.Context, url string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, fmt.Errorf("get %s: %w", url, err)
	}

	defer func() {
		if err := response.Body.Close(); err != nil {
			// A close failure here is worth noticing but not worth failing
			// the call that already succeeded.
			_ = err
		}
	}()

	return response.StatusCode, nil
}

// Lookup reports whether id exists.
//
// Fixed: errors.Is survives wrapping, and %w keeps the chain intact for the
// next caller.
func Lookup(id int) error {
	err := find(id)

	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("lookup %d: %w", id, err)
	}

	return err
}

func find(id int) error {
	if id == 0 {
		return ErrNotFound
	}

	return nil
}

// Total sums values.
//
// Fixed: one initialisation, no discarded assignment.
func Total(values []int) int {
	total := 0

	for _, value := range values {
		total += value
	}

	return total
}

// Describe renders a count.
//
// Fixed: %d for an int. vet catches this at build time; production catches it
// in a support ticket.
func Describe(count int) string {
	return fmt.Sprintf("processed %d items", count)
}

// Normalise replaces tabs with spaces.
//
// Fixed: ReplaceAll says what it means, and len() is the direct question.
func Normalise(value string) string {
	value = strings.ReplaceAll(value, "\t", " ")

	if len(value) == 0 {
		return "empty"
	}

	return value
}

// Fixed: the dead helper is gone entirely. Deleting code is a legitimate fix.

// Greeting builds a greeting for name.
//
// Fixed: no overwritten assignment, and the typo in the comment is corrected.
func Greeting(name string) string {
	return "hello " + name
}
