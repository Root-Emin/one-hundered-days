// Package embedded runs a NATS server inside the process.
//
// nats-server is a Go library as well as a binary, so a demo and a test suite
// can start a real broker - real protocol, real JetStream, real acks - with
// nothing installed. In production this is a separate process; for learning
// and for tests it removes every excuse not to run the code.
package embedded

import (
	"fmt"
	"os"
	"time"

	server "github.com/nats-io/nats-server/v2/server"
)

type Server struct {
	server    *server.Server
	storeDir  string
	ClientURL string
}

// Start launches a JetStream-enabled server on a random port.
//
// A random port matters for tests: a fixed one collides the moment two
// packages run in parallel.
func Start() (*Server, error) {
	storeDir, err := os.MkdirTemp("", "nats-jetstream-*")
	if err != nil {
		return nil, fmt.Errorf("create store dir: %w", err)
	}

	options := &server.Options{
		Host:      "127.0.0.1",
		Port:      -1, // -1 means "pick a free port"
		JetStream: true,
		StoreDir:  storeDir,
		NoLog:     true,
		NoSigs:    true,
	}

	instance, err := server.NewServer(options)
	if err != nil {
		removeDir(storeDir)

		return nil, fmt.Errorf("create server: %w", err)
	}

	go instance.Start()

	if !instance.ReadyForConnections(10 * time.Second) {
		instance.Shutdown()
		removeDir(storeDir)

		return nil, fmt.Errorf("server did not become ready")
	}

	return &Server{server: instance, storeDir: storeDir, ClientURL: instance.ClientURL()}, nil
}

// Stop shuts the server down and removes its storage. Skipping the cleanup
// leaves JetStream files behind on every test run.
func (s *Server) Stop() {
	if s == nil || s.server == nil {
		return
	}

	s.server.Shutdown()
	s.server.WaitForShutdown()

	removeDir(s.storeDir)
}

func removeDir(path string) {
	if path == "" {
		return
	}

	if err := os.RemoveAll(path); err != nil {
		fmt.Fprintf(os.Stderr, "remove %s: %v\n", path, err)
	}
}
