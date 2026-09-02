package httpclient_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-89/internal/httpclient"
)

func echoServer(t *testing.T) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(strings.Repeat("x", 256))); err != nil {
			t.Errorf("write: %v", err)
		}
	}))

	t.Cleanup(server.Close)

	return server
}

// The property keep-alive exists for: one connection, many requests.
func TestKeepAliveReusesTheConnection(t *testing.T) {
	server := echoServer(t)

	client := httpclient.New(httpclient.DefaultConfig())
	stats := &httpclient.Stats{}

	for i := 0; i < 20; i++ {
		if _, err := httpclient.Get(t.Context(), client, server.URL, stats); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}

	if stats.NewConns.Load() != 1 {
		t.Errorf("new connections = %d, want 1", stats.NewConns.Load())
	}

	if stats.ReusedConns.Load() != 19 {
		t.Errorf("reused connections = %d, want 19", stats.ReusedConns.Load())
	}

	if rate := stats.ReuseRate(); rate < 0.9 {
		t.Errorf("reuse rate = %.2f, want > 0.9", rate)
	}
}

func TestDisableKeepAlivesOpensAConnectionPerRequest(t *testing.T) {
	server := echoServer(t)

	config := httpclient.DefaultConfig()
	config.DisableKeepAlives = true

	client := httpclient.New(config)
	stats := &httpclient.Stats{}

	const requests = 10

	for i := 0; i < requests; i++ {
		if _, err := httpclient.Get(t.Context(), client, server.URL, stats); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}

	if stats.NewConns.Load() != requests {
		t.Errorf("new connections = %d, want %d", stats.NewConns.Load(), requests)
	}

	if stats.ReusedConns.Load() != 0 {
		t.Errorf("reused connections = %d, want 0", stats.ReusedConns.Load())
	}
}

// The trap: keep-alive is on, but an undrained body means the connection
// cannot go back in the pool. This is the most common way reuse is lost.
func TestUndrainedBodyDefeatsKeepAlive(t *testing.T) {
	server := echoServer(t)

	client := httpclient.New(httpclient.DefaultConfig())
	stats := &httpclient.Stats{}

	const requests = 10

	for i := 0; i < requests; i++ {
		if err := httpclient.GetWithoutDraining(t.Context(), client, server.URL, stats); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}

	if stats.ReusedConns.Load() > 1 {
		t.Errorf("reused %d connections despite not draining the bodies", stats.ReusedConns.Load())
	}
}

// ResponseHeaderTimeout is the one that catches a dependency that accepts the
// connection and then thinks forever.
func TestResponseHeaderTimeoutFiresOnAHungServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(10 * time.Second):
		case <-r.Context().Done():
		}
	}))

	t.Cleanup(server.Close)

	config := httpclient.DefaultConfig()
	config.Timeouts.ResponseHeader = 100 * time.Millisecond

	client := httpclient.New(config)

	start := time.Now()

	_, err := httpclient.Get(t.Context(), client, server.URL, nil)

	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout")
	}

	if elapsed > 2*time.Second {
		t.Errorf("took %s - the response header timeout did not fire", elapsed)
	}

	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error = %v, want a timeout", err)
	}
}

// A caller's deadline must win when it is shorter than the client's.
func TestCallerContextDeadlineWins(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(10 * time.Second):
		case <-r.Context().Done():
		}
	}))

	t.Cleanup(server.Close)

	client := httpclient.New(httpclient.DefaultConfig()) // 10s total timeout

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()

	_, err := httpclient.Get(ctx, client, server.URL, nil)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want DeadlineExceeded", err)
	}

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %s, want ~50ms", elapsed)
	}
}

// The dial timeout is what fails fast when the host is simply not there.
func TestDialTimeoutOnAnUnroutableHost(t *testing.T) {
	config := httpclient.DefaultConfig()
	config.Timeouts.Dial = 100 * time.Millisecond
	config.Timeouts.Total = 5 * time.Second

	client := httpclient.New(config)

	start := time.Now()

	// 203.0.113.0/24 is TEST-NET-3: reserved for documentation, guaranteed
	// not to route anywhere.
	_, err := httpclient.Get(t.Context(), client, "http://203.0.113.1:81/", nil)

	if err == nil {
		t.Fatal("expected a dial failure")
	}

	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("took %s - the dial timeout did not fire", elapsed)
	}
}

func TestNonOKStatusIsAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))

	t.Cleanup(server.Close)

	client := httpclient.New(httpclient.DefaultConfig())

	body, err := httpclient.Get(t.Context(), client, server.URL, nil)
	if err == nil {
		t.Fatal("expected an error for 503")
	}

	// The body still comes back: an error response usually explains itself.
	if !strings.Contains(string(body), "nope") {
		t.Errorf("body = %q, want the server's message", body)
	}
}

func TestDefaultConfigSetsEveryTimeout(t *testing.T) {
	timeouts := httpclient.DefaultTimeouts()

	if timeouts.Dial == 0 || timeouts.TLSHandshake == 0 ||
		timeouts.ResponseHeader == 0 || timeouts.Total == 0 {
		t.Errorf("a zero timeout means no timeout: %+v", timeouts)
	}

	if timeouts.ResponseHeader >= timeouts.Total {
		t.Errorf("ResponseHeader (%s) should be shorter than the total (%s)",
			timeouts.ResponseHeader, timeouts.Total)
	}
}
