package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-61/gen/catalog/v1"
)

/*
Day 61 - gRPC & Protocol Buffers: Protocol Buffers Basics

Tasks covered:

 1. Toolchain installed and wired up (buf + the Go plugins - see below)
 2. Messages defined in proto/catalog/v1/catalog.proto
 3. Go code generated into gen/catalog/v1
 4. Messages marshalled and unmarshalled, with the wire format inspected

Toolchain:

	go install github.com/bufbuild/buf/cmd/buf@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

	buf lint          # style and consistency checks on the .proto files
	buf generate      # regenerate gen/ after any .proto change
	buf breaking --against '.git#branch=main'   # refuse incompatible changes

buf compiles .proto files itself, so no system protoc install is needed. With
protoc instead, the equivalent command is:

	protoc --go_out=gen --go_opt=paths=source_relative \
	  -I proto proto/catalog/v1/catalog.proto

Run:

	go run .

Test:

	go test ./...
*/

func main() {
	log.SetFlags(0)

	product := &catalogv1.Product{
		Id:           42,
		Sku:          "KB-01",
		Name:         "Mechanical Keyboard",
		Price:        &catalogv1.Money{Cents: 12900, Currency: "EUR"},
		Stock:        7,
		Availability: catalogv1.Availability_AVAILABILITY_IN_STOCK,
		Tags:         []string{"peripherals", "input"},
		Attributes:   map[string]string{"switch": "brown", "layout": "iso"},
		CreatedAt:    timestamppb.New(time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)),
	}

	//
	// 1. Marshal and unmarshal
	//

	fmt.Println("\n1) Round trip")
	fmt.Println(strings.Repeat("-", 72))

	encoded, err := proto.Marshal(product)
	if err != nil {
		log.Fatalf("marshal: %v", err)
	}

	decoded := &catalogv1.Product{}

	if err := proto.Unmarshal(encoded, decoded); err != nil {
		log.Fatalf("unmarshal: %v", err)
	}

	// proto.Equal compares by protobuf semantics, not by Go struct identity -
	// generated messages carry internal state that == would trip over.
	if !proto.Equal(product, decoded) {
		log.Fatalf("round trip changed the message")
	}

	fmt.Printf("  encoded %d bytes, decoded back to an identical message\n", len(encoded))
	fmt.Printf("  sku=%s price=%d %s stock=%d availability=%s\n",
		decoded.GetSku(), decoded.GetPrice().GetCents(), decoded.GetPrice().GetCurrency(),
		decoded.GetStock(), decoded.GetAvailability())

	//
	// 2. Size on the wire
	//

	fmt.Println("\n2) Wire size compared with JSON")
	fmt.Println(strings.Repeat("-", 72))

	// protojson is the canonical JSON form of a protobuf message.
	jsonBytes, err := protojson.Marshal(product)
	if err != nil {
		log.Fatalf("protojson marshal: %v", err)
	}

	// And the shape a hand-written Go struct would produce.
	plainJSON, err := json.Marshal(map[string]any{
		"id": 42, "sku": "KB-01", "name": "Mechanical Keyboard",
		"price":        map[string]any{"cents": 12900, "currency": "EUR"},
		"stock":        7,
		"availability": "AVAILABILITY_IN_STOCK",
		"tags":         []string{"peripherals", "input"},
		"attributes":   map[string]string{"switch": "brown", "layout": "iso"},
		"created_at":   "2026-03-01T12:00:00Z",
	})
	if err != nil {
		log.Fatalf("json marshal: %v", err)
	}

	fmt.Printf("  protobuf   %3d bytes\n", len(encoded))
	fmt.Printf("  protojson  %3d bytes\n", len(jsonBytes))
	fmt.Printf("  plain JSON %3d bytes\n", len(plainJSON))
	fmt.Printf("  protobuf is %.0f%% of the JSON size\n",
		float64(len(encoded))/float64(len(plainJSON))*100)
	fmt.Println("  Field names are not on the wire: only field numbers and types are,")
	fmt.Println("  which is why renaming a field is free and renumbering one is not.")

	//
	// 3. What the bytes actually are
	//

	fmt.Println("\n3) The encoding, byte by byte")
	fmt.Println(strings.Repeat("-", 72))

	small := &catalogv1.Money{Cents: 300, Currency: "EUR"}

	smallBytes, err := proto.Marshal(small)
	if err != nil {
		log.Fatalf("marshal: %v", err)
	}

	fmt.Printf("  Money{cents: 300, currency: \"EUR\"} -> % x  (%d bytes)\n", smallBytes, len(smallBytes))
	fmt.Println("    08          field 1, varint      (1<<3 | 0)")
	fmt.Println("    ac 02       300 as a varint      (0x2ac = 300, 7 bits per byte)")
	fmt.Println("    12          field 2, length      (2<<3 | 2)")
	fmt.Println("    03 45 55 52 3 bytes: 'E' 'U' 'R'")

	//
	// 4. Proto3 defaults
	//

	fmt.Println("\n4) Zero values are not sent")
	fmt.Println(strings.Repeat("-", 72))

	empty := &catalogv1.Product{}

	emptyBytes, err := proto.Marshal(empty)
	if err != nil {
		log.Fatalf("marshal: %v", err)
	}

	zeroStock := &catalogv1.Product{Sku: "MS-02", Stock: 0}

	zeroStockBytes, err := proto.Marshal(zeroStock)
	if err != nil {
		log.Fatalf("marshal: %v", err)
	}

	fmt.Printf("  an empty Product encodes to %d bytes\n", len(emptyBytes))
	fmt.Printf("  Product{sku: \"MS-02\", stock: 0} encodes to %d bytes - stock is absent\n", len(zeroStockBytes))
	fmt.Println("  In proto3 a zero value and an unset field are the same thing on the")
	fmt.Println("  wire. When the difference matters (\"stock is 0\" vs \"stock unknown\"),")
	fmt.Println("  declare the field 'optional', as supplier_code is:")

	withOptional := &catalogv1.Product{Sku: "MS-02"}

	fmt.Printf("    supplier_code unset:   HasSupplierCode=%t value=%q\n",
		withOptional.SupplierCode != nil, withOptional.GetSupplierCode())

	withOptional.SupplierCode = proto.String("")

	fmt.Printf("    supplier_code set to \"\": HasSupplierCode=%t value=%q\n",
		withOptional.SupplierCode != nil, withOptional.GetSupplierCode())

	//
	// 5. Nil safety
	//

	fmt.Println("\n5) Getters are nil safe")
	fmt.Println(strings.Repeat("-", 72))

	var missing *catalogv1.Product

	// Reading through a nil message returns zero values instead of panicking,
	// which is why generated getters exist at all.
	fmt.Printf("  (*Product)(nil).GetPrice().GetCents() = %d\n", missing.GetPrice().GetCents())
	fmt.Printf("  product.GetPrice().GetCurrency()       = %q\n", product.GetPrice().GetCurrency())

	//
	// 6. Nested and repeated
	//

	fmt.Println("\n6) A larger message")
	fmt.Println(strings.Repeat("-", 72))

	order := &catalogv1.Order{
		Id:            1001,
		CustomerEmail: "ada@example.com",
		Lines: []*catalogv1.Order_Line{
			{Sku: "KB-01", Quantity: 2, UnitPrice: &catalogv1.Money{Cents: 12900, Currency: "EUR"}},
			{Sku: "MS-02", Quantity: 1, UnitPrice: &catalogv1.Money{Cents: 4900, Currency: "EUR"}},
		},
		Total:    &catalogv1.Money{Cents: 30700, Currency: "EUR"},
		PlacedAt: timestamppb.Now(),
	}

	orderBytes, err := proto.Marshal(order)
	if err != nil {
		log.Fatalf("marshal: %v", err)
	}

	fmt.Printf("  order with %d lines: %d bytes\n", len(order.GetLines()), len(orderBytes))

	pretty, err := protojson.MarshalOptions{Multiline: true, Indent: "    "}.Marshal(order)
	if err != nil {
		log.Fatalf("protojson: %v", err)
	}

	fmt.Printf("  as protojson:\n%s\n", indent(string(pretty), "  "))

	fmt.Println("Next: Day 62 adds service definitions to this .proto and generates")
	fmt.Println("gRPC stubs from them.")
}

func indent(text, prefix string) string {
	lines := strings.Split(text, "\n")

	for i, line := range lines {
		lines[i] = prefix + line
	}

	return strings.Join(lines, "\n")
}
