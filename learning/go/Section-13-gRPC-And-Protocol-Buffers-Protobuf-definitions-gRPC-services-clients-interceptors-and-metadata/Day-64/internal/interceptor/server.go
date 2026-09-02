// Package interceptor holds the gRPC middleware for both sides of a call.
//
// Server interceptors are HTTP middleware for gRPC: they wrap the handler, so
// logging, authentication, recovery and metrics are written once instead of
// at the top of every method.
package interceptor

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Metadata keys. gRPC lowercases every key, so a lookup with a capital letter
// silently finds nothing - always use lower case constants like these.
const (
	AuthorizationKey = "authorization"
	RequestIDKey     = "x-request-id"
	UserIDKey        = "x-user-id"
	RoleKey          = "x-user-role"
)

type contextKey string

const (
	requestIDContextKey contextKey = "request_id"
	identityContextKey  contextKey = "identity"
	identityHolderKey   contextKey = "identity_holder"
)

// identityHolder is a mutable box installed by the outermost interceptor and
// filled in by the auth interceptor.
//
// Context values are immutable: an inner interceptor cannot add one that an
// outer interceptor will see. Logging runs outside auth (so rejected calls are
// still logged), yet it wants the identity that auth resolved - a pointer in
// the context is the standard way to pass a value back outward.
type identityHolder struct {
	identity Identity
	set      bool
}

func setResolvedIdentity(ctx context.Context, identity Identity) {
	if holder, ok := ctx.Value(identityHolderKey).(*identityHolder); ok {
		holder.identity = identity
		holder.set = true
	}
}

// ResolvedIdentity reports the identity authentication produced, readable from
// an interceptor that ran before it.
func ResolvedIdentity(ctx context.Context) (Identity, bool) {
	if holder, ok := ctx.Value(identityHolderKey).(*identityHolder); ok && holder.set {
		return holder.identity, true
	}

	return IdentityFrom(ctx)
}

// Identity is what authentication produces and authorization consumes.
type Identity struct {
	UserID string
	Role   string
}

func IdentityFrom(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityContextKey).(Identity)

	return identity, ok
}

func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDContextKey).(string)

	return id
}

//
// REQUEST ID
//

// RequestIDUnaryServer takes the caller's request id or mints one, and puts it
// in the context so every later interceptor and the handler can log it.
//
// Without a shared id, a failure spread over three services is three unrelated
// log lines.
func RequestIDUnaryServer() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {
		id := firstMetadataValue(ctx, RequestIDKey)

		if id == "" {
			generated, err := randomID()
			if err != nil {
				return nil, status.Error(codes.Internal, "could not generate a request id")
			}

			id = generated
		}

		ctx = context.WithValue(ctx, requestIDContextKey, id)

		// The box every later interceptor can write the identity into.
		ctx = context.WithValue(ctx, identityHolderKey, &identityHolder{})

		// Send it back to the caller too, so a client can log the id of a
		// request it did not generate.
		if err := grpc.SetHeader(ctx, metadata.Pairs(RequestIDKey, id)); err != nil {
			log.Printf("set request id header: %v", err)
		}

		return handler(ctx, request)
	}
}

//
// LOGGING
//

// LoggingUnaryServer logs one line per RPC: method, code, duration, request id.
//
// It logs the status code rather than the error text, because the code is what
// dashboards and alerts group by.
func LoggingUnaryServer(prefix string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {
		start := time.Now()

		response, err := handler(ctx, request)

		// ResolvedIdentity, not IdentityFrom: this interceptor runs outside
		// the auth one, so the identity arrives through the holder.
		identity, _ := ResolvedIdentity(ctx)

		log.Printf("%s method=%s code=%s duration=%s request_id=%s user=%s",
			prefix,
			info.FullMethod,
			status.Code(err),
			time.Since(start).Round(time.Microsecond),
			RequestIDFrom(ctx),
			orDash(identity.UserID),
		)

		return response, err
	}
}

//
// RECOVERY
//

// RecoveryUnaryServer turns a panic into an Internal status instead of killing
// the whole process - one bad request must not take the server down.
func RecoveryUnaryServer() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (response any, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				// The panic value goes to the log; the caller gets nothing
				// but the code, because a stack trace is an information leak.
				log.Printf("panic in %s request_id=%s: %v",
					info.FullMethod, RequestIDFrom(ctx), recovered)

				err = status.Error(codes.Internal, "internal error")
			}
		}()

		return handler(ctx, request)
	}
}

//
// AUTHENTICATION AND AUTHORIZATION
//

// TokenValidator resolves a bearer token into an identity. In a real service
// this verifies a JWT (Day 52) or calls an auth service.
type TokenValidator func(token string) (Identity, error)

// AuthUnaryServer rejects calls without valid credentials.
//
// skip lists full method names that are public - a health check must not need
// a token, and forgetting to exempt it is a classic outage.
func AuthUnaryServer(validate TokenValidator, skip ...string) grpc.UnaryServerInterceptor {
	exempt := make(map[string]struct{}, len(skip))

	for _, method := range skip {
		exempt[method] = struct{}{}
	}

	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {
		if _, public := exempt[info.FullMethod]; public {
			return handler(ctx, request)
		}

		header := firstMetadataValue(ctx, AuthorizationKey)

		if header == "" {
			// Unauthenticated: no credentials at all.
			return nil, status.Error(codes.Unauthenticated, "missing authorization metadata")
		}

		scheme, token, found := strings.Cut(header, " ")

		if !found || !strings.EqualFold(scheme, "bearer") || strings.TrimSpace(token) == "" {
			return nil, status.Error(codes.Unauthenticated, "authorization must be 'Bearer <token>'")
		}

		identity, err := validate(strings.TrimSpace(token))
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}

		setResolvedIdentity(ctx, identity)

		return handler(context.WithValue(ctx, identityContextKey, identity), request)
	}
}

// RequireRole is authorization: the caller is known, but may still be refused.
// PermissionDenied, not Unauthenticated - the same distinction as 403 vs 401.
func RequireRole(role string, methods ...string) grpc.UnaryServerInterceptor {
	guarded := make(map[string]struct{}, len(methods))

	for _, method := range methods {
		guarded[method] = struct{}{}
	}

	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {
		if _, checked := guarded[info.FullMethod]; !checked {
			return handler(ctx, request)
		}

		identity, ok := IdentityFrom(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "no identity on the request")
		}

		if identity.Role != role {
			return nil, status.Errorf(codes.PermissionDenied, "role %q may not call %s",
				identity.Role, info.FullMethod)
		}

		return handler(ctx, request)
	}
}

// TrustedIdentityUnaryServer is what an internal service uses instead of
// re-authenticating: it reads the identity the edge already verified and
// forwarded.
//
// This is only safe when the service is unreachable from outside, because
// anyone who can call it directly can claim to be anybody. Say that out loud
// in review; "internal only" is a network property, not a code property.
func TrustedIdentityUnaryServer() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {
		userID := firstMetadataValue(ctx, UserIDKey)

		if userID == "" {
			return nil, status.Error(codes.Unauthenticated, "no propagated identity")
		}

		identity := Identity{UserID: userID, Role: firstMetadataValue(ctx, RoleKey)}

		setResolvedIdentity(ctx, identity)

		return handler(context.WithValue(ctx, identityContextKey, identity), request)
	}
}

//
// HELPERS
//

func firstMetadataValue(ctx context.Context, key string) string {
	incoming, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}

	values := incoming.Get(key)
	if len(values) == 0 {
		return ""
	}

	return values[0]
}

func randomID() (string, error) {
	raw := make([]byte, 8)

	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate request id: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func orDash(value string) string {
	if value == "" {
		return "-"
	}

	return value
}
