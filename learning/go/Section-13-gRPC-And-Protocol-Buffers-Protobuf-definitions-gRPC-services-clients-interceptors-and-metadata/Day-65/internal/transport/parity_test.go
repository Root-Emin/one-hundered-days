package transport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	notesv1 "example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-65/gen/notes/v1"
	"example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-65/internal/notes"
	"example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-65/internal/transport/grpcapi"
	"example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-65/internal/transport/restapi"
)

/*
Parity tests.

Two transports over one service is only a win if they cannot drift. These
tests drive both against the SAME service instance and assert that:

  - data written through one is visible through the other
  - every failure maps to the matching pair of codes
  - authentication is enforced identically

If someone adds a rule to one transport instead of to the service, one of
these fails.
*/

func authenticate(token string) (string, bool) {
	users := map[string]string{"ada-token": "u-1", "alan-token": "u-2"}

	userID, found := users[token]

	return userID, found
}

type harness struct {
	grpc notesv1.NotesServiceClient
	http *httptest.Server
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	// One service, two transports - exactly as cmd/server wires it.
	service := notes.NewService(notes.SystemClock{})

	listener := bufconn.Listen(1024 * 1024)

	server := grpc.NewServer(grpc.ChainUnaryInterceptor(
		grpcapi.RecoveryInterceptor(),
		grpcapi.AuthInterceptor(authenticate),
	))

	notesv1.RegisterNotesServiceServer(server, grpcapi.NewServer(service))

	go func() {
		if err := server.Serve(listener); err != nil {
			t.Logf("serve: %v", err)
		}
	}()

	connection, err := grpc.NewClient("passthrough:///notes",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	httpServer := httptest.NewServer(restapi.NewHandler(service, authenticate).Routes())

	t.Cleanup(func() {
		httpServer.Close()

		if err := connection.Close(); err != nil {
			t.Errorf("close connection: %v", err)
		}

		server.Stop()
	})

	return &harness{grpc: notesv1.NewNotesServiceClient(connection), http: httpServer}
}

func (h *harness) ctx(token string) context.Context {
	if token == "" {
		return context.Background()
	}

	return metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+token)
}

func (h *harness) call(t *testing.T, method, path, token string, payload any) (int, []byte) {
	t.Helper()

	var body *bytes.Reader

	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		body = bytes.NewReader(encoded)
	} else {
		body = bytes.NewReader(nil)
	}

	request, err := http.NewRequestWithContext(t.Context(), method, h.http.URL+path, body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	request.Header.Set("Content-Type", "application/json")

	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := h.http.Client().Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
	}()

	var buffer bytes.Buffer

	if _, err := buffer.ReadFrom(response.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}

	return response.StatusCode, buffer.Bytes()
}

// TestWrittenOverOneTransportVisibleOverTheOther is the headline test: the
// transports are adapters over shared state, not two services.
func TestWrittenOverOneTransportVisibleOverTheOther(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	created, err := h.grpc.CreateNote(h.ctx("ada-token"), &notesv1.CreateNoteRequest{
		Title: "over grpc", Body: "hello",
	})
	if err != nil {
		t.Fatalf("grpc create: %v", err)
	}

	status, body := h.call(t, http.MethodGet, "/notes/1", "ada-token", nil)

	if status != http.StatusOK {
		t.Fatalf("http get = %d (%s)", status, body)
	}

	var overHTTP struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
		Owner string `json:"owner_id"`
	}

	if err := json.Unmarshal(body, &overHTTP); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if overHTTP.ID != created.GetNote().GetId() || overHTTP.Title != "over grpc" {
		t.Fatalf("http view = %+v, grpc view = %v", overHTTP, created.GetNote())
	}

	// And the other direction.
	status, body = h.call(t, http.MethodPost, "/notes", "ada-token",
		map[string]string{"title": "over http", "body": "hello"})

	if status != http.StatusCreated {
		t.Fatalf("http create = %d (%s)", status, body)
	}

	var httpCreated struct {
		ID int64 `json:"id"`
	}

	if err := json.Unmarshal(body, &httpCreated); err != nil {
		t.Fatalf("decode: %v", err)
	}

	got, err := h.grpc.GetNote(h.ctx("ada-token"), &notesv1.GetNoteRequest{Id: httpCreated.ID})
	if err != nil {
		t.Fatalf("grpc get: %v", err)
	}

	if got.GetNote().GetTitle() != "over http" {
		t.Fatalf("grpc view = %v", got.GetNote())
	}
}

// TestErrorParity: each domain failure must produce the matching pair of
// codes. A mapping added to one transport and forgotten in the other fails
// here rather than in production.
func TestErrorParity(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	// Seed: note 1 belongs to ada.
	if _, err := h.grpc.CreateNote(h.ctx("ada-token"),
		&notesv1.CreateNoteRequest{Title: "ada's note"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tests := []struct {
		name       string
		grpcCall   func() error
		httpMethod string
		httpPath   string
		httpToken  string
		httpBody   any
		wantCode   codes.Code
		wantStatus int
	}{
		{
			name: "no credentials",
			grpcCall: func() error {
				_, err := h.grpc.GetNote(h.ctx(""), &notesv1.GetNoteRequest{Id: 1})
				return err
			},
			httpMethod: http.MethodGet, httpPath: "/notes/1", httpToken: "",
			wantCode: codes.Unauthenticated, wantStatus: http.StatusUnauthorized,
		},
		{
			name: "invalid token",
			grpcCall: func() error {
				_, err := h.grpc.GetNote(h.ctx("nope"), &notesv1.GetNoteRequest{Id: 1})
				return err
			},
			httpMethod: http.MethodGet, httpPath: "/notes/1", httpToken: "nope",
			wantCode: codes.Unauthenticated, wantStatus: http.StatusUnauthorized,
		},
		{
			name: "another user's note",
			grpcCall: func() error {
				_, err := h.grpc.GetNote(h.ctx("alan-token"), &notesv1.GetNoteRequest{Id: 1})
				return err
			},
			httpMethod: http.MethodGet, httpPath: "/notes/1", httpToken: "alan-token",
			wantCode: codes.PermissionDenied, wantStatus: http.StatusForbidden,
		},
		{
			name: "missing note",
			grpcCall: func() error {
				_, err := h.grpc.GetNote(h.ctx("ada-token"), &notesv1.GetNoteRequest{Id: 9999})
				return err
			},
			httpMethod: http.MethodGet, httpPath: "/notes/9999", httpToken: "ada-token",
			wantCode: codes.NotFound, wantStatus: http.StatusNotFound,
		},
		{
			name: "empty title",
			grpcCall: func() error {
				_, err := h.grpc.CreateNote(h.ctx("ada-token"), &notesv1.CreateNoteRequest{Title: "   "})
				return err
			},
			httpMethod: http.MethodPost, httpPath: "/notes", httpToken: "ada-token",
			httpBody: map[string]string{"title": "   "},
			wantCode: codes.InvalidArgument, wantStatus: http.StatusUnprocessableEntity,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := status.Code(test.grpcCall()); got != test.wantCode {
				t.Errorf("grpc code = %s, want %s", got, test.wantCode)
			}

			gotStatus, body := h.call(t, test.httpMethod, test.httpPath, test.httpToken, test.httpBody)

			if gotStatus != test.wantStatus {
				t.Errorf("http status = %d, want %d (%s)", gotStatus, test.wantStatus, body)
			}
		})
	}
}

func TestListParity(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := h.ctx("ada-token")

	for _, title := range []string{"first", "second", "third"} {
		if _, err := h.grpc.CreateNote(ctx, &notesv1.CreateNoteRequest{Title: title}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	if _, err := h.grpc.ArchiveNote(ctx, &notesv1.ArchiveNoteRequest{Id: 1}); err != nil {
		t.Fatalf("archive: %v", err)
	}

	overGRPC, err := h.grpc.ListNotes(ctx, &notesv1.ListNotesRequest{})
	if err != nil {
		t.Fatalf("grpc list: %v", err)
	}

	_, body := h.call(t, http.MethodGet, "/notes", "ada-token", nil)

	var overHTTP struct {
		Notes []struct {
			ID int64 `json:"id"`
		} `json:"notes"`
		TotalSize int32 `json:"total_size"`
	}

	if err := json.Unmarshal(body, &overHTTP); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(overGRPC.GetNotes()) != len(overHTTP.Notes) || overGRPC.GetTotalSize() != overHTTP.TotalSize {
		t.Fatalf("grpc returned %d/%d, http returned %d/%d",
			len(overGRPC.GetNotes()), overGRPC.GetTotalSize(),
			len(overHTTP.Notes), overHTTP.TotalSize)
	}

	// The archived note is hidden by default in both.
	for _, note := range overGRPC.GetNotes() {
		if note.GetId() == 1 {
			t.Fatal("grpc listed the archived note by default")
		}
	}

	// And included when asked, in both.
	withArchived, err := h.grpc.ListNotes(ctx, &notesv1.ListNotesRequest{IncludeArchived: true})
	if err != nil {
		t.Fatalf("grpc list: %v", err)
	}

	_, body = h.call(t, http.MethodGet, "/notes?include_archived=true", "ada-token", nil)

	if err := json.Unmarshal(body, &overHTTP); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(withArchived.GetNotes()) != 3 || len(overHTTP.Notes) != 3 {
		t.Fatalf("with archived: grpc %d, http %d, want 3 each",
			len(withArchived.GetNotes()), len(overHTTP.Notes))
	}
}

func TestDeleteIsIdempotentOnBothTransports(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := h.ctx("ada-token")

	if _, err := h.grpc.CreateNote(ctx, &notesv1.CreateNoteRequest{Title: "temporary"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	first, err := h.grpc.DeleteNote(ctx, &notesv1.DeleteNoteRequest{Id: 1})
	if err != nil {
		t.Fatalf("grpc delete: %v", err)
	}

	if !first.GetDeleted() {
		t.Fatal("first delete removed nothing")
	}

	// Second delete over the other transport: still OK, still honest about
	// having removed nothing.
	statusCode, body := h.call(t, http.MethodDelete, "/notes/1", "ada-token", nil)

	if statusCode != http.StatusOK {
		t.Fatalf("http delete = %d (%s)", statusCode, body)
	}

	var response struct {
		Deleted bool `json:"deleted"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if response.Deleted {
		t.Fatal("the second delete claimed to remove a note that was already gone")
	}
}
