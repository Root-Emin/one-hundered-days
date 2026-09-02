// Package grpcapi is the gRPC transport: interceptors plus a thin adapter over
// the shared notes service.
package grpcapi

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	notesv1 "example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-65/gen/notes/v1"
	"example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-65/internal/notes"
)

//
// AUTH
//

// Authenticator resolves a token into a user id. The same function is handed
// to the HTTP transport, so the two cannot drift apart.
type Authenticator func(token string) (string, bool)

type contextKey string

const userContextKey contextKey = "user_id"

func UserFrom(ctx context.Context) string {
	user, _ := ctx.Value(userContextKey).(string)

	return user
}

// AuthInterceptor is the gRPC twin of the HTTP auth middleware.
func AuthInterceptor(authenticate Authenticator) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {
		incoming, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}

		values := incoming.Get("authorization")
		if len(values) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing authorization metadata")
		}

		scheme, token, found := strings.Cut(values[0], " ")

		if !found || !strings.EqualFold(scheme, "bearer") {
			return nil, status.Error(codes.Unauthenticated, "authorization must be 'Bearer <token>'")
		}

		userID, valid := authenticate(strings.TrimSpace(token))
		if !valid {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}

		return handler(context.WithValue(ctx, userContextKey, userID), request)
	}
}

// LoggingInterceptor logs one line per RPC with the resulting status code.
func LoggingInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {
		start := time.Now()

		response, err := handler(ctx, request)

		log.Printf("grpc method=%s code=%s duration=%s",
			info.FullMethod, status.Code(err), time.Since(start).Round(time.Microsecond))

		return response, err
	}
}

func RecoveryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (response any, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("grpc panic in %s: %v", info.FullMethod, recovered)

				err = status.Error(codes.Internal, "internal error")
			}
		}()

		return handler(ctx, request)
	}
}

//
// SERVER
//

type Server struct {
	notesv1.UnimplementedNotesServiceServer

	service *notes.Service
}

func NewServer(service *notes.Service) *Server {
	return &Server{service: service}
}

var _ notesv1.NotesServiceServer = (*Server)(nil)

func (s *Server) CreateNote(ctx context.Context, request *notesv1.CreateNoteRequest) (*notesv1.CreateNoteResponse, error) {
	note, err := s.service.Create(ctx, UserFrom(ctx), request.GetTitle(), request.GetBody())
	if err != nil {
		return nil, toStatus(err)
	}

	return &notesv1.CreateNoteResponse{Note: toProto(note)}, nil
}

func (s *Server) GetNote(ctx context.Context, request *notesv1.GetNoteRequest) (*notesv1.GetNoteResponse, error) {
	note, err := s.service.Get(ctx, UserFrom(ctx), request.GetId())
	if err != nil {
		return nil, toStatus(err)
	}

	return &notesv1.GetNoteResponse{Note: toProto(note)}, nil
}

func (s *Server) ListNotes(ctx context.Context, request *notesv1.ListNotesRequest) (*notesv1.ListNotesResponse, error) {
	found, total, err := s.service.List(ctx, UserFrom(ctx), request.GetPageSize(), request.GetIncludeArchived())
	if err != nil {
		return nil, toStatus(err)
	}

	response := &notesv1.ListNotesResponse{
		Notes:     make([]*notesv1.Note, 0, len(found)),
		TotalSize: total,
	}

	for _, note := range found {
		response.Notes = append(response.Notes, toProto(note))
	}

	return response, nil
}

func (s *Server) ArchiveNote(ctx context.Context, request *notesv1.ArchiveNoteRequest) (*notesv1.ArchiveNoteResponse, error) {
	note, err := s.service.Archive(ctx, UserFrom(ctx), request.GetId())
	if err != nil {
		return nil, toStatus(err)
	}

	return &notesv1.ArchiveNoteResponse{Note: toProto(note)}, nil
}

func (s *Server) DeleteNote(ctx context.Context, request *notesv1.DeleteNoteRequest) (*notesv1.DeleteNoteResponse, error) {
	deleted, err := s.service.Delete(ctx, UserFrom(ctx), request.GetId())
	if err != nil {
		return nil, toStatus(err)
	}

	return &notesv1.DeleteNoteResponse{Deleted: deleted}, nil
}

func toProto(note notes.Note) *notesv1.Note {
	return &notesv1.Note{
		Id:        note.ID,
		OwnerId:   note.OwnerID,
		Title:     note.Title,
		Body:      note.Body,
		Archived:  note.Archived,
		CreatedAt: timestamppb.New(note.CreatedAt),
	}
}

// toStatus is the gRPC half of the error mapping. The REST half is in
// restapi.respondError, and the parity test asserts they agree.
func toStatus(err error) error {
	switch {
	case errors.Is(err, notes.ErrNotFound):
		return status.Error(codes.NotFound, "not found")

	case errors.Is(err, notes.ErrForbidden):
		return status.Error(codes.PermissionDenied, "not your note")

	case errors.Is(err, notes.ErrValidation):
		return status.Error(codes.InvalidArgument, err.Error())

	case errors.Is(err, notes.ErrUnauthenticated):
		return status.Error(codes.Unauthenticated, "no identity")

	default:
		log.Printf("internal error: %v", err)

		return status.Error(codes.Internal, "internal error")
	}
}
