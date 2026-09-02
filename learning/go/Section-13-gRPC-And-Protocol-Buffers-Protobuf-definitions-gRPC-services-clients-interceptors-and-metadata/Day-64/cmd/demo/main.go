// Command demo runs both services in one process, over in-memory listeners,
// and drives them through the interceptor stack so the logs show what each
// interceptor did.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	greetingv1 "example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-64/gen/greeting/v1"
	"example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-64/internal/interceptor"
	"example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-64/internal/servers"
)

/*
Day 64 - gRPC & Protocol Buffers: Interceptors and Metadata

Tasks covered:

 1. Server interceptors: request id, logging, recovery, authentication,
    role-based authorization (internal/interceptor/server.go)
 2. Client interceptors: attach credentials, carry the request id, log, apply
    a default deadline (internal/interceptor/client.go)
 3. Metadata propagated from the edge service to the internal one, which is
    what makes tracing and internal authorization work at all
 4. Interceptors tested, including the rejected unauthenticated call
    (internal/interceptor/interceptor_test.go)

Run:

	go run ./cmd/demo

Test:

	go test ./...

The two services talk over bufconn, so the whole demo runs in one process with
no ports. The interceptor behaviour is identical over a real network.
*/

// Tokens the demo validator understands. A real one verifies a JWT.
var tokens = map[string]interceptor.Identity{
	"ada-token":   {UserID: "u-1", Role: "member"},
	"alan-token":  {UserID: "u-2", Role: "member"},
	"admin-token": {UserID: "u-1", Role: "admin"},
}

func validateToken(token string) (interceptor.Identity, error) {
	identity, found := tokens[token]
	if !found {
		return interceptor.Identity{}, errors.New("unknown token")
	}

	return identity, nil
}

func main() {
	log.SetFlags(log.Ltime | log.Lmsgprefix)
	log.SetPrefix("day64 ")

	if err := run(); err != nil {
		log.Fatalf("%v", err)
	}
}

func run() error {
	//
	// The internal profile service. It authenticates nobody: it trusts the
	// identity the edge propagated, which is only acceptable because it is
	// unreachable from outside.
	//

	profileListener := bufconn.Listen(1024 * 1024)

	profileServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			interceptor.RequestIDUnaryServer(),
			interceptor.RecoveryUnaryServer(),
			interceptor.TrustedIdentityUnaryServer(),
			interceptor.LoggingUnaryServer("profile"),
		),
	)

	greetingv1.RegisterProfileServiceServer(profileServer, servers.NewProfileServer())

	go func() {
		if err := profileServer.Serve(profileListener); err != nil {
			log.Printf("profile server stopped: %v", err)
		}
	}()

	defer profileServer.Stop()

	// The edge's client of the profile service. PropagateClient is what
	// carries the request id and the identity across the boundary.
	profileConnection, err := grpc.NewClient("passthrough:///profile",
		grpc.WithContextDialer(dialer(profileListener)),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(
			interceptor.PropagateClient(),
			interceptor.TimeoutClient(5*time.Second),
		),
	)
	if err != nil {
		return err
	}

	defer func() {
		if err := profileConnection.Close(); err != nil {
			log.Printf("close profile connection: %v", err)
		}
	}()

	//
	// The public edge service.
	//
	// Interceptor order is the order they run, outermost first:
	//   request id -> recovery -> logging -> auth -> role check -> handler
	// Logging sits outside auth so rejected calls are logged too; recovery
	// sits outside logging so a panic still produces a log line.
	//

	edgeListener := bufconn.Listen(1024 * 1024)

	edgeServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			interceptor.RequestIDUnaryServer(),
			interceptor.RecoveryUnaryServer(),
			interceptor.LoggingUnaryServer("edge   "),
			interceptor.AuthUnaryServer(validateToken),
			interceptor.RequireRole("admin", "/greeting.v1.EdgeService/Panic"),
		),
	)

	greetingv1.RegisterEdgeServiceServer(edgeServer,
		servers.NewEdgeServer(greetingv1.NewProfileServiceClient(profileConnection)))

	go func() {
		if err := edgeServer.Serve(edgeListener); err != nil {
			log.Printf("edge server stopped: %v", err)
		}
	}()

	defer edgeServer.Stop()

	//
	// A client of the edge, with its own interceptors.
	//

	client, closeClient, err := newEdgeClient(edgeListener, "ada-token")
	if err != nil {
		return err
	}

	defer closeClient()

	ctx := context.Background()

	fmt.Println("\n1) A call that flows through both services")
	fmt.Println(strings.Repeat("-", 78))

	response, err := client.Greet(ctx, &greetingv1.GreetRequest{})
	if err != nil {
		return fmt.Errorf("greet: %w", err)
	}

	fmt.Printf("\n  message      : %s\n", response.GetMessage())
	fmt.Printf("  request id   : %s\n", response.GetRequestId())
	fmt.Printf("  resolved user: %s\n", response.GetResolvedUser())
	fmt.Println("  The same request id appears in the edge log line and the profile")
	fmt.Println("  log line above: it was propagated as metadata, not guessed.")

	fmt.Println("\n2) Missing and invalid credentials")
	fmt.Println(strings.Repeat("-", 78))

	anonymous, closeAnonymous, err := newAnonymousClient(edgeListener)
	if err != nil {
		return err
	}

	defer closeAnonymous()

	_, err = anonymous.Greet(ctx, &greetingv1.GreetRequest{})
	fmt.Printf("\n  no token        -> %s\n", describe(err))

	badClient, closeBad, err := newEdgeClient(edgeListener, "made-up-token")
	if err != nil {
		return err
	}

	defer closeBad()

	_, err = badClient.Greet(ctx, &greetingv1.GreetRequest{})
	fmt.Printf("  invalid token   -> %s\n", describe(err))

	fmt.Println("\n3) Authorization: a valid identity that is still refused")
	fmt.Println(strings.Repeat("-", 78))

	_, err = client.Panic(ctx, &greetingv1.PanicRequest{})
	fmt.Printf("\n  member calls an admin-only RPC -> %s\n", describe(err))

	fmt.Println("\n4) Recovery: a panicking handler")
	fmt.Println(strings.Repeat("-", 78))

	adminClient, closeAdmin, err := newEdgeClient(edgeListener, "admin-token")
	if err != nil {
		return err
	}

	defer closeAdmin()

	_, err = adminClient.Panic(ctx, &greetingv1.PanicRequest{})
	fmt.Printf("\n  admin calls Panic -> %s\n", describe(err))
	fmt.Println("  The process is still running: the panic became a status code.")

	fmt.Println("\n5) Downstream authorization through propagated identity")
	fmt.Println(strings.Repeat("-", 78))

	// alan (u-2) asks the edge for a greeting; the edge asks the profile
	// service for u-2's profile, and the propagated identity is what lets the
	// internal service allow it.
	alanClient, closeAlan, err := newEdgeClient(edgeListener, "alan-token")
	if err != nil {
		return err
	}

	defer closeAlan()

	alanResponse, err := alanClient.Greet(ctx, &greetingv1.GreetRequest{Name: "Alan"})
	if err != nil {
		return fmt.Errorf("greet as alan: %w", err)
	}

	fmt.Printf("\n  %s\n", alanResponse.GetMessage())
	fmt.Println("  The profile service never saw a token - only x-user-id and")
	fmt.Println("  x-user-role, forwarded by the edge's client interceptor.")

	fmt.Println()

	return nil
}

func newEdgeClient(listener *bufconn.Listener, token string) (greetingv1.EdgeServiceClient, func(), error) {
	connection, err := grpc.NewClient("passthrough:///edge",
		grpc.WithContextDialer(dialer(listener)),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(
			interceptor.AuthClient(token),
			interceptor.LoggingClient("client "),
			interceptor.TimeoutClient(5*time.Second),
		),
	)
	if err != nil {
		return nil, nil, err
	}

	return greetingv1.NewEdgeServiceClient(connection), func() {
		if err := connection.Close(); err != nil {
			log.Printf("close client: %v", err)
		}
	}, nil
}

func newAnonymousClient(listener *bufconn.Listener) (greetingv1.EdgeServiceClient, func(), error) {
	connection, err := grpc.NewClient("passthrough:///edge",
		grpc.WithContextDialer(dialer(listener)),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, nil, err
	}

	return greetingv1.NewEdgeServiceClient(connection), func() {
		if err := connection.Close(); err != nil {
			log.Printf("close client: %v", err)
		}
	}, nil
}

func dialer(listener *bufconn.Listener) func(context.Context, string) (net.Conn, error) {
	return func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.DialContext(ctx)
	}
}

func describe(err error) string {
	if err == nil {
		return "OK"
	}

	statusError, ok := status.FromError(err)
	if !ok {
		return err.Error()
	}

	if statusError.Code() == codes.OK {
		return "OK"
	}

	return fmt.Sprintf("%-18s %s", statusError.Code(), statusError.Message())
}
