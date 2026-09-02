// Command server runs the inventory service over gRPC and HTTP at the same
// time, backed by one shared service instance.
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

	inventoryv1 "example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-63/gen/inventory/v1"
	"example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-63/internal/grpcadapter"
	"example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-63/internal/httpadapter"
	"example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-63/internal/inventory"
)

/*
Day 63 - gRPC & Protocol Buffers: Client and Server Implementation

Tasks covered:

 1. A gRPC server registering the generated service implementation
 2. A client that dials, calls every RPC and tears the connection down
    (cmd/client)
 3. Domain errors mapped to gRPC status codes (internal/grpcadapter)
 4. One service layer shared by gRPC and HTTP (internal/inventory)

Run:

	go run ./cmd/server            # gRPC on :9090, HTTP on :8080
	go run ./cmd/client            # drives every RPC against it

	grpcurl -plaintext localhost:9090 list          # reflection is enabled
	grpcurl -plaintext -d '{"sku":"KB-01"}' localhost:9090 inventory.v1.InventoryService/GetItem

	curl localhost:8080/items
	curl -XPOST localhost:8080/items -d '{"sku":"kb-01","name":"Keyboard","quantity":5}'

Environment variables:

	GRPC_ADDR   gRPC listen address. Default: :9090
	HTTP_ADDR   HTTP listen address. Default: :8080

Test:

	go test ./...
*/

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day63: ")

	if err := run(); err != nil {
		log.Fatalf("%v", err)
	}
}

func run() error {
	// One service. Two transports. This is the whole lesson.
	service := inventory.NewService(inventory.SystemClock{})

	if err := seed(service); err != nil {
		return err
	}

	grpcServer := grpc.NewServer()

	inventoryv1.RegisterInventoryServiceServer(grpcServer, grpcadapter.NewServer(service))

	// Reflection lets grpcurl and similar tools discover the API without a
	// copy of the .proto. Convenient in development; usually disabled in
	// production, where it advertises the whole surface to anyone who asks.
	reflection.Register(grpcServer)

	grpcAddress := envOr("GRPC_ADDR", ":9090")

	listener, err := net.Listen("tcp", grpcAddress)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              envOr("HTTP_ADDR", ":8080"),
		Handler:           httpadapter.NewHandler(service).Routes(),
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

	// GracefulStop refuses new RPCs and waits for the in-flight ones, the
	// gRPC equivalent of http.Server.Shutdown.
	stopped := make(chan struct{})

	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
		log.Printf("grpc stopped cleanly")

	case <-ctx.Done():
		// A stuck RPC must not hold the deployment forever.
		log.Printf("grpc graceful stop timed out, forcing")
		grpcServer.Stop()
	}

	return nil
}

func seed(service *inventory.Service) error {
	items := []inventory.Item{
		{SKU: "KB-01", Name: "Mechanical Keyboard", Quantity: 12, Location: "A1"},
		{SKU: "MS-02", Name: "Wireless Mouse", Quantity: 40, Location: "A2"},
		{SKU: "HP-03", Name: "Studio Headphones", Quantity: 3, Location: "B1"},
	}

	for _, item := range items {
		if _, err := service.Create(context.Background(), item, ""); err != nil {
			return err
		}
	}

	log.Printf("seeded %d items", service.Count())

	return nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
