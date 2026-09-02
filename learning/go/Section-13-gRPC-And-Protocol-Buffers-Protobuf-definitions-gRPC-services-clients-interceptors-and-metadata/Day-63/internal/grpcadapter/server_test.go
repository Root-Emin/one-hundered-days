package grpcadapter

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	inventoryv1 "example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-63/gen/inventory/v1"
	"example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-63/internal/inventory"
)

/*
gRPC tests over bufconn: a real server, real generated stubs, real status
codes - carried by an in-memory listener instead of a TCP port.

No port to allocate, nothing to clean up, and the tests can run in parallel.
This is the standard way to test a gRPC service in Go.
*/

func newTestClient(t *testing.T) (inventoryv1.InventoryServiceClient, *inventory.Service) {
	t.Helper()

	service := inventory.NewService(inventory.SystemClock{})

	const bufferSize = 1024 * 1024

	listener := bufconn.Listen(bufferSize)

	server := grpc.NewServer()
	inventoryv1.RegisterInventoryServiceServer(server, NewServer(service))

	go func() {
		if err := server.Serve(listener); err != nil {
			// Serve returns when the listener closes; that is not a failure.
			t.Logf("serve stopped: %v", err)
		}
	}()

	connection, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close connection: %v", err)
		}

		server.Stop()

		if err := listener.Close(); err != nil && err != net.ErrClosed {
			t.Logf("close listener: %v", err)
		}
	})

	return inventoryv1.NewInventoryServiceClient(connection), service
}

func seedItem(t *testing.T, client inventoryv1.InventoryServiceClient, sku string, quantity int32) {
	t.Helper()

	_, err := client.CreateItem(context.Background(), &inventoryv1.CreateItemRequest{
		Sku: sku, Name: "Test item", Quantity: quantity, Location: "A1",
	})
	if err != nil {
		t.Fatalf("seed %s: %v", sku, err)
	}
}

func TestCreateAndGet(t *testing.T) {
	t.Parallel()

	client, _ := newTestClient(t)
	ctx := context.Background()

	created, err := client.CreateItem(ctx, &inventoryv1.CreateItemRequest{
		Sku: "kb-01", Name: "Keyboard", Quantity: 5, Location: "A1",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// The service normalises the sku, and the response carries the stored form.
	if created.GetItem().GetSku() != "KB-01" {
		t.Fatalf("sku = %q, want KB-01", created.GetItem().GetSku())
	}

	if created.GetItem().GetUpdatedAt() == nil {
		t.Fatal("updated_at was not set")
	}

	got, err := client.GetItem(ctx, &inventoryv1.GetItemRequest{Sku: "KB-01"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.GetItem().GetQuantity() != 5 {
		t.Fatalf("quantity = %d", got.GetItem().GetQuantity())
	}
}

// TestStatusCodes is the mapping contract: each domain failure must arrive as
// a specific code, because that is what clients branch on.
func TestStatusCodes(t *testing.T) {
	t.Parallel()

	client, _ := newTestClient(t)
	ctx := context.Background()

	seedItem(t, client, "kb-01", 3)

	tests := []struct {
		name string
		call func() error
		want codes.Code
	}{
		{
			"unknown sku is NotFound",
			func() error {
				_, err := client.GetItem(ctx, &inventoryv1.GetItemRequest{Sku: "zz-99"})
				return err
			},
			codes.NotFound,
		},
		{
			"empty sku is InvalidArgument",
			func() error {
				_, err := client.GetItem(ctx, &inventoryv1.GetItemRequest{Sku: ""})
				return err
			},
			codes.InvalidArgument,
		},
		{
			"duplicate sku is AlreadyExists",
			func() error {
				_, err := client.CreateItem(ctx, &inventoryv1.CreateItemRequest{
					Sku: "KB-01", Name: "Copy", Quantity: 1,
				})
				return err
			},
			codes.AlreadyExists,
		},
		{
			"missing name is InvalidArgument",
			func() error {
				_, err := client.CreateItem(ctx, &inventoryv1.CreateItemRequest{Sku: "new-1", Quantity: 1})
				return err
			},
			codes.InvalidArgument,
		},
		{
			"oversell is FailedPrecondition",
			func() error {
				_, err := client.AdjustStock(ctx, &inventoryv1.AdjustStockRequest{Sku: "KB-01", Delta: -100})
				return err
			},
			codes.FailedPrecondition,
		},
		{
			"zero delta is InvalidArgument",
			func() error {
				_, err := client.AdjustStock(ctx, &inventoryv1.AdjustStockRequest{Sku: "KB-01", Delta: 0})
				return err
			},
			codes.InvalidArgument,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.call()

			if got := status.Code(err); got != test.want {
				t.Fatalf("code = %s, want %s (err=%v)", got, test.want, err)
			}
		})
	}
}

func TestAdjustStock(t *testing.T) {
	t.Parallel()

	client, _ := newTestClient(t)
	ctx := context.Background()

	seedItem(t, client, "kb-01", 10)

	response, err := client.AdjustStock(ctx, &inventoryv1.AdjustStockRequest{
		Sku: "KB-01", Delta: -3, Reason: "order 1",
	})
	if err != nil {
		t.Fatalf("adjust: %v", err)
	}

	if response.GetPreviousQuantity() != 10 || response.GetItem().GetQuantity() != 7 {
		t.Fatalf("previous=%d current=%d", response.GetPreviousQuantity(), response.GetItem().GetQuantity())
	}
}

// TestIdempotency: the same request_id must not apply an adjustment twice.
// This is the property that makes a client-side retry safe after a timeout.
func TestIdempotency(t *testing.T) {
	t.Parallel()

	client, _ := newTestClient(t)
	ctx := context.Background()

	seedItem(t, client, "kb-01", 10)

	for range 3 {
		if _, err := client.AdjustStock(ctx, &inventoryv1.AdjustStockRequest{
			Sku: "KB-01", Delta: -3, RequestId: "retry-me",
		}); err != nil {
			t.Fatalf("adjust: %v", err)
		}
	}

	got, err := client.GetItem(ctx, &inventoryv1.GetItemRequest{Sku: "KB-01"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.GetItem().GetQuantity() != 7 {
		t.Fatalf("quantity = %d, want 7 - the retried adjustment was applied more than once",
			got.GetItem().GetQuantity())
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	t.Parallel()

	client, _ := newTestClient(t)
	ctx := context.Background()

	seedItem(t, client, "kb-01", 1)

	first, err := client.DeleteItem(ctx, &inventoryv1.DeleteItemRequest{Sku: "KB-01"})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	if !first.GetDeleted() {
		t.Fatal("first delete reported nothing removed")
	}

	// The second delete is OK, not NotFound: a retried delete must not fail.
	second, err := client.DeleteItem(ctx, &inventoryv1.DeleteItemRequest{Sku: "KB-01"})
	if err != nil {
		t.Fatalf("second delete: %v", err)
	}

	if second.GetDeleted() {
		t.Fatal("second delete claimed to remove a row that was already gone")
	}
}

func TestPagination(t *testing.T) {
	t.Parallel()

	client, _ := newTestClient(t)
	ctx := context.Background()

	for _, sku := range []string{"a-1", "b-2", "c-3", "d-4", "e-5"} {
		seedItem(t, client, sku, 1)
	}

	first, err := client.ListItems(ctx, &inventoryv1.ListItemsRequest{PageSize: 2})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(first.GetItems()) != 2 || first.GetTotalSize() != 5 {
		t.Fatalf("page = %d items, total = %d", len(first.GetItems()), first.GetTotalSize())
	}

	if first.GetNextPageToken() == "" {
		t.Fatal("no next page token on a partial listing")
	}

	second, err := client.ListItems(ctx, &inventoryv1.ListItemsRequest{
		PageSize: 2, PageToken: first.GetNextPageToken(),
	})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}

	if len(second.GetItems()) != 2 {
		t.Fatalf("second page = %d items", len(second.GetItems()))
	}

	if second.GetItems()[0].GetSku() == first.GetItems()[0].GetSku() {
		t.Fatal("the second page repeated the first")
	}
}

// TestDeadlinePropagates: the client's context deadline reaches the server as
// a real deadline, and the failure is DeadlineExceeded rather than a hang.
func TestDeadlinePropagates(t *testing.T) {
	t.Parallel()

	client, _ := newTestClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()

	_, err := client.GetItem(ctx, &inventoryv1.GetItemRequest{Sku: "KB-01"})

	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("code = %s, want DeadlineExceeded", status.Code(err))
	}
}

// TestSharedServiceState is the point of the day: the gRPC adapter and the
// HTTP adapter would see the same data, because both hold the same service.
func TestSharedServiceState(t *testing.T) {
	t.Parallel()

	client, service := newTestClient(t)

	seedItem(t, client, "kb-01", 4)

	item, err := service.Get(context.Background(), "KB-01")
	if err != nil {
		t.Fatalf("service get: %v", err)
	}

	if item.Quantity != 4 {
		t.Fatalf("quantity = %d", item.Quantity)
	}
}
