// Command server exposes the notes MVP over gRPC and HTTP simultaneously.
package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	notesv1 "example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-65/gen/notes/v1"
	"example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-65/internal/notes"
	"example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-65/internal/transport/grpcapi"
	"example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-65/internal/transport/restapi"
)

/*
Day 65 - gRPC & Protocol Buffers: Practice

Section 13 capstone. See README.md for the codegen steps, the endpoint parity
table and the local development commands.

Tasks covered:

 1. The MVP speaks gRPC and HTTP at once, from one process
 2. Both transports call the same internal/notes service - no duplicated rules
 3. gRPC interceptors log every RPC and reject calls without valid credentials;
    the HTTP middleware does the same with the same Authenticator
 4. README.md documents regeneration and local calls

Run:

	go run ./cmd/server        # gRPC :9090, HTTP :8080
	go run ./cmd/client        # drives both transports and compares them

Demo tokens: ada-token (user u-1), alan-token (user u-2)
*/

// authenticate is the single identity source shared by both transports. In a
// real service this verifies a JWT (Day 52).
func authenticate(token string) (string, bool) {
	users := map[string]string{
		"ada-token":  "u-1",
		"alan-token": "u-2",
	}

	userID, found := users[token]

	return userID, found
}

func main() {
	log.SetFlags(log.Ltime | log.Lmsgprefix)
	log.SetPrefix("day65 ")

	if err := run(); err != nil {
		log.Fatalf("%v", err)
	}
}

func run() error {
	// One service instance. Both servers below hold a pointer to it, so a
	// note created over gRPC is immediately visible over HTTP.
	service := notes.NewService(notes.SystemClock{})

	grpcServer := grpc.NewServer(grpc.ChainUnaryInterceptor(
		grpcapi.RecoveryInterceptor(),
		grpcapi.LoggingInterceptor(),
		grpcapi.AuthInterceptor(authenticate),
	))

	notesv1.RegisterNotesServiceServer(grpcServer, grpcapi.NewServer(service))
	reflection.Register(grpcServer)

	grpcAddress := envOr("GRPC_ADDR", ":9090")

	listener, err := net.Listen("tcp", grpcAddress)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              envOr("HTTP_ADDR", ":8080"),
		Handler:           restapi.NewHandler(service, authenticate).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 2)

	go func() {
		log.Printf("gRPC listening on %s", grpcAddress)

		if err := grpcServer.Serve(listener); err != nil {
			serverErrors <- err
		}
	}()

	go func() {
		log.Printf("HTTP listening on %s", httpServer.Addr)

		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		return err
	case received := <-shutdown:
		log.Printf("shutdown signal: %s", received)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("http shutdown: %v", err)
	}

	stopped := make(chan struct{})

	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
		log.Printf("stopped cleanly")
	case <-ctx.Done():
		grpcServer.Stop()
	}

	return nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
