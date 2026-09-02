// Command client dials the inventory service and exercises every RPC,
// including the failure paths, so the status-code mapping is visible.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	inventoryv1 "example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-63/gen/inventory/v1"
)

/*
Run the server first:

	go run ./cmd/server

Then:

	go run ./cmd/client
	GRPC_ADDR=localhost:9090 go run ./cmd/client
*/

func main() {
	log.SetFlags(0)

	address := envOr("GRPC_ADDR", "localhost:9090")

	// NewClient replaces the deprecated Dial: it does not block, and the
	// first RPC establishes the connection. WithBlock-style waiting belongs in
	// a health check, not in construction.
	//
	// insecure.NewCredentials() is for local development only. Anything
	// crossing a network gets TLS credentials.
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial %s: %v", address, err)
	}

	// One connection per server, kept for the lifetime of the process, closed
	// on the way out. gRPC multiplexes every call over it - creating a
	// connection per request throws away HTTP/2 and is a common mistake.
	defer func() {
		if err := connection.Close(); err != nil {
			log.Printf("close connection: %v", err)
		}
	}()

	client := inventoryv1.NewInventoryServiceClient(connection)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fmt.Println("\nCalling the inventory service")
	fmt.Println(strings.Repeat("-", 74))

	//
	// Create
	//

	created, err := client.CreateItem(ctx, &inventoryv1.CreateItemRequest{
		Sku:       "cl-01",
		Name:      "Cable",
		Quantity:  10,
		Location:  "C1",
		RequestId: "demo-create-1",
	})
	if err != nil {
		log.Fatalf("create: %v", describe(err))
	}

	fmt.Printf("  CreateItem  -> %s quantity=%d\n",
		created.GetItem().GetSku(), created.GetItem().GetQuantity())

	// The same request id again: idempotent, so no AlreadyExists.
	replay, err := client.CreateItem(ctx, &inventoryv1.CreateItemRequest{
		Sku: "cl-01", Name: "Cable", Quantity: 10, RequestId: "demo-create-1",
	})
	if err != nil {
		log.Fatalf("replayed create: %v", describe(err))
	}

	fmt.Printf("  CreateItem  -> replay with the same request_id returned %s, not an error\n",
		replay.GetItem().GetSku())

	//
	// Get
	//

	got, err := client.GetItem(ctx, &inventoryv1.GetItemRequest{Sku: "kb-01"})
	if err != nil {
		log.Fatalf("get: %v", describe(err))
	}

	fmt.Printf("  GetItem     -> %s %q quantity=%d\n",
		got.GetItem().GetSku(), got.GetItem().GetName(), got.GetItem().GetQuantity())

	//
	// List
	//

	list, err := client.ListItems(ctx, &inventoryv1.ListItemsRequest{PageSize: 2})
	if err != nil {
		log.Fatalf("list: %v", describe(err))
	}

	skus := make([]string, 0, len(list.GetItems()))

	for _, item := range list.GetItems() {
		skus = append(skus, item.GetSku())
	}

	fmt.Printf("  ListItems   -> %s (total %d, next page token %q)\n",
		strings.Join(skus, ", "), list.GetTotalSize(), list.GetNextPageToken())

	//
	// Adjust
	//

	adjusted, err := client.AdjustStock(ctx, &inventoryv1.AdjustStockRequest{
		Sku: "kb-01", Delta: -2, Reason: "order 1001", RequestId: "demo-adjust-1",
	})
	if err != nil {
		log.Fatalf("adjust: %v", describe(err))
	}

	fmt.Printf("  AdjustStock -> %d became %d\n",
		adjusted.GetPreviousQuantity(), adjusted.GetItem().GetQuantity())

	//
	// The error paths, which is where status codes earn their keep
	//

	fmt.Println("\nError codes")
	fmt.Println(strings.Repeat("-", 74))

	failures := []struct {
		label string
		call  func() error
	}{
		{"unknown sku", func() error {
			_, err := client.GetItem(ctx, &inventoryv1.GetItemRequest{Sku: "zz-99"})
			return err
		}},
		{"empty sku", func() error {
			_, err := client.GetItem(ctx, &inventoryv1.GetItemRequest{Sku: ""})
			return err
		}},
		{"duplicate sku", func() error {
			_, err := client.CreateItem(ctx, &inventoryv1.CreateItemRequest{
				Sku: "kb-01", Name: "Duplicate", Quantity: 1,
			})
			return err
		}},
		{"oversell", func() error {
			_, err := client.AdjustStock(ctx, &inventoryv1.AdjustStockRequest{Sku: "hp-03", Delta: -999})
			return err
		}},
		{"zero delta", func() error {
			_, err := client.AdjustStock(ctx, &inventoryv1.AdjustStockRequest{Sku: "hp-03", Delta: 0})
			return err
		}},
	}

	for _, failure := range failures {
		fmt.Printf("  %-14s -> %s\n", failure.label, describe(failure.call()))
	}

	//
	// Deadlines
	//

	fmt.Println("\nDeadlines")
	fmt.Println(strings.Repeat("-", 74))

	shortCtx, shortCancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer shortCancel()

	_, err = client.GetItem(shortCtx, &inventoryv1.GetItemRequest{Sku: "kb-01"})

	fmt.Printf("  1ns deadline   -> %s\n", describe(err))
	fmt.Println("  Every RPC should carry a deadline: without one a stuck server")
	fmt.Println("  holds the caller's goroutine until the connection dies.")

	fmt.Println()
}

// describe prints the gRPC status the way a client should read it: the code
// first, because that is what the caller branches on.
func describe(err error) string {
	if err == nil {
		return "OK"
	}

	statusError, ok := status.FromError(err)
	if !ok {
		return "non-status error: " + err.Error()
	}

	if statusError.Code() == codes.OK {
		return "OK"
	}

	return fmt.Sprintf("%-18s %s", statusError.Code(), statusError.Message())
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
