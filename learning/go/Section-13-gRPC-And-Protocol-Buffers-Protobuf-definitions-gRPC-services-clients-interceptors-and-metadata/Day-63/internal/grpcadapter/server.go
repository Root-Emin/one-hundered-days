// Package grpcadapter implements the generated gRPC server interface on top
// of the shared inventory service.
//
// It is a transport adapter, exactly like the HTTP handler next door: it
// converts protobuf messages into domain calls and domain errors into gRPC
// status codes. There is no business rule in this package.
package grpcadapter

import (
	"context"
	"errors"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	inventoryv1 "example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-63/gen/inventory/v1"
	"example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-63/internal/inventory"
)

type Server struct {
	// Embedding the generated Unimplemented type keeps this build compiling
	// when a new RPC is added to the .proto.
	inventoryv1.UnimplementedInventoryServiceServer

	service *inventory.Service
}

func NewServer(service *inventory.Service) *Server {
	return &Server{service: service}
}

var _ inventoryv1.InventoryServiceServer = (*Server)(nil)

func (s *Server) GetItem(ctx context.Context, request *inventoryv1.GetItemRequest) (*inventoryv1.GetItemResponse, error) {
	item, err := s.service.Get(ctx, request.GetSku())
	if err != nil {
		return nil, toStatus(err)
	}

	return &inventoryv1.GetItemResponse{Item: toProto(item)}, nil
}

func (s *Server) ListItems(ctx context.Context, request *inventoryv1.ListItemsRequest) (*inventoryv1.ListItemsResponse, error) {
	items, next, total, err := s.service.List(ctx,
		request.GetLocation(), request.GetPageSize(), request.GetPageToken())
	if err != nil {
		return nil, toStatus(err)
	}

	response := &inventoryv1.ListItemsResponse{
		Items:         make([]*inventoryv1.Item, 0, len(items)),
		NextPageToken: next,
		TotalSize:     total,
	}

	for _, item := range items {
		response.Items = append(response.Items, toProto(item))
	}

	return response, nil
}

func (s *Server) CreateItem(ctx context.Context, request *inventoryv1.CreateItemRequest) (*inventoryv1.CreateItemResponse, error) {
	item, err := s.service.Create(ctx, inventory.Item{
		SKU:      request.GetSku(),
		Name:     request.GetName(),
		Quantity: request.GetQuantity(),
		Location: request.GetLocation(),
	}, request.GetRequestId())
	if err != nil {
		return nil, toStatus(err)
	}

	return &inventoryv1.CreateItemResponse{Item: toProto(item)}, nil
}

func (s *Server) AdjustStock(ctx context.Context, request *inventoryv1.AdjustStockRequest) (*inventoryv1.AdjustStockResponse, error) {
	item, previous, err := s.service.Adjust(ctx,
		request.GetSku(), request.GetDelta(), request.GetReason(), request.GetRequestId())
	if err != nil {
		return nil, toStatus(err)
	}

	return &inventoryv1.AdjustStockResponse{
		Item:             toProto(item),
		PreviousQuantity: previous,
	}, nil
}

func (s *Server) DeleteItem(ctx context.Context, request *inventoryv1.DeleteItemRequest) (*inventoryv1.DeleteItemResponse, error) {
	deleted, err := s.service.Delete(ctx, request.GetSku())
	if err != nil {
		return nil, toStatus(err)
	}

	return &inventoryv1.DeleteItemResponse{Deleted: deleted}, nil
}

//
// MAPPING
//

func toProto(item inventory.Item) *inventoryv1.Item {
	message := &inventoryv1.Item{
		Sku:       item.SKU,
		Name:      item.Name,
		Quantity:  item.Quantity,
		Location:  item.Location,
		UpdatedAt: timestamppb.New(item.UpdatedAt),
	}

	if item.Barcode != "" {
		message.Barcode = &item.Barcode
	}

	return message
}

// toStatus is the gRPC twin of the HTTP respondError function: one place that
// turns domain meaning into protocol codes.
//
// The mapping mirrors the HTTP one on purpose:
//
//	NotFound          404
//	AlreadyExists     409
//	InvalidArgument   422
//	FailedPrecondition 409 (state, not input)
//	Internal          500
func toStatus(err error) error {
	switch {
	case errors.Is(err, inventory.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())

	case errors.Is(err, inventory.ErrAlreadyExists):
		return status.Error(codes.AlreadyExists, err.Error())

	case errors.Is(err, inventory.ErrValidation):
		return status.Error(codes.InvalidArgument, err.Error())

	case errors.Is(err, inventory.ErrInsufficient):
		// FailedPrecondition, not InvalidArgument: the request was fine, the
		// system state is what refused it. A client should not retry blindly.
		return status.Error(codes.FailedPrecondition, err.Error())

	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "request canceled")

	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "deadline exceeded")

	default:
		// Never hand an internal error text to a caller: log it, return a
		// generic message with the Internal code.
		log.Printf("internal error: %v", err)

		return status.Error(codes.Internal, "internal error")
	}
}
