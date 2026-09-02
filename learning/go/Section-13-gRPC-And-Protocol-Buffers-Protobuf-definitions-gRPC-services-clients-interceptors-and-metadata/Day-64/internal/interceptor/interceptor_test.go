package interceptor_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	greetingv1 "example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-64/gen/greeting/v1"
	"example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-64/internal/interceptor"
	"example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-64/internal/servers"
)

/*
Interceptor tests.

Auth bugs in RPCs are as serious as in REST, and an interceptor is easy to
disable by accident - a reordered chain, a method added to the wrong skip
list. These tests assert the security behaviour directly:

  - a call with no credentials is rejected
  - a call with bad credentials is rejected
  - a valid identity without the role is rejected differently (PermissionDenied)
  - identity and request id really do cross the service boundary
  - a panic becomes a status instead of a crash
*/

var validTokens = map[string]interceptor.Identity{
	"member-token": {UserID: "u-1", Role: "member"},
	"other-token":  {UserID: "u-2", Role: "member"},
	"admin-token":  {UserID: "u-1", Role: "admin"},
}

func validate(token string) (interceptor.Identity, error) {
	identity, found := validTokens[token]
	if !found {
		return interceptor.Identity{}, errors.New("unknown token")
	}

	return identity, nil
}

// stack builds the two-service topology used by the tests and returns a client
// factory so each case can choose its token.
func stack(t *testing.T) func(token string) greetingv1.EdgeServiceClient {
	t.Helper()

	profileListener := bufconn.Listen(1024 * 1024)

	profileServer := grpc.NewServer(grpc.ChainUnaryInterceptor(
		interceptor.RequestIDUnaryServer(),
		interceptor.RecoveryUnaryServer(),
		interceptor.TrustedIdentityUnaryServer(),
	))

	greetingv1.RegisterProfileServiceServer(profileServer, servers.NewProfileServer())

	go func() {
		if err := profileServer.Serve(profileListener); err != nil {
			t.Logf("profile serve: %v", err)
		}
	}()

	profileConnection, err := grpc.NewClient("passthrough:///profile",
		grpc.WithContextDialer(dial(profileListener)),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(interceptor.PropagateClient()),
	)
	if err != nil {
		t.Fatalf("profile client: %v", err)
	}

	edgeListener := bufconn.Listen(1024 * 1024)

	edgeServer := grpc.NewServer(grpc.ChainUnaryInterceptor(
		interceptor.RequestIDUnaryServer(),
		interceptor.RecoveryUnaryServer(),
		interceptor.AuthUnaryServer(validate),
		interceptor.RequireRole("admin", "/greeting.v1.EdgeService/Panic"),
	))

	greetingv1.RegisterEdgeServiceServer(edgeServer,
		servers.NewEdgeServer(greetingv1.NewProfileServiceClient(profileConnection)))

	go func() {
		if err := edgeServer.Serve(edgeListener); err != nil {
			t.Logf("edge serve: %v", err)
		}
	}()

	var connections []*grpc.ClientConn

	t.Cleanup(func() {
		for _, connection := range connections {
			if err := connection.Close(); err != nil {
				t.Errorf("close connection: %v", err)
			}
		}

		if err := profileConnection.Close(); err != nil {
			t.Errorf("close profile connection: %v", err)
		}

		edgeServer.Stop()
		profileServer.Stop()
	})

	return func(token string) greetingv1.EdgeServiceClient {
		options := []grpc.DialOption{
			grpc.WithContextDialer(dial(edgeListener)),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		}

		if token != "" {
			options = append(options, grpc.WithChainUnaryInterceptor(interceptor.AuthClient(token)))
		}

		connection, err := grpc.NewClient("passthrough:///edge", options...)
		if err != nil {
			t.Fatalf("edge client: %v", err)
		}

		connections = append(connections, connection)

		return greetingv1.NewEdgeServiceClient(connection)
	}
}

func dial(listener *bufconn.Listener) func(context.Context, string) (net.Conn, error) {
	return func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.DialContext(ctx)
	}
}

func TestUnauthenticatedCallsAreRejected(t *testing.T) {
	t.Parallel()

	newClient := stack(t)

	tests := []struct {
		name  string
		token string
	}{
		{"no credentials", ""},
		{"unknown token", "made-up"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := newClient(test.token).Greet(context.Background(), &greetingv1.GreetRequest{})

			if status.Code(err) != codes.Unauthenticated {
				t.Fatalf("code = %s, want Unauthenticated (err=%v)", status.Code(err), err)
			}
		})
	}
}

func TestMalformedAuthorizationHeader(t *testing.T) {
	t.Parallel()

	newClient := stack(t)
	client := newClient("")

	tests := map[string]string{
		"no scheme":    "member-token",
		"wrong scheme": "Basic member-token",
		"empty token":  "Bearer ",
	}

	for name, header := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := metadata.AppendToOutgoingContext(context.Background(),
				interceptor.AuthorizationKey, header)

			_, err := client.Greet(ctx, &greetingv1.GreetRequest{})

			if status.Code(err) != codes.Unauthenticated {
				t.Fatalf("code = %s, want Unauthenticated", status.Code(err))
			}
		})
	}
}

func TestAuthenticatedCallSucceeds(t *testing.T) {
	t.Parallel()

	newClient := stack(t)

	response, err := newClient("member-token").Greet(context.Background(), &greetingv1.GreetRequest{})
	if err != nil {
		t.Fatalf("greet: %v", err)
	}

	if response.GetResolvedUser() != "u-1" {
		t.Fatalf("resolved user = %q, want u-1", response.GetResolvedUser())
	}
}

// TestRoleCheckIsPermissionDenied: authenticated but not allowed must be a
// different code from not authenticated, or a client cannot tell "log in"
// from "you may not".
func TestRoleCheckIsPermissionDenied(t *testing.T) {
	t.Parallel()

	newClient := stack(t)

	_, err := newClient("member-token").Panic(context.Background(), &greetingv1.PanicRequest{})

	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code = %s, want PermissionDenied", status.Code(err))
	}
}

// TestPanicBecomesInternal: the recovery interceptor keeps the server alive,
// and the caller gets a code rather than a dropped connection.
func TestPanicBecomesInternal(t *testing.T) {
	t.Parallel()

	newClient := stack(t)
	client := newClient("admin-token")

	_, err := client.Panic(context.Background(), &greetingv1.PanicRequest{})

	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %s, want Internal", status.Code(err))
	}

	// The message must not leak the panic value or a stack trace.
	if message := status.Convert(err).Message(); message != "internal error" {
		t.Fatalf("message = %q, want a generic one", message)
	}

	// And the server still works afterwards.
	if _, err := client.Greet(context.Background(), &greetingv1.GreetRequest{}); err != nil {
		t.Fatalf("the server did not survive the panic: %v", err)
	}
}

// TestRequestIDIsGeneratedAndReturned: a caller that sends no id gets one back
// in the response headers, so its logs can reference the same call.
func TestRequestIDIsGeneratedAndReturned(t *testing.T) {
	t.Parallel()

	newClient := stack(t)

	var header metadata.MD

	response, err := newClient("member-token").Greet(context.Background(),
		&greetingv1.GreetRequest{}, grpc.Header(&header))
	if err != nil {
		t.Fatalf("greet: %v", err)
	}

	if response.GetRequestId() == "" {
		t.Fatal("no request id was generated")
	}

	values := header.Get(interceptor.RequestIDKey)

	if len(values) == 0 || values[0] != response.GetRequestId() {
		t.Fatalf("header request id = %v, response = %q", values, response.GetRequestId())
	}
}

// TestRequestIDIsHonoured: a caller-supplied id must be used as-is, or a trace
// that started upstream is broken at this hop.
func TestRequestIDIsHonoured(t *testing.T) {
	t.Parallel()

	newClient := stack(t)

	ctx := metadata.AppendToOutgoingContext(context.Background(),
		interceptor.RequestIDKey, "caller-supplied-id")

	response, err := newClient("member-token").Greet(ctx, &greetingv1.GreetRequest{})
	if err != nil {
		t.Fatalf("greet: %v", err)
	}

	if response.GetRequestId() != "caller-supplied-id" {
		t.Fatalf("request id = %q, want the caller's", response.GetRequestId())
	}
}

// TestIdentityPropagatesDownstream is the metadata test that matters: the
// internal service authorizes using an identity it never authenticated,
// because the edge forwarded it.
func TestIdentityPropagatesDownstream(t *testing.T) {
	t.Parallel()

	newClient := stack(t)

	// u-2 asks for a greeting; the edge asks the profile service for u-2's
	// profile. The profile service allows it only because x-user-id arrived.
	response, err := newClient("other-token").Greet(context.Background(),
		&greetingv1.GreetRequest{Name: "Alan"})
	if err != nil {
		t.Fatalf("greet: %v", err)
	}

	if response.GetResolvedUser() != "u-2" {
		t.Fatalf("resolved user = %q, want u-2", response.GetResolvedUser())
	}
}

// TestInternalServiceRejectsMissingIdentity: without propagation, the internal
// service refuses. This is the failure a forgotten PropagateClient produces.
func TestInternalServiceRejectsMissingIdentity(t *testing.T) {
	t.Parallel()

	listener := bufconn.Listen(1024 * 1024)

	server := grpc.NewServer(grpc.ChainUnaryInterceptor(
		interceptor.RequestIDUnaryServer(),
		interceptor.TrustedIdentityUnaryServer(),
	))

	greetingv1.RegisterProfileServiceServer(server, servers.NewProfileServer())

	go func() {
		if err := server.Serve(listener); err != nil {
			t.Logf("serve: %v", err)
		}
	}()

	connection, err := grpc.NewClient("passthrough:///profile",
		grpc.WithContextDialer(dial(listener)),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close: %v", err)
		}

		server.Stop()
	})

	_, err = greetingv1.NewProfileServiceClient(connection).GetProfile(context.Background(),
		&greetingv1.GetProfileRequest{UserId: "u-1"})

	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("code = %s, want Unauthenticated", status.Code(err))
	}
}

// TestClientTimeoutInterceptorAppliesADefault: a call made without a deadline
// still gets one.
func TestClientTimeoutInterceptorAppliesADefault(t *testing.T) {
	t.Parallel()

	listener := bufconn.Listen(1024 * 1024)

	server := grpc.NewServer(grpc.UnaryInterceptor(
		func(ctx context.Context, request any, info *grpc.UnaryServerInfo,
			handler grpc.UnaryHandler) (any, error) {
			if _, hasDeadline := ctx.Deadline(); !hasDeadline {
				return nil, status.Error(codes.FailedPrecondition, "no deadline arrived at the server")
			}

			// Outlive the client's deadline on purpose.
			time.Sleep(200 * time.Millisecond)

			return handler(ctx, request)
		},
	))

	greetingv1.RegisterProfileServiceServer(server, servers.NewProfileServer())

	go func() {
		if err := server.Serve(listener); err != nil {
			t.Logf("serve: %v", err)
		}
	}()

	connection, err := grpc.NewClient("passthrough:///profile",
		grpc.WithContextDialer(dial(listener)),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(interceptor.TimeoutClient(20*time.Millisecond)),
	)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close: %v", err)
		}

		server.Stop()
	})

	_, err = greetingv1.NewProfileServiceClient(connection).GetProfile(context.Background(),
		&greetingv1.GetProfileRequest{UserId: "u-1"})

	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("code = %s, want DeadlineExceeded", status.Code(err))
	}
}
