package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	inventoryv1 "example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-62/gen/inventory/v1"
)

/*
Contract tests.

These do not test behaviour - Day 63 does that. They test the API shape, which
is the thing that must not change by accident:

  - every declared RPC really is implemented
  - request/response types follow the naming convention
  - field numbers are stable

Checks like these belong next to a generated API. A renumbered field is not
caught by any behavioural test; it is caught here, or in production.
*/

// TestEveryRPCIsImplemented guards the cost of require_unimplemented_servers:
// a forgotten method compiles fine and fails at runtime, so assert it here.
func TestEveryRPCIsImplemented(t *testing.T) {
	t.Parallel()

	implementation := reflect.TypeOf(&stubServer{})
	descriptor := inventoryv1.File_inventory_v1_inventory_proto.Services().Get(0)

	for i := range descriptor.Methods().Len() {
		name := string(descriptor.Methods().Get(i).Name())

		method, found := implementation.MethodByName(name)
		if !found {
			t.Errorf("%s is declared in the .proto but not implemented", name)
			continue
		}

		// (receiver, context, request) -> (response, error)
		if method.Type.NumIn() != 3 || method.Type.NumOut() != 2 {
			t.Errorf("%s has an unexpected signature: %s", name, method.Type)
		}
	}
}

// TestRequestResponseNaming keeps the API consistent: GetItem takes a
// GetItemRequest and returns a GetItemResponse, always.
func TestRequestResponseNaming(t *testing.T) {
	t.Parallel()

	descriptor := inventoryv1.File_inventory_v1_inventory_proto.Services().Get(0)

	for i := range descriptor.Methods().Len() {
		method := descriptor.Methods().Get(i)

		name := string(method.Name())

		input := lastSegment(string(method.Input().FullName()))
		output := lastSegment(string(method.Output().FullName()))

		if input != name+"Request" {
			t.Errorf("%s takes %s, want %sRequest", name, input, name)
		}

		if output != name+"Response" {
			t.Errorf("%s returns %s, want %sResponse", name, output, name)
		}
	}
}

// TestFieldNumbersAreStable is the golden test for the wire contract. Adding a
// field is fine; changing a number here must be a deliberate edit to this map,
// with a very good reason.
func TestFieldNumbersAreStable(t *testing.T) {
	t.Parallel()

	expected := map[string]int32{
		"sku":        1,
		"name":       2,
		"quantity":   3,
		"location":   4,
		"updated_at": 5,
		"barcode":    6,
	}

	item := inventoryv1.File_inventory_v1_inventory_proto.Messages().ByName("Item")

	for i := range item.Fields().Len() {
		field := item.Fields().Get(i)

		want, known := expected[string(field.Name())]
		if !known {
			t.Errorf("new field %q (number %d): add it to the golden map on purpose",
				field.Name(), field.Number())

			continue
		}

		if int32(field.Number()) != want {
			t.Errorf("field %q has number %d, want %d - this breaks every deployed client",
				field.Name(), field.Number(), want)
		}
	}

	if item.Fields().Len() < len(expected) {
		t.Errorf("a field was removed: %d fields remain, expected at least %d",
			item.Fields().Len(), len(expected))
	}
}

func TestPackageCarriesTheMajorVersion(t *testing.T) {
	t.Parallel()

	name := string(inventoryv1.File_inventory_v1_inventory_proto.Package())

	if !strings.HasSuffix(name, ".v1") {
		t.Fatalf("package = %q, want a versioned package like inventory.v1", name)
	}
}

// TestUnimplementedReturnsTheRightCode: the embedded Unimplemented server
// answers with codes.Unimplemented, which is what lets a client tell "this
// server is older than my client" apart from a real failure.
func TestUnimplementedReturnsTheRightCode(t *testing.T) {
	t.Parallel()

	var server inventoryv1.UnimplementedInventoryServiceServer

	_, err := server.GetItem(context.Background(), &inventoryv1.GetItemRequest{Sku: "KB-01"})

	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("code = %s, want Unimplemented", status.Code(err))
	}
}

func TestClientInterfaceCoversEveryMethod(t *testing.T) {
	t.Parallel()

	clientType := reflect.TypeOf((*inventoryv1.InventoryServiceClient)(nil)).Elem()
	descriptor := inventoryv1.File_inventory_v1_inventory_proto.Services().Get(0)

	if clientType.NumMethod() != descriptor.Methods().Len() {
		t.Fatalf("client has %d methods, the service declares %d",
			clientType.NumMethod(), descriptor.Methods().Len())
	}
}

func lastSegment(fullName string) string {
	parts := strings.Split(fullName, ".")

	return parts[len(parts)-1]
}
