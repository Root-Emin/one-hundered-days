// Package httpclient is the network half of Day 89: connection reuse and
// timeouts.
//
// Two failure modes, both common, both invisible until they are not:
//
//	NO KEEP-ALIVE  every request pays a TCP handshake (and a TLS handshake, and
//	               possibly a DNS lookup). For a chatty service that is more
//	               time than the request itself.
//	NO TIMEOUT     http.DefaultClient has NO timeout. A dependency that hangs
//	               holds your goroutine, its memory and its connection until
//	               the TCP stack gives up - which can be minutes. Enough of
//	               those and the service is out of workers.
package httpclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
	"time"
)

// Timeouts are the four that matter, and they are not the same knob.
//
// A single client.Timeout covers everything including reading the body, which
// is wrong for a streaming endpoint. The layered ones below fail fast on a
// dead host without capping a legitimately long download.
type Timeouts struct {
	// Dial: how long to wait for the TCP connection. Small - a host that is
	// up answers in milliseconds.
	Dial time.Duration

	// TLSHandshake: the certificate exchange, after the connection is up.
	TLSHandshake time.Duration

	// ResponseHeader: how long the server may think before it starts
	// answering. This is the one that catches a stuck dependency.
	ResponseHeader time.Duration

	// Total: the whole request including the body. Set it generously, or not
	// at all for streaming endpoints - but then ResponseHeader must be set.
	Total time.Duration
}

// DefaultTimeouts are sane starting values for a service-to-service call.
func DefaultTimeouts() Timeouts {
	return Timeouts{
		Dial:           2 * time.Second,
		TLSHandshake:   2 * time.Second,
		ResponseHeader: 3 * time.Second,
		Total:          10 * time.Second,
	}
}

// Config describes a client.
type Config struct {
	Timeouts Timeouts

	// DisableKeepAlives turns connection reuse off. Never do this in
	// production; it exists here so the demo can measure what it costs.
	DisableKeepAlives bool

	// MaxIdleConnsPerHost is the setting people miss. The default is 2, so a
	// service making 50 concurrent calls to ONE host keeps 2 connections and
	// closes the other 48 after each request - re-handshaking every time.
	// For a service that talks to a handful of hosts, set it to your
	// concurrency.
	MaxIdleConnsPerHost int

	// IdleConnTimeout: how long an unused connection is kept. Longer means
	// more reuse; too long and you hold connections a load balancer has
	// already dropped, producing sporadic EOFs.
	IdleConnTimeout time.Duration
}

func DefaultConfig() Config {
	return Config{
		Timeouts:            DefaultTimeouts(),
		MaxIdleConnsPerHost: 64,
		IdleConnTimeout:     90 * time.Second,
	}
}

// New builds an http.Client with an explicit transport.
//
// Never use http.DefaultClient for an outbound call: it has no timeout, and
// its transport is shared with every library in the process, so tuning it
// changes behaviour you cannot see.
func New(config Config) *http.Client {
	if config.MaxIdleConnsPerHost <= 0 {
		config.MaxIdleConnsPerHost = 64
	}

	if config.IdleConnTimeout <= 0 {
		config.IdleConnTimeout = 90 * time.Second
	}

	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: config.Timeouts.Dial,
			// KeepAlive here is TCP keep-alive probes on an idle connection -
			// a different thing from HTTP keep-alive, and worth not confusing.
			KeepAlive: 30 * time.Second,
		}).DialContext,

		TLSHandshakeTimeout:   config.Timeouts.TLSHandshake,
		ResponseHeaderTimeout: config.Timeouts.ResponseHeader,
		ExpectContinueTimeout: time.Second,

		DisableKeepAlives:   config.DisableKeepAlives,
		MaxIdleConns:        256,
		MaxIdleConnsPerHost: config.MaxIdleConnsPerHost,
		IdleConnTimeout:     config.IdleConnTimeout,

		ForceAttemptHTTP2: true,

		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}

	return &http.Client{
		Transport: transport,
		Timeout:   config.Timeouts.Total,
	}
}

// Stats counts what actually happened at the connection level.
type Stats struct {
	Requests    atomic.Int64
	NewConns    atomic.Int64
	ReusedConns atomic.Int64
	DNSLookups  atomic.Int64
}

func (s *Stats) ReuseRate() float64 {
	total := s.NewConns.Load() + s.ReusedConns.Load()

	if total == 0 {
		return 0
	}

	return float64(s.ReusedConns.Load()) / float64(total)
}

// Trace instruments one request with httptrace.
//
// This is how you answer "is keep-alive actually working?" without guessing:
// GotConn reports whether the connection was reused, per request.
func Trace(ctx context.Context, stats *Stats) context.Context {
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			if info.Reused {
				stats.ReusedConns.Add(1)

				return
			}

			stats.NewConns.Add(1)
		},
		DNSStart: func(httptrace.DNSStartInfo) {
			stats.DNSLookups.Add(1)
		},
	})
}

// Get performs a request and drains the body.
//
// Draining and closing is not optional: an undrained body means the connection
// cannot go back into the idle pool, and keep-alive silently stops working
// even though it is switched on. This is the most common way connection reuse
// is lost in real code.
func Get(ctx context.Context, client *http.Client, url string, stats *Stats) ([]byte, error) {
	if stats != nil {
		ctx = Trace(ctx, stats)
		stats.Requests.Add(1)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}

	defer func() {
		if err := response.Body.Close(); err != nil {
			_ = err
		}
	}()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if response.StatusCode != http.StatusOK {
		return body, fmt.Errorf("GET %s: %s", url, response.Status)
	}

	return body, nil
}

// GetWithoutDraining shows the anti-pattern: the body is closed but never
// read, so the connection is discarded instead of reused.
func GetWithoutDraining(ctx context.Context, client *http.Client, url string, stats *Stats) error {
	if stats != nil {
		ctx = Trace(ctx, stats)
		stats.Requests.Add(1)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}

	// Closing without reading: the remaining bytes are still in flight, so the
	// transport cannot put this connection back in the pool.
	return response.Body.Close()
}
