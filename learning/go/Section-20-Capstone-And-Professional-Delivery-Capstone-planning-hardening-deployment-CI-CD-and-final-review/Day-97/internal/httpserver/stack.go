package httpserver

import "runtime"

// stack renders the current goroutine's stack, bounded.
//
// Bounded because an unbounded stack in a log line is a way to fill a disk, and
// the first few frames are the ones that name the bug.
func stack() string {
	const limit = 8 << 10

	buffer := make([]byte, limit)

	written := runtime.Stack(buffer, false)

	return string(buffer[:written])
}
