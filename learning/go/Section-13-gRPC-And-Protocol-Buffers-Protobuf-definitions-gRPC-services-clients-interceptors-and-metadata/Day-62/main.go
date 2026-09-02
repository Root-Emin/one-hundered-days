package main

import (
	"fmt"
	"log"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"

	inventoryv1 "example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-62/gen/inventory/v1"
)

/*
Day 62 - gRPC & Protocol Buffers: Defining gRPC Services

Tasks covered:

 1. Unary RPC methods declared in proto/inventory/v1/inventory.proto
 2. Server and client stubs generated with protoc-gen-go-grpc
 3. Versioning: the package carries the major version, and the file documents
    which changes are safe (see the compatibility table this program prints)
 4. Every RPC documented in the .proto with its errors and idempotency

Generate:

	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	buf lint
	buf generate

Check that a change is safe before shipping it:

	buf breaking --against '.git#branch=main'

Run:

	go run .

Test:

	go test ./...

Day 63 implements these stubs; today is about the contract itself.
*/

// stubServer shows what the generated code asks of an implementation.
//
// Embedding UnimplementedInventoryServiceServer is not optional here
// (require_unimplemented_servers=true in buf.gen.yaml): it means a method
// added to the .proto later compiles into an "unimplemented" response instead
// of breaking every server build at once. The trade-off is that forgetting to
// implement a method is a runtime error rather than a compile error - so the
// test file asserts every method is really implemented.
type stubServer struct {
	inventoryv1.UnimplementedInventoryServiceServer
}

var _ inventoryv1.InventoryServiceServer = (*stubServer)(nil)

func main() {
	log.SetFlags(0)

	//
	// 1. What was generated
	//

	fmt.Println("\n1) Generated surface")
	fmt.Println(strings.Repeat("-", 78))

	fmt.Printf("  service constant : %s\n", inventoryv1.InventoryService_ServiceDesc.ServiceName)
	fmt.Printf("  server interface : InventoryServiceServer (%d methods)\n",
		len(inventoryv1.InventoryService_ServiceDesc.Methods))
	fmt.Println("  client interface : InventoryServiceClient")
	fmt.Println("  constructor      : NewInventoryServiceClient(grpc.ClientConnInterface)")
	fmt.Println("  registration     : RegisterInventoryServiceServer(grpcServer, impl)")

	//
	// 2. The methods, read from the descriptor
	//

	fmt.Println("\n2) Methods (read from the compiled descriptor, not hardcoded)")
	fmt.Println(strings.Repeat("-", 78))

	descriptor := inventoryv1.File_inventory_v1_inventory_proto.Services().Get(0)

	fmt.Printf("%-14s %-24s %-24s %s\n", "METHOD", "REQUEST", "RESPONSE", "KIND")

	for i := range descriptor.Methods().Len() {
		method := descriptor.Methods().Get(i)

		fmt.Printf("%-14s %-24s %-24s %s\n",
			method.Name(),
			shortName(method.Input()),
			shortName(method.Output()),
			kindOf(method),
		)
	}

	//
	// 3. Message fields and numbers
	//

	fmt.Println("\n3) Item fields (numbers are the contract)")
	fmt.Println(strings.Repeat("-", 78))

	item := inventoryv1.File_inventory_v1_inventory_proto.Messages().ByName("Item")

	fmt.Printf("%-6s %-14s %-12s %s\n", "NUMBER", "NAME", "TYPE", "PRESENCE")

	for i := range item.Fields().Len() {
		field := item.Fields().Get(i)

		presence := "implicit"
		if field.HasPresence() {
			presence = "explicit (optional)"
		}

		fmt.Printf("%-6d %-14s %-12s %s\n", field.Number(), field.Name(), field.Kind(), presence)
	}

	//
	// 4. Versioning rules
	//

	printCompatibility()
}

func shortName(message protoreflect.MessageDescriptor) string {
	parts := strings.Split(string(message.FullName()), ".")

	return parts[len(parts)-1]
}

func kindOf(method protoreflect.MethodDescriptor) string {
	switch {
	case method.IsStreamingClient() && method.IsStreamingServer():
		return "bidirectional streaming"
	case method.IsStreamingClient():
		return "client streaming"
	case method.IsStreamingServer():
		return "server streaming"
	default:
		return "unary"
	}
}

func printCompatibility() {
	fmt.Println("\n4) What you may change without breaking clients")
	fmt.Println(strings.Repeat("-", 78))

	rows := []struct {
		change string
		safe   string
		why    string
	}{
		{"Add a new field", "yes", "old readers keep it as an unknown field and pass it through"},
		{"Add a new RPC", "yes", "old clients simply never call it"},
		{"Add an enum value", "careful", "old clients decode it as the unknown value; plan for that"},
		{"Rename a field", "yes*", "names are not on the wire, but JSON consumers and code do break"},
		{"Change a field number", "NO", "the wire meaning of those bytes changes underneath every peer"},
		{"Change a field type", "NO", "int32 -> string is a different wire type; the parse fails"},
		{"Remove a field", "only if reserved", "reserve the number and the name so neither is reused"},
		{"Rename an RPC", "NO", "the method name is the HTTP/2 path clients call"},
		{"Make a field optional", "yes", "presence is an extra bit, not a different encoding"},
		{"Change the package", "NO", "that is a new service: publish inventory.v2 instead"},
	}

	fmt.Printf("%-24s %-16s %s\n", "CHANGE", "SAFE?", "WHY")

	for _, row := range rows {
		fmt.Printf("%-24s %-16s %s\n", row.change, row.safe, row.why)
	}

	fmt.Println("\n  * Renaming is wire-safe and source-breaking. Treat it as breaking")
	fmt.Println("    unless every consumer is regenerated in the same change.")

	fmt.Println("\n  Enforce all of this in CI rather than in review:")
	fmt.Println("      buf breaking --against '.git#branch=main'")
	fmt.Println("  It fails the build on exactly the rows marked NO.")

	fmt.Println("\n  Versioning strategy used here:")
	fmt.Println("    inventory.v1  - stable, additive changes only")
	fmt.Println("    inventory.v2  - a new package when something must break;")
	fmt.Println("                    both are served at once until v1 is retired")
}
