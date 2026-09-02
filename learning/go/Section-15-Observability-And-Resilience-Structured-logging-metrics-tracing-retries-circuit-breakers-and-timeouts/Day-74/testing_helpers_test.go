package main

import (
	"io"
	"log/slog"
)

// testLogger discards output: the tests assert on behaviour, and a noisy
// suite hides real failures.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
