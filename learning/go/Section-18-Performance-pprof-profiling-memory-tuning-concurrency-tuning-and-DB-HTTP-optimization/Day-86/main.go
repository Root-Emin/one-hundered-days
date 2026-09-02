// Day 86 - Performance: profiling with pprof.
//
// This program runs a complete profiling session end to end, the way you would
// do it by hand against a production service:
//
//  1. start the service with a pprof listener on its own port
//  2. put real load on it - an idle server profiles as nothing but epoll_wait
//  3. pull a CPU profile over the debug port WHILE the load runs
//  4. pull a heap profile to see where the allocations come from
//  5. read both with 'go tool pprof -top'
//  6. change one thing, re-measure, and compare
//
// The optimization here is deliberately unglamorous: string concatenation in a
// loop becomes a strings.Builder, a regexp compiled per item moves to package
// level, and fmt.Sprintf becomes strconv. No new algorithm. The profile said
// where to look, and the benchmark says whether it worked.
//
// Run: go run ./Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-86
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-86/internal/load"
	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-86/internal/profiling"
	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-86/internal/service"
)

// focusRegexp keeps only stacks that pass through this day's own packages.
const focusRegexp = "Day-86/internal"

const (
	loadWorkers  = 6
	loadDuration = 6 * time.Second
	profileSecs  = 4
	itemsPerCall = 400
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	profileDir, err := os.MkdirTemp("", "day86-profiles")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}

	defer func() {
		if err := os.RemoveAll(profileDir); err != nil {
			fmt.Fprintln(os.Stderr, "cleanup:", err)
		}
	}()

	section("1. pprof on its own listener")

	// Block and mutex profiles are off by default because collecting them
	// costs something. Turned on here so the goroutine/block endpoints have
	// data when you go looking.
	profiling.EnableBlockAndMutexProfiles()

	debug, err := profiling.StartDebugServer("127.0.0.1:0")
	if err != nil {
		return err
	}

	defer func() {
		if err := debug.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "close pprof:", err)
		}
	}()

	app := service.New()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	server := &http.Server{Handler: app.Routes(), ReadHeaderTimeout: 5 * time.Second}

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, "serve:", err)
		}
	}()

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintln(os.Stderr, "shutdown:", err)
		}
	}()

	appURL := "http://" + listener.Addr().String()

	fmt.Printf("  application  %s\n", appURL)
	fmt.Printf("  pprof        %s/debug/pprof/\n", debug.URL())
	fmt.Println("  the debug listener is bound to 127.0.0.1 on purpose: /debug/pprof exposes")
	fmt.Println("  your heap and lets anyone start a 30s CPU profile on the box")

	if err := listProfiles(ctx, debug.URL()); err != nil {
		return err
	}

	slowProfile := filepath.Join(profileDir, "cpu-slow.pprof")
	fastProfile := filepath.Join(profileDir, "cpu-fast.pprof")
	heapProfile := filepath.Join(profileDir, "heap-slow.pprof")

	section("2. CPU profile under load - the SLOW implementation")

	slowResult, err := profileUnderLoad(ctx, appURL, debug.URL(), "slow", slowProfile, heapProfile)
	if err != nil {
		return err
	}

	fmt.Printf("  load: %s\n\n", slowResult)

	report, err := profiling.Top(ctx, slowProfile, 10, false, "")
	if err != nil {
		return err
	}

	fmt.Println(profiling.Indent(report, "  "))

	fmt.Println()
	fmt.Println("  the top of a flat profile is almost always the runtime parking threads:")
	fmt.Println("  usleep, pthread_cond_wait, kevent. That is not your bottleneck, it is")
	fmt.Println("  what idle cores look like. Narrow to your own code with -focus, and sort by\n  cumulative time so the responsible CALLER rises to the top:")
	fmt.Println()

	focused, err := profiling.Top(ctx, slowProfile, 14, true, focusRegexp)
	if err != nil {
		return err
	}

	fmt.Println(profiling.Indent(focused, "  "))

	fmt.Println()
	fmt.Println("  now it is legible: concatstrings/concatstring2 is the cost of")
	fmt.Println("  'output += ...' in a loop, and the regexp entries are one pattern")
	fmt.Println("  being recompiled once per item")

	section("3. Heap profile - where the bytes come from")

	heapReport, err := profiling.Top(ctx, heapProfile, 10, true, "")
	if err != nil {
		return err
	}

	fmt.Println(profiling.Indent(heapReport, "  "))

	fmt.Println()
	fmt.Println("  -cum sorts by cumulative bytes, so the answer is the caller that is")
	fmt.Println("  responsible for the allocations, not the runtime function doing them")

	section("4. Same load, the FAST implementation")

	app.Reset()

	fastResult, err := profileUnderLoad(ctx, appURL, debug.URL(), "fast", fastProfile, "")
	if err != nil {
		return err
	}

	fmt.Printf("  load: %s\n\n", fastResult)

	fastReport, err := profiling.Top(ctx, fastProfile, 10, true, focusRegexp)
	if err != nil {
		return err
	}

	fmt.Println(profiling.Indent(fastReport, "  "))

	section("5. Before and after")

	fmt.Printf("  %-8s %-12s %-12s %-12s %s\n", "mode", "requests", "mean", "p99", "throughput")
	printRow("slow", slowResult)
	printRow("fast", fastResult)

	if slowResult.Requests > 0 && fastResult.Requests > 0 {
		fmt.Printf("\n  %.1fx more requests served in the same wall clock time\n",
			float64(fastResult.Requests)/float64(slowResult.Requests))
	}

	section("6. The benchmark that settles it")

	fmt.Println("  go test -run XXX -bench 'Render' -benchmem ./internal/report")
	fmt.Println()

	benchmarks, err := runBenchmarks(ctx)
	if err != nil {
		// A missing toolchain should not fail the whole demo.
		fmt.Println("  (skipped:", err, ")")
	} else {
		fmt.Println(profiling.Indent(benchmarks, "  "))
	}

	fmt.Println()
	fmt.Println("  the profile pointed at the hot function; the benchmark proved the fix.")
	fmt.Println("  neither one alone is evidence.")

	return nil
}

// profileUnderLoad starts the load, then captures the profile while it runs.
//
// The ordering is the entire technique. A profile captured before or after the
// load is a profile of an idle process.
func profileUnderLoad(ctx context.Context, appURL, debugURL, mode, cpuPath, heapPath string) (load.Result, error) {
	url := fmt.Sprintf("%s/report?items=%d&mode=%s", appURL, itemsPerCall, mode)

	resultCh := make(chan load.Result, 1)

	go func() {
		resultCh <- load.Run(ctx, url, loadWorkers, loadDuration)
	}()

	// Let the load reach a steady state before sampling: the first moments
	// include connection setup and a cold cache.
	time.Sleep(500 * time.Millisecond)

	if err := profiling.FetchProfile(ctx, debugURL, "profile", cpuPath, profileSecs); err != nil {
		return load.Result{}, err
	}

	if heapPath != "" {
		// Taken while the load is still running, so the profile shows live
		// working memory rather than a quiet post-run heap.
		if err := profiling.FetchProfile(ctx, debugURL, "heap", heapPath, 0); err != nil {
			return load.Result{}, err
		}
	}

	return <-resultCh, nil
}

func listProfiles(ctx context.Context, debugURL string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, debugURL+"/debug/pprof/", nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("list profiles: %w", err)
	}

	defer func() {
		if err := response.Body.Close(); err != nil {
			_ = err
		}
	}()

	fmt.Printf("  index responds with %s and links: allocs, block, cmdline, goroutine,\n", response.Status)
	fmt.Println("  heap, mutex, profile (CPU), threadcreate, trace")

	return nil
}

func printRow(mode string, result load.Result) {
	throughput := float64(result.Requests) / result.Duration.Seconds()

	fmt.Printf("  %-8s %-12d %-12s %-12s %.0f req/s\n",
		mode, result.Requests,
		result.Mean.Round(time.Microsecond), result.P99.Round(time.Microsecond),
		throughput)
}

func runBenchmarks(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// -benchtime=200x keeps the demo short; a real comparison runs longer and
	// goes through benchstat to check the difference is not noise.
	// The package path is relative to the module root, which is where
	// 'go run ./Section-18.../Day-86' is started from.
	packagePath := "./" + filepath.ToSlash(filepath.Join(dayDir(), "internal", "report"))

	command := exec.CommandContext(ctx, "go", "test",
		"-run", "XXX", "-bench", "Render", "-benchmem", "-benchtime=200x", packagePath)

	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go test: %v: %s", err, strings.TrimSpace(string(output)))
	}

	var kept []string

	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "Benchmark") || strings.HasPrefix(line, "cpu:") ||
			strings.HasPrefix(line, "goos:") || strings.HasPrefix(line, "goarch:") {
			kept = append(kept, line)
		}
	}

	return strings.Join(kept, "\n"), nil
}

// dayDir is this day's directory relative to the module root, so the demo can
// shell out to 'go test' no matter where it was started from.
func dayDir() string {
	return filepath.Join(
		"Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization",
		"Day-86")
}

func section(title string) {
	underline := make([]byte, len(title))

	for i := range underline {
		underline[i] = '='
	}

	fmt.Printf("\n%s\n%s\n", title, underline)
}
