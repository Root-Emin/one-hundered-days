package main

import (
	"bytes"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-61/gen/catalog/v1"
)

func sampleProduct() *catalogv1.Product {
	return &catalogv1.Product{
		Id:           42,
		Sku:          "KB-01",
		Name:         "Mechanical Keyboard",
		Price:        &catalogv1.Money{Cents: 12900, Currency: "EUR"},
		Stock:        7,
		Availability: catalogv1.Availability_AVAILABILITY_IN_STOCK,
		Tags:         []string{"peripherals", "input"},
		Attributes:   map[string]string{"switch": "brown"},
		CreatedAt:    timestamppb.New(time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)),
	}
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()

	original := sampleProduct()

	encoded, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	decoded := &catalogv1.Product{}

	if err := proto.Unmarshal(encoded, decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// proto.Equal, not reflect.DeepEqual: generated messages carry internal
	// state that a struct comparison would trip over.
	if !proto.Equal(original, decoded) {
		t.Fatalf("round trip mismatch:\n got %v\nwant %v", decoded, original)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	t.Parallel()

	original := sampleProduct()

	encoded, err := protojson.Marshal(original)
	if err != nil {
		t.Fatalf("protojson marshal: %v", err)
	}

	decoded := &catalogv1.Product{}

	if err := protojson.Unmarshal(encoded, decoded); err != nil {
		t.Fatalf("protojson unmarshal: %v", err)
	}

	if !proto.Equal(original, decoded) {
		t.Fatal("protojson round trip changed the message")
	}

	// The canonical JSON uses lowerCamelCase field names by default.
	if !bytes.Contains(encoded, []byte(`"createdAt"`)) {
		t.Fatalf("unexpected JSON field naming: %s", encoded)
	}
}

// TestUnknownFieldsSurvive is the compatibility guarantee that makes rolling
// deploys safe: a message written by a NEWER sender, parsed by an OLDER
// receiver and forwarded on, must keep the fields the receiver did not
// understand.
func TestUnknownFieldsSurvive(t *testing.T) {
	t.Parallel()

	original := sampleProduct()

	encoded, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Field 99, varint, value 7 - a field this build has never heard of.
	// Tag byte: (99 << 3) | 0 = 792, encoded as a varint.
	future := append(append([]byte(nil), encoded...), 0x98, 0x06, 0x07)

	decoded := &catalogv1.Product{}

	if err := proto.Unmarshal(future, decoded); err != nil {
		t.Fatalf("unmarshal with an unknown field: %v", err)
	}

	// The known fields decoded correctly.
	if decoded.GetSku() != "KB-01" {
		t.Fatalf("sku = %q", decoded.GetSku())
	}

	// And the unknown bytes were kept.
	if len(decoded.ProtoReflect().GetUnknown()) == 0 {
		t.Fatal("the unknown field was dropped: forwarding would lose data")
	}

	reencoded, err := proto.Marshal(decoded)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}

	if !bytes.Contains(reencoded, []byte{0x98, 0x06, 0x07}) {
		t.Fatal("the unknown field did not survive a re-encode")
	}
}

// TestProto3ZeroValues pins the semantics that surprise newcomers: a zero
// value is indistinguishable from an unset field unless the field is optional.
func TestProto3ZeroValues(t *testing.T) {
	t.Parallel()

	empty, err := proto.Marshal(&catalogv1.Product{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if len(empty) != 0 {
		t.Fatalf("an empty message encoded to %d bytes, want 0", len(empty))
	}

	withZero, err := proto.Marshal(&catalogv1.Product{Sku: "MS-02", Stock: 0})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	withoutZero, err := proto.Marshal(&catalogv1.Product{Sku: "MS-02"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if !bytes.Equal(withZero, withoutZero) {
		t.Fatal("stock=0 and unset stock produced different bytes")
	}

	// An optional field records presence explicitly.
	explicit := &catalogv1.Product{Sku: "MS-02", SupplierCode: proto.String("")}

	explicitBytes, err := proto.Marshal(explicit)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if bytes.Equal(explicitBytes, withoutZero) {
		t.Fatal("an explicitly set optional empty string was not encoded")
	}

	roundTripped := &catalogv1.Product{}

	if err := proto.Unmarshal(explicitBytes, roundTripped); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if roundTripped.SupplierCode == nil {
		t.Fatal("presence was lost in the round trip")
	}
}

func TestEnumDefaultIsUnspecified(t *testing.T) {
	t.Parallel()

	product := &catalogv1.Product{Sku: "KB-01"}

	if product.GetAvailability() != catalogv1.Availability_AVAILABILITY_UNSPECIFIED {
		t.Fatalf("default availability = %v", product.GetAvailability())
	}

	// The zero enum must never mean something real, or a message from a
	// sender that did not set the field is silently misread.
	if catalogv1.Availability_AVAILABILITY_UNSPECIFIED != 0 {
		t.Fatal("UNSPECIFIED is not the zero value")
	}
}

func TestNilGettersDoNotPanic(t *testing.T) {
	t.Parallel()

	var product *catalogv1.Product

	if cents := product.GetPrice().GetCents(); cents != 0 {
		t.Fatalf("cents = %d", cents)
	}

	if tags := product.GetTags(); len(tags) != 0 {
		t.Fatalf("tags = %v", tags)
	}
}

func TestNestedMessages(t *testing.T) {
	t.Parallel()

	order := &catalogv1.Order{
		Id:            1,
		CustomerEmail: "ada@example.com",
		Lines: []*catalogv1.Order_Line{
			{Sku: "KB-01", Quantity: 2, UnitPrice: &catalogv1.Money{Cents: 12900, Currency: "EUR"}},
		},
		Total:    &catalogv1.Money{Cents: 25800, Currency: "EUR"},
		PlacedAt: timestamppb.New(time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)),
	}

	encoded, err := proto.Marshal(order)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	decoded := &catalogv1.Order{}

	if err := proto.Unmarshal(encoded, decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(decoded.GetLines()) != 1 || decoded.GetLines()[0].GetQuantity() != 2 {
		t.Fatalf("lines = %v", decoded.GetLines())
	}

	if !decoded.GetPlacedAt().AsTime().Equal(order.GetPlacedAt().AsTime()) {
		t.Fatal("timestamp did not survive the round trip")
	}
}
