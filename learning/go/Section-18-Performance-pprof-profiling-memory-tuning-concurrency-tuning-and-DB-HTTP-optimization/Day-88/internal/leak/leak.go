// Package leak is about goroutines that never exit.
//
// A leaked goroutine is not just memory - it is a live stack (8 KB and up),
// everything that stack references kept alive with it, and one more entry the
// scheduler walks. A handler that leaks one goroutine per request leaks
// thousands per hour, and the symptom is a service that gets slower and
// fatter for no visible reason.
//
// The three leaks below cover almost every real one:
//
//  1. a send with no receiver     - the classic timeout leak
//  2. a range over a channel that is never closed
//  3. a worker that ignores ctx.Done()
//
// Each has a Leaky and a Fixed version, and leak_test.go asserts that the
// leaky one leaks and the fixed one does not.
package leak

import (
	"context"
	"fmt"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"
)

//
// LEAK 1: THE SEND WITH NO RECEIVER
//

// LeakyRequest is the single most common goroutine leak in Go.
//
// The goroutine sends on an UNBUFFERED channel. When the caller times out and
// returns, nobody ever receives - so that send blocks forever, and the
// goroutine (plus everything its stack references) is immortal.
func LeakyRequest(ctx context.Context, work time.Duration) (string, error) {
	result := make(chan string) // unbuffered: the bug

	go func() {
		time.Sleep(work)

		result <- "done" // blocks forever if the caller has already left
	}()

	select {
	case value := <-result:
		return value, nil

	case <-ctx.Done():
		return "", fmt.Errorf("request: %w", ctx.Err())
	}
}

// FixedRequest changes one character: a buffer of 1.
//
// Now the send always completes, whether anyone is listening or not, and the
// goroutine returns. The buffered value is garbage collected with the channel.
//
// The alternative fix is to select on ctx.Done() in the sender too. Buffering
// is simpler, and simpler is what you want in the code that runs per request.
func FixedRequest(ctx context.Context, work time.Duration) (string, error) {
	result := make(chan string, 1) // room for the value even with no receiver

	go func() {
		time.Sleep(work)

		result <- "done"
	}()

	select {
	case value := <-result:
		return value, nil

	case <-ctx.Done():
		return "", fmt.Errorf("request: %w", ctx.Err())
	}
}

//
// LEAK 2: THE CHANNEL NOBODY CLOSES
//

// LeakyConsumer ranges over a channel the producer never closes. When the
// producer stops sending, the range blocks forever instead of ending.
func LeakyConsumer(values []int) chan int {
	jobs := make(chan int)

	go func() {
		for _, value := range values {
			jobs <- value
		}
		// No close(jobs): every consumer ranging over this blocks forever.
	}()

	go func() {
		total := 0

		for value := range jobs { // never ends
			total += value
		}
	}()

	return jobs
}

// FixedConsumer closes the channel when the work is done.
//
// The rule: the sender closes, never the receiver, and only one sender may
// close. close() is a broadcast that says "no more values" - it is how a range
// loop is supposed to end.
func FixedConsumer(ctx context.Context, values []int) <-chan int {
	out := make(chan int)

	go func() {
		defer close(out) // the deferred close is the whole fix

		for _, value := range values {
			select {
			case out <- value:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

//
// LEAK 3: THE WORKER THAT IGNORES CANCELLATION
//

// LeakyWorker loops forever on a ticker, with no way out. Cancelling the
// context does nothing, because nothing reads it. This is what turns a clean
// shutdown into a SIGKILL thirty seconds later.
func LeakyWorker(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		// Not even a Stop: the ticker's runtime goroutine stays too.

		for range ticker.C {
			_ = "poll something"
		}
	}()
}

// FixedWorker selects on ctx.Done() and stops its ticker on the way out.
func FixedWorker(ctx context.Context, interval time.Duration) <-chan struct{} {
	done := make(chan struct{})

	go func() {
		defer close(done) // lets a caller wait for the worker to actually stop

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return

			case <-ticker.C:
				_ = "poll something"
			}
		}
	}()

	return done
}

//
// DETECTION
//

// Count is runtime.NumGoroutine, which is the cheapest possible leak detector
// and worth exporting from any service as a metric. A count that climbs and
// never comes back down is a leak, and no other symptom is needed.
func Count() int {
	return runtime.NumGoroutine()
}

// Settle waits for the goroutine count to drop to want, or gives up.
//
// Polling rather than sleeping a fixed amount: goroutines exit asynchronously,
// so an assertion made immediately after a cancel is a flaky test.
func Settle(want int, timeout time.Duration) (int, bool) {
	deadline := time.Now().Add(timeout)

	for {
		current := runtime.NumGoroutine()

		if current <= want {
			return current, true
		}

		if time.Now().After(deadline) {
			return current, false
		}

		// Give the scheduler a chance to run the goroutines that are exiting.
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
}

// Profile returns the goroutine profile as text: every live goroutine grouped
// by stack, with a count.
//
// This is the same data as /debug/pprof/goroutine?debug=1. When the count is
// climbing, this tells you WHICH stack is accumulating - and the answer is
// nearly always a channel operation on line one.
func Profile(debug int) (string, error) {
	profile := pprof.Lookup("goroutine")
	if profile == nil {
		return "", fmt.Errorf("goroutine profile is not registered")
	}

	var builder strings.Builder

	if err := profile.WriteTo(&builder, debug); err != nil {
		return "", fmt.Errorf("write goroutine profile: %w", err)
	}

	return builder.String(), nil
}

// TopStacks summarises the profile: the busiest stacks, with their counts.
//
// The debug=1 text format is one block per distinct stack:
//
//	50 @ 0x104c4e26c 0x104c51610 ...
//	#	0x104ca3663	....LeakyRequest.func1+0x23	/path/leak.go:45
//	#	...
//
// The leading number is how many goroutines share that stack - which is the
// only number that matters when hunting a leak. The frame that names the bug
// is the first one in your own code, so runtime and stdlib frames are skipped.
func TopStacks(limit int) (string, error) {
	raw, err := Profile(1)
	if err != nil {
		return "", err
	}

	var out []string

	for _, block := range strings.Split(raw, "\n\n") {
		lines := strings.Split(strings.TrimSpace(block), "\n")

		count, frames := parseBlock(lines)
		if count == 0 {
			continue
		}

		frame := "(no user frame)"

		for _, candidate := range frames {
			if isUserFrame(candidate.function) {
				frame = fmt.Sprintf("%s\n%s%s",
					shorten(candidate.function), strings.Repeat(" ", 18), shorten(candidate.location))

				break
			}
		}

		noun := "goroutines"

		if count == 1 {
			noun = "goroutine "
		}

		out = append(out, fmt.Sprintf("%5d %s  %s", count, noun, frame))

		if len(out) >= limit {
			break
		}
	}

	return strings.Join(out, "\n"), nil
}

type frame struct {
	function string
	location string
}

// parseBlock pulls the goroutine count and the stack frames out of one block.
func parseBlock(lines []string) (int, []frame) {
	var (
		count  int
		frames []frame
	)

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "#") {
			// #\t0xADDR\tpkg.Func+0x23\t/path/file.go:45
			fields := strings.Split(line, "\t")

			if len(fields) < 4 {
				continue
			}

			function := fields[2]

			if index := strings.LastIndex(function, "+"); index > 0 {
				function = function[:index]
			}

			frames = append(frames, frame{function: function, location: fields[3]})

			continue
		}

		if index := strings.Index(line, " @ "); index > 0 && count == 0 {
			parsed, err := strconv.Atoi(strings.TrimSpace(line[:index]))
			if err == nil {
				count = parsed
			}
		}
	}

	return count, frames
}

// isUserFrame skips the runtime and standard library, which are never the
// answer - the bug is in the frame that belongs to the program.
func isUserFrame(function string) bool {
	for _, prefix := range []string{"runtime.", "runtime/pprof.", "internal/", "sync.", "time.", "reflect."} {
		if strings.HasPrefix(function, prefix) {
			return false
		}
	}

	return strings.Contains(function, ".")
}

// shorten trims the module and section prefix so a stack line fits in a
// terminal. The interesting part of a frame is always its tail.
func shorten(value string) string {
	if index := strings.Index(value, "/internal/"); index > 0 {
		return value[index+1:]
	}

	if index := strings.LastIndex(value, "/"); index > 0 {
		return value[index+1:]
	}

	return value
}
