// Package servers holds the two service implementations used to demonstrate
// metadata propagation: a public edge service and an internal profile service.
package servers

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	greetingv1 "example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-64/gen/greeting/v1"
	"example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-64/internal/interceptor"
)

//
// PROFILE SERVICE (internal)
//

type ProfileServer struct {
	greetingv1.UnimplementedProfileServiceServer

	// Stored as pointers: a protobuf message contains internal state and
	// must never be copied by value.
	profiles map[string]*greetingv1.GetProfileResponse
}

func NewProfileServer() *ProfileServer {
	return &ProfileServer{
		profiles: map[string]*greetingv1.GetProfileResponse{
			"u-1": {UserId: "u-1", DisplayName: "Ada Lovelace", Tier: "gold"},
			"u-2": {UserId: "u-2", DisplayName: "Alan Turing", Tier: "silver"},
		},
	}
}

var _ greetingv1.ProfileServiceServer = (*ProfileServer)(nil)

// GetProfile trusts the identity that the edge service propagated, and refuses
// to serve a profile that does not belong to the caller.
//
// The authorization decision needs the identity, and the identity only exists
// here because the client interceptor copied it into outgoing metadata.
func (s *ProfileServer) GetProfile(ctx context.Context, request *greetingv1.GetProfileRequest) (*greetingv1.GetProfileResponse, error) {
	identity, ok := interceptor.IdentityFrom(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no propagated identity")
	}

	if identity.UserID != request.GetUserId() && identity.Role != "admin" {
		return nil, status.Errorf(codes.PermissionDenied,
			"user %s may not read the profile of %s", identity.UserID, request.GetUserId())
	}

	profile, found := s.profiles[request.GetUserId()]
	if !found {
		return nil, status.Errorf(codes.NotFound, "profile %s not found", request.GetUserId())
	}

	// Clone before returning: handing out the stored message would let a
	// caller mutate this server's state.
	return proto.Clone(profile).(*greetingv1.GetProfileResponse), nil
}

//
// EDGE SERVICE (public)
//

type EdgeServer struct {
	greetingv1.UnimplementedEdgeServiceServer

	profiles greetingv1.ProfileServiceClient
}

func NewEdgeServer(profiles greetingv1.ProfileServiceClient) *EdgeServer {
	return &EdgeServer{profiles: profiles}
}

var _ greetingv1.EdgeServiceServer = (*EdgeServer)(nil)

func (s *EdgeServer) Greet(ctx context.Context, request *greetingv1.GreetRequest) (*greetingv1.GreetResponse, error) {
	identity, ok := interceptor.IdentityFrom(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no identity")
	}

	// The outgoing call carries the same ctx, and the client interceptor turns
	// its request id and identity into metadata. Passing context.Background()
	// here instead would silently break tracing and internal authorization -
	// and nothing would fail until someone tried to debug a production issue.
	profile, err := s.profiles.GetProfile(ctx, &greetingv1.GetProfileRequest{UserId: identity.UserID})
	if err != nil {
		// The downstream status is already meaningful; wrap the message but
		// keep the code so the caller sees PermissionDenied as
		// PermissionDenied, not as Internal.
		if statusError, ok := status.FromError(err); ok {
			return nil, status.Errorf(statusError.Code(), "profile lookup: %s", statusError.Message())
		}

		return nil, status.Error(codes.Internal, "profile lookup failed")
	}

	name := request.GetName()

	if name == "" {
		name = profile.GetDisplayName()
	}

	return &greetingv1.GreetResponse{
		Message:      fmt.Sprintf("Hello %s, you are on the %s tier", name, profile.GetTier()),
		RequestId:    interceptor.RequestIDFrom(ctx),
		ResolvedUser: profile.GetUserId(),
	}, nil
}

// Panic exists to prove the recovery interceptor turns a crash into a status.
func (s *EdgeServer) Panic(ctx context.Context, request *greetingv1.PanicRequest) (*greetingv1.PanicResponse, error) {
	panic(errors.New("a handler panicked on purpose"))
}
