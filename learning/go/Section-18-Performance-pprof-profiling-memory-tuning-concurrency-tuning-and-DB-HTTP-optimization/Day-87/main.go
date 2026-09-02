// Day 87 - Performance: memory and allocation tuning.
//
// Day 86 found the hot function. Today is about the allocations inside it, in
// the order you should actually apply the fixes:
//
//  1. preallocate     make([]byte, 0, n) instead of growing by doubling
//  2. build, not concat   strings.Builder / append instead of s += ...
//  3. let the caller own the buffer   AppendX(dst, ...) - zero allocations
//  4. sync.Pool       only when a profile shows churn you cannot remove
//
// The order matters. Steps 1-3 are local, obvious and always safe. A pool is
// shared mutable state with a reset you can forget, and its payoff depends
// entirely on the size and lifetime of the objects - so it comes last, with a
// benchmark behind it.
//
// Run: go run ./Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-87
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-87/internal/encode"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	records := encode.Sample(100, 7)

	section("0. Same output, five ways")

	baseline := encode.EncodeNaive(records)

	fmt.Printf("  %d records -> %d bytes\n", len(records), len(baseline))
	fmt.Printf("  first line: %s\n", strings.SplitN(string(baseline), "\n", 2)[0])

	for name, encoded := range map[string][]byte{
		"EncodePrealloc": encode.EncodePrealloc(records),
		"AppendEncode":   encode.AppendEncode(nil, records),
		"BuildString":    []byte(encode.BuildString(records)),
		"Encoder.Encode": encode.NewEncoder().Encode(records),
	} {
		if !bytes.Equal(encoded, baseline) {
			return fmt.Errorf("%s produced different output", name)
		}
	}

	fmt.Println("  all five implementations produce byte-identical output")

	section("1. allocs/op is the number to watch")

	// testing.Benchmark runs a benchmark from ordinary code and hands back the
	// same numbers 'go test -bench' prints, including AllocsPerOp.
	results := []struct {
		name   string
		result testing.BenchmarkResult
	}{
		{"EncodeNaive (fmt + concat)", testing.Benchmark(func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				sink = encode.EncodeNaive(records)
			}
		})},
		{"AppendEncode (no prealloc)", testing.Benchmark(func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				sink = encode.AppendEncode(nil, records)
			}
		})},
		{"EncodePrealloc", testing.Benchmark(func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				sink = encode.EncodePrealloc(records)
			}
		})},
		{"AppendEncode (reused buffer)", testing.Benchmark(func(b *testing.B) {
			buffer := make([]byte, 0, encode.EstimateSize(records))

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				buffer = encode.AppendEncode(buffer[:0], records)
			}

			sink = buffer
		})},
		{"Encoder.WriteTo (sync.Pool)", testing.Benchmark(func(b *testing.B) {
			encoder := encode.NewEncoder()

			var writer counter

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				if _, err := encoder.WriteTo(&writer, records); err != nil {
					b.Fatal(err)
				}
			}
		})},
	}

	fmt.Printf("  %-30s %12s %12s %10s\n", "implementation", "ns/op", "B/op", "allocs/op")

	var baselineNs float64

	for i, entry := range results {
		result := entry.result

		nsPerOp := float64(result.NsPerOp())

		if i == 0 {
			baselineNs = nsPerOp
		}

		speedup := ""

		if i > 0 && nsPerOp > 0 {
			speedup = fmt.Sprintf("  (%.1fx)", baselineNs/nsPerOp)
		}

		fmt.Printf("  %-30s %12d %12d %10d%s\n",
			entry.name, result.NsPerOp(), result.AllocedBytesPerOp(), result.AllocsPerOp(), speedup)
	}

	fmt.Println()
	fmt.Println("  1317 allocations become 1 by sizing the buffer, and 1 becomes 0 by")
	fmt.Println("  letting the caller own it. The pool only matters once the buffer")
	fmt.Println("  cannot be caller-owned - concurrent handlers, say.")

	section("2. What that does to the garbage collector")

	naiveStats := measureGC(func() {
		for i := 0; i < 2000; i++ {
			sink = encode.EncodeNaive(records)
		}
	})

	pooledStats := measureGC(func() {
		encoder := encode.NewEncoder()

		var writer counter

		for i := 0; i < 2000; i++ {
			if _, err := encoder.WriteTo(&writer, records); err != nil {
				panic(err)
			}
		}
	})

	fmt.Printf("  %-14s %14s %12s %12s\n", "", "total alloc", "GC cycles", "wall")
	fmt.Printf("  %-14s %11.1f MB %12d %12s\n", "naive",
		naiveStats.bytes, naiveStats.collections, naiveStats.elapsed.Round(time.Millisecond))
	fmt.Printf("  %-14s %11.1f MB %12d %12s\n", "pooled",
		pooledStats.bytes, pooledStats.collections, pooledStats.elapsed.Round(time.Millisecond))

	fmt.Println()
	fmt.Println("  fewer allocated bytes means fewer GC cycles, and every cycle is CPU")
	fmt.Println("  taken from serving requests. This is why allocations cost even though")
	fmt.Println("  Go's collector is fast.")

	section("3. Where a pool does NOT pay")

	small := encode.Sample(1, 7)

	withoutPool := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			sink = encode.EncodePrealloc(small)
		}
	})

	withPool := testing.Benchmark(func(b *testing.B) {
		encoder := encode.NewEncoder()

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			sink = encoder.Encode(small)
		}
	})

	fmt.Printf("  one small record, no pool: %6d ns/op  %d allocs/op\n",
		withoutPool.NsPerOp(), withoutPool.AllocsPerOp())
	fmt.Printf("  one small record, pooled : %6d ns/op  %d allocs/op\n",
		withPool.NsPerOp(), withPool.AllocsPerOp())
	fmt.Println("  the pool is not faster here: for a small buffer, allocation is a")
	fmt.Println("  pointer bump and the pool's own bookkeeping costs more than it saves.")
	fmt.Println("  'use sync.Pool' is not advice - it is a hypothesis you benchmark.")

	section("4. Why the allocation happened: escape analysis")

	output, err := escapeAnalysis(context.Background())
	if err != nil {
		fmt.Println("  (skipped:", err, ")")
	} else {
		fmt.Println(output)
	}

	fmt.Println()
	fmt.Println("  -m tells you what the COMPILER decided at the definition. It is not")
	fmt.Println("  the last word: escape analysis runs after inlining, per call site, so")
	fmt.Println("  a 'escapes to heap' function can allocate nothing when the caller")
	fmt.Println("  discards the result. internal/escape asserts the measured behaviour.")

	section("5. The rules that survived measurement")

	for _, rule := range []string{
		"size the buffer when you know the size - one allocation instead of log2(n)",
		"append into a caller-provided buffer (dst []byte) - the standard library's own API shape",
		"strings.Builder when the result must be a string; it hands over its buffer without a copy",
		"strconv.Append* instead of fmt.Sprintf in hot paths - no format parsing, no reflection",
		"sync.Pool last, for large short-lived buffers under concurrency, with a benchmark",
		"always reset a pooled object on the way out, never trust the caller to have done it",
		"never return a pooled buffer, or a value that points into one",
		"drop oversized buffers instead of pooling them, or one big request raises the floor forever",
	} {
		fmt.Printf("  - %s\n", rule)
	}

	return nil
}

var sink []byte

type counter struct{ n int }

func (c *counter) Write(p []byte) (int, error) {
	c.n += len(p)

	return len(p), nil
}

type gcStats struct {
	bytes       float64
	collections uint32
	elapsed     time.Duration
}

// measureGC reports what a workload cost the runtime.
//
// TotalAlloc is cumulative bytes ever allocated - the number that drives GC
// frequency. NumGC is how many collections that triggered. Both are what
// "allocations cost CPU" actually means.
func measureGC(work func()) gcStats {
	// Start from a clean, collected heap so the numbers describe the workload
	// and not whatever the previous phase left behind.
	runtime.GC()

	var before, after runtime.MemStats

	runtime.ReadMemStats(&before)

	start := time.Now()

	work()

	elapsed := time.Since(start)

	runtime.ReadMemStats(&after)

	return gcStats{
		bytes:       float64(after.TotalAlloc-before.TotalAlloc) / (1 << 20),
		collections: after.NumGC - before.NumGC,
		elapsed:     elapsed,
	}
}

func escapeAnalysis(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	packagePath := "./" + filepath.ToSlash(filepath.Join(dayDir(), "internal", "escape"))

	command := exec.CommandContext(ctx, "go", "build", "-gcflags=-m", packagePath)

	// -m writes to stderr.
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go build -gcflags=-m: %v", err)
	}

	var kept []string

	for _, line := range strings.Split(string(output), "\n") {
		if !strings.Contains(line, "escapes to heap") && !strings.Contains(line, "does not escape") {
			continue
		}

		// Trim the long section directory from the front of each line.
		if index := strings.Index(line, "escape.go"); index >= 0 {
			line = line[index:]
		}

		kept = append(kept, "  "+line)
	}

	return strings.Join(kept, "\n"), nil
}

func dayDir() string {
	return filepath.Join(
		"Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization",
		"Day-87")
}

func section(title string) {
	underline := make([]byte, len(title))

	for i := range underline {
		underline[i] = '='
	}

	fmt.Printf("\n%s\n%s\n", title, underline)
}
