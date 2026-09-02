package interceptor

import (
	"context"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

/*
Client interceptors.

They wrap outgoing calls the way server interceptors wrap incoming ones, and
they are where cross-cutting client concerns belong: attaching credentials,
carrying the request id forward, logging, retrying.
*/

// AuthClient attaches a bearer token to every outgoing call, so no call site
// has to remember to.
func AuthClient(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, request, response any,
		connection *grpc.ClientConn, invoker grpc.UnaryInvoker, options ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, AuthorizationKey, "Bearer "+token)

		return invoker(ctx, method, request, response, connection, options...)
	}
}

// PropagateClient carries the incoming request's id and identity onto the
// outgoing call.
//
// This is the crux of distributed tracing and of internal authorization: a
// context does NOT travel across a network on its own. Incoming metadata must
// be explicitly copied into the outgoing metadata, or every hop starts a new
// story.
func PropagateClient() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, request, response any,
		connection *grpc.ClientConn, invoker grpc.UnaryInvoker, options ...grpc.CallOption) error {
		if requestID := RequestIDFrom(ctx); requestID != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, RequestIDKey, requestID)
		}

		if identity, ok := IdentityFrom(ctx); ok {
			ctx = metadata.AppendToOutgoingContext(ctx,
				UserIDKey, identity.UserID,
				RoleKey, identity.Role,
			)
		}

		return invoker(ctx, method, request, response, connection, options...)
	}
}

// LoggingClient logs the outgoing call and what came back, including the
// request id the server reported in its headers.
func LoggingClient(prefix string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, request, response any,
		connection *grpc.ClientConn, invoker grpc.UnaryInvoker, options ...grpc.CallOption) error {
		start := time.Now()

		var header metadata.MD

		options = append(options, grpc.Header(&header))

		err := invoker(ctx, method, request, response, connection, options...)

		serverRequestID := "-"

		if values := header.Get(RequestIDKey); len(values) > 0 {
			serverRequestID = values[0]
		}

		log.Printf("%s method=%s code=%s duration=%s request_id=%s",
			prefix, method, status.Code(err),
			time.Since(start).Round(time.Microsecond), serverRequestID)

		return err
	}
}

// TimeoutClient applies a default deadline to calls that arrive without one.
//
// A gRPC call with no deadline can wait forever. This is the safety net for
// the call site that forgot; it never shortens a deadline the caller set.
func TimeoutClient(timeout time.Duration) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, request, response any,
		connection *grpc.ClientConn, invoker grpc.UnaryInvoker, options ...grpc.CallOption) error {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			var cancel context.CancelFunc

			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		return invoker(ctx, method, request, response, connection, options...)
	}
}

// WithIdentity puts an identity into a client-side context, so a program that
// is not behind a server interceptor (a CLI, a test) can still propagate one.
func WithIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, identityContextKey, identity)
}

// WithRequestID does the same for the request id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDContextKey, id)
}
