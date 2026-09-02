# Day 65 — Notes over gRPC *and* HTTP

Section 13 capstone. One service layer, two transports, one set of rules.

```
                  ┌────────────────────┐
   gRPC :9090 ───►│  transport/grpcapi │──┐
                  └────────────────────┘  │     ┌──────────────────┐
                                          ├────►│  internal/notes  │
                  ┌────────────────────┐  │     │  (the only place │
   HTTP :8080 ───►│  transport/restapi │──┘     │   with rules)    │
                  └────────────────────┘        └──────────────────┘
```

Neither transport contains a business rule. Both call the same
`*notes.Service`, and `internal/transport/parity_test.go` fails if they ever
start disagreeing.

## Generating the stubs

The `.proto` file is the source of truth. After **any** change to
`proto/notes/v1/notes.proto`, regenerate:

```bash
# one-time toolchain install
go install github.com/bufbuild/buf/cmd/buf@latest
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# from this directory
buf lint        # style and naming checks
buf generate    # writes gen/notes/v1/*.pb.go
go build ./...
```

`buf` compiles protobuf itself — no system `protoc` needed. With `protoc`
instead:

```bash
protoc -I proto \
  --go_out=gen --go_opt=paths=source_relative \
  --go-grpc_out=gen --go-grpc_opt=paths=source_relative \
  proto/notes/v1/notes.proto
```

Before opening a PR, check that the change cannot break deployed clients:

```bash
buf breaking --against '.git#branch=main'
```

**Generated code is committed.** `gen/` is checked in so `go build ./...`
works on a clean checkout without the plugin toolchain. Never edit it by hand;
regenerate and commit the result in the same change as the `.proto`.

## Running locally

```bash
go run ./cmd/server      # gRPC on :9090, HTTP on :8080
go run ./cmd/client      # calls both and prints them side by side
```

Demo tokens: `ada-token` → user `u-1`, `alan-token` → user `u-2`.

### gRPC

```bash
# server reflection is on, so grpcurl needs no .proto
grpcurl -plaintext localhost:9090 list
grpcurl -plaintext localhost:9090 describe notes.v1.NotesService

grpcurl -plaintext \
  -H 'authorization: Bearer ada-token' \
  -d '{"title":"From grpcurl","body":"hello"}' \
  localhost:9090 notes.v1.NotesService/CreateNote

grpcurl -plaintext -H 'authorization: Bearer ada-token' \
  -d '{}' localhost:9090 notes.v1.NotesService/ListNotes
```

### HTTP

```bash
curl -XPOST localhost:8080/notes \
  -H 'Authorization: Bearer ada-token' \
  -d '{"title":"From curl","body":"hello"}'

curl localhost:8080/notes -H 'Authorization: Bearer ada-token'
curl localhost:8080/notes/1 -H 'Authorization: Bearer ada-token'
curl -XPOST localhost:8080/notes/1/archive -H 'Authorization: Bearer ada-token'
curl -XDELETE localhost:8080/notes/1 -H 'Authorization: Bearer ada-token'
```

A note created over one transport is immediately visible over the other — the
two servers share one service instance.

## Endpoint parity

| Operation | gRPC | HTTP |
|---|---|---|
| Create | `NotesService/CreateNote` | `POST /notes` |
| Read one | `NotesService/GetNote` | `GET /notes/{id}` |
| List | `NotesService/ListNotes` | `GET /notes?page_size=&include_archived=` |
| Archive | `NotesService/ArchiveNote` | `POST /notes/{id}/archive` |
| Delete | `NotesService/DeleteNote` | `DELETE /notes/{id}` |

### Error parity

| Situation | gRPC code | HTTP status |
|---|---|---|
| No credentials | `UNAUTHENTICATED` | `401` |
| Invalid token | `UNAUTHENTICATED` | `401` |
| Someone else's note | `PERMISSION_DENIED` | `403` |
| Unknown note | `NOT_FOUND` | `404` |
| Validation failure | `INVALID_ARGUMENT` | `422` |
| Panic in a handler | `INTERNAL` | `500` |

Both tables are asserted by `TestErrorParity`. Adding a rule to one transport
and forgetting the other is a test failure, not a support ticket.

## Cross-cutting concerns

| Concern | gRPC | HTTP |
|---|---|---|
| Authentication | `grpcapi.AuthInterceptor` | `restapi.authMiddleware` |
| Logging | `grpcapi.LoggingInterceptor` | `restapi.logging` |
| Panic recovery | `grpcapi.RecoveryInterceptor` | (add one; `http.Server` already isolates a panicking handler's connection) |
| Credentials on the wire | `authorization` metadata | `Authorization` header |

Both call the **same** `Authenticator` function, so there is one definition of
"who is this caller?".

## Choosing a transport

- **gRPC** for service-to-service: smaller payloads, HTTP/2 multiplexing,
  generated clients in every language, deadlines and status codes built in.
- **HTTP/JSON** for browsers, webhooks, curl, and anything that must be
  debuggable by a human with a terminal.
- **Both**, as here, when a public API and internal callers have different
  needs. The cost is one adapter per transport — acceptable only because the
  rules live below them.

## Tests

```bash
go test ./...
go test -race -count=1 ./...
go test -run TestErrorParity -v ./internal/transport
```

`internal/transport/parity_test.go` runs both transports against one service
over `bufconn` and an `httptest` server: no ports, no fixtures, no sleeping.
