// Command client calls the same operations over gRPC and over HTTP and prints
// the two results side by side.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	notesv1 "example.com/onehundredday/Section-13-gRPC-And-Protocol-Buffers-Protobuf-definitions-gRPC-services-clients-interceptors-and-metadata/Day-65/gen/notes/v1"
)

/*
Run the server first:

	go run ./cmd/server

Then:

	go run ./cmd/client
*/

func main() {
	log.SetFlags(0)

	grpcAddress := envOr("GRPC_ADDR", "localhost:9090")
	httpAddress := envOr("HTTP_BASE", "http://localhost:8080")

	connection, err := grpc.NewClient(grpcAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial: %v", err)
	}

	defer func() {
		if err := connection.Close(); err != nil {
			log.Printf("close: %v", err)
		}
	}()

	client := notesv1.NewNotesServiceClient(connection)

	// The token travels as gRPC metadata here and as an HTTP header below -
	// same credential, two encodings.
	ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer ada-token")

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	fmt.Println("\n1) Create over gRPC, read over HTTP")
	fmt.Println(strings.Repeat("-", 74))

	created, err := client.CreateNote(ctx, &notesv1.CreateNoteRequest{
		Title: "Written over gRPC",
		Body:  "and readable over HTTP, because both transports share one service",
	})
	if err != nil {
		log.Fatalf("create: %v", err)
	}

	fmt.Printf("  gRPC CreateNote -> id=%d title=%q\n",
		created.GetNote().GetId(), created.GetNote().GetTitle())

	body, code, err := httpCall(http.MethodGet,
		fmt.Sprintf("%s/notes/%d", httpAddress, created.GetNote().GetId()), "ada-token", nil)
	if err != nil {
		log.Fatalf("http get: %v", err)
	}

	fmt.Printf("  HTTP  GET /notes/%d -> %d %s\n", created.GetNote().GetId(), code, compact(body))

	fmt.Println("\n2) Create over HTTP, read over gRPC")
	fmt.Println(strings.Repeat("-", 74))

	body, code, err = httpCall(http.MethodPost, httpAddress+"/notes", "ada-token",
		map[string]string{"title": "Written over HTTP", "body": "and readable over gRPC"})
	if err != nil {
		log.Fatalf("http create: %v", err)
	}

	var httpNote struct {
		ID int64 `json:"id"`
	}

	if err := json.Unmarshal(body, &httpNote); err != nil {
		log.Fatalf("decode: %v", err)
	}

	fmt.Printf("  HTTP  POST /notes -> %d id=%d\n", code, httpNote.ID)

	got, err := client.GetNote(ctx, &notesv1.GetNoteRequest{Id: httpNote.ID})
	if err != nil {
		log.Fatalf("grpc get: %v", err)
	}

	fmt.Printf("  gRPC  GetNote     -> title=%q owner=%s\n",
		got.GetNote().GetTitle(), got.GetNote().GetOwnerId())

	fmt.Println("\n3) The same failures, in each protocol's vocabulary")
	fmt.Println(strings.Repeat("-", 74))

	fmt.Printf("%-24s %-28s %s\n", "CASE", "gRPC", "HTTP")

	// Missing credentials.
	_, grpcErr := notesv1.NewNotesServiceClient(connection).
		GetNote(context.Background(), &notesv1.GetNoteRequest{Id: 1})

	_, httpCode, _ := httpCall(http.MethodGet, httpAddress+"/notes/1", "", nil)

	fmt.Printf("%-24s %-28s %d\n", "no credentials", status.Code(grpcErr), httpCode)

	// Someone else's note: note 1 belongs to ada, so alan must be refused on
	// both transports.
	alanCtx, cancelAlan := context.WithTimeout(
		metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer alan-token"),
		5*time.Second)
	defer cancelAlan()

	_, grpcErr = client.GetNote(alanCtx, &notesv1.GetNoteRequest{Id: 1})

	_, httpCode, _ = httpCall(http.MethodGet, httpAddress+"/notes/1", "alan-token", nil)

	fmt.Printf("%-24s %-28s %d\n", "another user's note", status.Code(grpcErr), httpCode)

	// Missing note.
	_, grpcErr = client.GetNote(ctx, &notesv1.GetNoteRequest{Id: 99999})

	_, httpCode, _ = httpCall(http.MethodGet, httpAddress+"/notes/99999", "ada-token", nil)

	fmt.Printf("%-24s %-28s %d\n", "missing note", status.Code(grpcErr), httpCode)

	// Validation.
	_, grpcErr = client.CreateNote(ctx, &notesv1.CreateNoteRequest{Title: "  "})

	_, httpCode, _ = httpCall(http.MethodPost, httpAddress+"/notes", "ada-token",
		map[string]string{"title": "  "})

	fmt.Printf("%-24s %-28s %d\n", "empty title", status.Code(grpcErr), httpCode)

	fmt.Println("\n  Same rules, same decisions, expressed as gRPC codes on one side and")
	fmt.Println("  HTTP statuses on the other. Neither transport implements a rule.")
	fmt.Println()
}

func httpCall(method, url, token string, payload any) ([]byte, int, error) {
	var reader io.Reader

	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}

		reader = bytes.NewReader(encoded)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, 0, err
	}

	request.Header.Set("Content-Type", "application/json")

	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, 0, err
	}

	defer func() {
		if err := response.Body.Close(); err != nil {
			log.Printf("close body: %v", err)
		}
	}()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, response.StatusCode, err
	}

	return body, response.StatusCode, nil
}

func compact(body []byte) string {
	var buffer bytes.Buffer

	if err := json.Compact(&buffer, body); err != nil {
		return string(body)
	}

	text := buffer.String()

	if len(text) > 90 {
		return text[:90] + "..."
	}

	return text
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
