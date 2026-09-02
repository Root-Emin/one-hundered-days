// Package profiling wires pprof into a service and captures profiles from it.
//
// The one rule worth stating loudly: pprof goes on its OWN listener, on a port
// that is not reachable from the internet.
//
// Importing net/http/pprof for its side effect registers /debug/pprof/* on
// http.DefaultServeMux. If your service also serves on DefaultServeMux - and
// plenty do without realising it - you have just published a public endpoint
// that dumps your heap and lets anyone start a 30-second CPU profile on your
// production box. That is both an information leak and a denial of service.
//
// So we register the handlers explicitly on a mux of our own.
package profiling

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/pprof"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	runtimepprof "runtime/pprof"
	"strconv"
	"strings"
	"time"
)

// DebugMux returns a mux with the pprof endpoints and nothing else.
func DebugMux() *http.ServeMux {
	mux := http.NewServeMux()

	// Index links the profile list; the other three need explicit routes
	// because Index only handles paths it can name.
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile) // CPU, ?seconds=N
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace) // execution tracer

	// The named runtime profiles - heap, goroutine, allocs, block, mutex -
	// are all served by Handler(name).
	for _, name := range []string{"heap", "goroutine", "allocs", "threadcreate", "block", "mutex"} {
		mux.Handle("/debug/pprof/"+name, pprof.Handler(name))
	}

	return mux
}

// DebugServer is a pprof listener you can shut down.
type DebugServer struct {
	server   *http.Server
	listener net.Listener
}

// StartDebugServer listens on addr ("127.0.0.1:0" for a random local port).
//
// Binding to 127.0.0.1 rather than :6060 means the endpoint is reachable only
// from the machine itself - you reach it through an SSH tunnel or a
// kubectl port-forward, which is exactly the friction it should have.
func StartDebugServer(addr string) (*DebugServer, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}

	server := &http.Server{Handler: DebugMux(), ReadHeaderTimeout: 5 * time.Second}

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, "pprof server:", err)
		}
	}()

	return &DebugServer{server: server, listener: listener}, nil
}

func (d *DebugServer) Addr() string {
	return d.listener.Addr().String()
}

func (d *DebugServer) URL() string {
	return "http://" + d.Addr()
}

func (d *DebugServer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return d.server.Shutdown(ctx)
}

// EnableBlockAndMutexProfiles turns on the two profiles that are off by
// default because they cost something to collect.
//
// Rate 1 samples every event, which is what you want while hunting a specific
// contention problem and not what you want running permanently.
func EnableBlockAndMutexProfiles() {
	runtime.SetBlockProfileRate(1)
	runtime.SetMutexProfileFraction(1)
}

// FetchProfile downloads a profile from a running server.
//
// This is what you actually do in production: the process keeps serving while
// you pull a profile out of it over the debug port.
func FetchProfile(ctx context.Context, baseURL, name, destination string, seconds int) error {
	query := neturl.Values{}

	if seconds > 0 {
		// The CPU profile runs for this long before the request returns.
		query.Set("seconds", strconv.Itoa(seconds))
	}

	if name == "heap" {
		// gc=1 forces a collection first, so the profile shows what is really
		// retained rather than everything not yet swept.
		query.Set("gc", "1")
	}

	url := baseURL + "/debug/pprof/" + name

	if encoded := query.Encode(); encoded != "" {
		url += "?" + encoded
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	// The CPU profile takes `seconds` to return, so the client timeout has to
	// be longer than the profile window.
	client := &http.Client{Timeout: time.Duration(seconds+30) * time.Second}

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}

	defer func() {
		if err := response.Body.Close(); err != nil {
			_ = err
		}
	}()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: %s", url, response.Status)
	}

	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return fmt.Errorf("create profile dir: %w", err)
	}

	file, err := os.Create(destination) //nolint:gosec // destination is built by this program
	if err != nil {
		return fmt.Errorf("create %s: %w", destination, err)
	}

	defer func() {
		if err := file.Close(); err != nil {
			_ = err
		}
	}()

	if _, err := io.Copy(file, response.Body); err != nil {
		return fmt.Errorf("write %s: %w", destination, err)
	}

	return nil
}

// CaptureCPU profiles a function call in-process.
//
// Use this for a batch job or a benchmark, where there is no server to pull a
// profile from. `go test -cpuprofile` does the same thing for tests.
func CaptureCPU(destination string, work func()) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return fmt.Errorf("create profile dir: %w", err)
	}

	file, err := os.Create(destination) //nolint:gosec // destination is built by this program
	if err != nil {
		return fmt.Errorf("create %s: %w", destination, err)
	}

	defer func() {
		if err := file.Close(); err != nil {
			_ = err
		}
	}()

	if err := runtimepprof.StartCPUProfile(file); err != nil {
		return fmt.Errorf("start cpu profile: %w", err)
	}

	defer runtimepprof.StopCPUProfile()

	work()

	return nil
}

// CaptureHeap writes a heap profile after forcing a GC.
func CaptureHeap(destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return fmt.Errorf("create profile dir: %w", err)
	}

	file, err := os.Create(destination) //nolint:gosec // destination is built by this program
	if err != nil {
		return fmt.Errorf("create %s: %w", destination, err)
	}

	defer func() {
		if err := file.Close(); err != nil {
			_ = err
		}
	}()

	// Without this, the profile counts garbage that simply has not been
	// collected yet, and every allocation looks like a leak.
	runtime.GC()

	if err := runtimepprof.WriteHeapProfile(file); err != nil {
		return fmt.Errorf("write heap profile: %w", err)
	}

	return nil
}

// Top runs `go tool pprof -top` and returns the report.
//
// Reading a profile with the tool - rather than eyeballing the binary format -
// is the whole point: pprof aggregates samples by function and sorts them.
//
// focus is a regexp; when set, pprof keeps only samples whose stack passes
// through a matching function. This is not optional in practice. A flat top on
// a multi-core machine is dominated by runtime.usleep, pthread_cond_wait and
// kevent - the scheduler parking idle threads - and your own code is somewhere
// on page two. -focus on your own package is how you find it.
func Top(ctx context.Context, profilePath string, nodes int, cumulative bool, focus string) (string, error) {
	args := []string{"tool", "pprof", "-top", fmt.Sprintf("-nodecount=%d", nodes)}

	if cumulative {
		// -cum sorts by cumulative time: the caller that is responsible for
		// the work, not the leaf that happens to execute it.
		args = append(args, "-cum")
	}

	if focus != "" {
		args = append(args, "-focus="+focus)
	}

	args = append(args, profilePath)

	command := exec.CommandContext(ctx, "go", args...)

	output, err := command.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("go tool pprof: %w", err)
	}

	return string(output), nil
}

// Indent shifts a pprof report so it reads as a quoted block.
func Indent(text, prefix string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")

	for i, line := range lines {
		lines[i] = prefix + line
	}

	return strings.Join(lines, "\n")
}
