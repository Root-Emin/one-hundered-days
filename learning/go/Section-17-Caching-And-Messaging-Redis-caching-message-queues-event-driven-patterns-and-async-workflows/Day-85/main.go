// Day 85 - Caching & Messaging, Practice.
//
// The MVP now has both halves of a real system:
//
//	SYNCHRONOUS   GET /products/{id} reads through a cache; PUT invalidates it
//	              after the write commits.
//	ASYNCHRONOUS  POST /orders commits the order and its event together and
//	              returns 202; a worker turns the event into a receipt.
//
// This demo runs the API and the worker in ONE process so it is a single
// command, but they are two binaries (cmd/api, cmd/worker) precisely because
// they scale and fail independently. The bus deliberately delivers every event
// twice, so the run proves the consumer is idempotent rather than assuming it.
//
// Run: go run ./Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/api"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/cache"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/queue"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/store"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/worker"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Warn level: the packages stay quiet unless something is actually wrong.
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	dir, err := os.MkdirTemp("", "day85")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}

	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintln(os.Stderr, "cleanup:", err)
		}
	}()

	// A file, not :memory: - the API and the worker are separate processes in
	// production and share the database, and the demo keeps that shape.
	db, err := store.Open("file:" + filepath.Join(dir, "day85.db"))
	if err != nil {
		return err
	}

	defer func() {
		if err := db.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "close db:", err)
		}
	}()

	dataStore := store.New(db)
	productCache := cache.NewMemory()
	service := api.New(dataStore, productCache, logger)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	server := &http.Server{Handler: service.Routes(), ReadHeaderTimeout: 5 * time.Second}

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, "serve:", err)
		}
	}()

	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintln(os.Stderr, "shutdown:", err)
		}
	}()

	base := "http://" + listener.Addr().String()

	// The async side, exactly as cmd/worker wires it.
	bus := queue.NewBus(logger)
	bus.DeliverTwice(true)

	receipts := worker.New(dataStore, logger)
	receipts.Register(bus)

	relay := queue.NewRelay(dataStore, bus, 50*time.Millisecond, logger)

	go relay.Run(ctx)

	client := &http.Client{Timeout: 5 * time.Second}

	product, err := demoCache(ctx, client, base)
	if err != nil {
		return err
	}

	if err := demoAsyncOrder(ctx, client, base, dataStore, product.ID); err != nil {
		return err
	}

	section("4. What the numbers say")

	hits, misses := service.CacheStats()
	delivered, failures := bus.Stats()
	processed, duplicates := receipts.Stats()

	fmt.Printf("  cache      hits=%d misses=%d entries=%d\n", hits, misses, productCache.Len())
	fmt.Printf("  relay      published=%d\n", relay.Published())
	fmt.Printf("  bus        delivered=%d failures=%d (duplicate delivery is ON)\n", delivered, failures)
	fmt.Printf("  worker     processed=%d duplicates_ignored=%d\n", processed, duplicates)
	fmt.Println("  every event was delivered twice and did its work once")

	fmt.Println("\nthe full flow is drawn in docs/ASYNC_FLOW.md")

	return nil
}

type product struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	PriceCent int64  `json:"price_cent"`
	Version   int64  `json:"version"`
}

type order struct {
	ID         int64  `json:"id"`
	AmountCent int64  `json:"amount_cent"`
	Status     string `json:"status"`
}

func demoCache(ctx context.Context, client *http.Client, base string) (product, error) {
	section("1. Cache-aside on the read path")

	var created product

	if _, err := call(ctx, client, http.MethodPost, base+"/products",
		map[string]any{"name": "keyboard", "price_cent": 12000}, &created); err != nil {
		return product{}, err
	}

	fmt.Printf("  created product %d at %d cents\n", created.ID, created.PriceCent)

	url := fmt.Sprintf("%s/products/%d", base, created.ID)

	for i := 1; i <= 3; i++ {
		var fetched product

		header, err := call(ctx, client, http.MethodGet, url, nil, &fetched)
		if err != nil {
			return product{}, err
		}

		fmt.Printf("  GET #%d -> X-Cache: %-4s price=%d\n", i, header.Get("X-Cache"), fetched.PriceCent)
	}

	section("2. Invalidation on the write path")

	var updated product

	if _, err := call(ctx, client, http.MethodPut, url, map[string]any{"price_cent": 9900}, &updated); err != nil {
		return product{}, err
	}

	fmt.Printf("  PUT price=9900 -> version %d (commit first, THEN delete the key)\n", updated.Version)

	var fetched product

	header, err := call(ctx, client, http.MethodGet, url, nil, &fetched)
	if err != nil {
		return product{}, err
	}

	fmt.Printf("  GET  -> X-Cache: %-4s price=%d (the stale value is gone)\n", header.Get("X-Cache"), fetched.PriceCent)

	if header, err = call(ctx, client, http.MethodGet, url, nil, &fetched); err != nil {
		return product{}, err
	}

	fmt.Printf("  GET  -> X-Cache: %-4s price=%d (repopulated)\n", header.Get("X-Cache"), fetched.PriceCent)

	return created, nil
}

func demoAsyncOrder(ctx context.Context, client *http.Client, base string, dataStore *store.Store, productID int64) error {
	section("3. Domain event -> worker, delivered twice")

	var placed order

	if _, err := call(ctx, client, http.MethodPost, base+"/orders",
		map[string]any{"product_id": productID, "quantity": 2}, &placed); err != nil {
		return err
	}

	fmt.Printf("  POST /orders -> 202 order %d status=%q amount=%d\n", placed.ID, placed.Status, placed.AmountCent)
	fmt.Println("  the HTTP request is finished; the receipt has not been written yet")

	// Poll the read model rather than sleeping a fixed amount: the point of
	// async is that you do not know when it lands, only that it will.
	deadline := time.Now().Add(5 * time.Second)

	var confirmed order

	for {
		url := fmt.Sprintf("%s/orders/%d", base, placed.ID)

		if _, err := call(ctx, client, http.MethodGet, url, nil, &confirmed); err != nil {
			return err
		}

		if confirmed.Status == "confirmed" {
			break
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("order %d never reached confirmed (status %q)", placed.ID, confirmed.Status)
		}

		time.Sleep(10 * time.Millisecond)
	}

	fmt.Printf("  worker ran -> order %d status=%q\n", confirmed.ID, confirmed.Status)

	receiptCount, err := dataStore.ReceiptCount(ctx, placed.ID)
	if err != nil {
		return err
	}

	pending, err := dataStore.PendingEventCount(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("  event delivered 2x -> %d receipt(s) written, %d event(s) still pending\n", receiptCount, pending)

	if receiptCount != 1 {
		return fmt.Errorf("idempotency broken: %d receipts for one order", receiptCount)
	}

	return nil
}

func call(ctx context.Context, client *http.Client, method, url string, body any, target any) (http.Header, error) {
	var reader io.Reader

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}

		reader = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, url, err)
	}

	defer func() {
		if err := response.Body.Close(); err != nil {
			_ = err
		}
	}()

	if response.StatusCode >= http.StatusBadRequest {
		payload, _ := io.ReadAll(response.Body)

		return nil, fmt.Errorf("%s %s: %s: %s", method, url, response.Status, bytes.TrimSpace(payload))
	}

	if target != nil {
		if err := json.NewDecoder(response.Body).Decode(target); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
	}

	return response.Header, nil
}

func section(title string) {
	underline := make([]byte, len(title))

	for i := range underline {
		underline[i] = '='
	}

	fmt.Printf("\n%s\n%s\n", title, underline)
}
