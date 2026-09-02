package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

/*
Day 46 - Databases (II) & Repositories: Repository Pattern

Tasks covered:

 1. Repository interfaces expressed in domain operations, not SQL (domain.go)
 2. A concrete SQL repository over database/sql (sqlite_repository.go)
 3. Mapping domain models to API DTOs at the boundary (dto.go)
 4. Repositories injected into handlers through constructors (this file)

Files:

	domain.go             domain types, errors, the repository interface, service
	sqlite_repository.go  the only file that knows SQL exists
	memory_repository.go  in-memory fake with identical behaviour
	dto.go                DTO mapping + HTTP handlers
	repository_test.go    one contract test suite, run against both repositories

Run:

	go run .                 # same scenario against memory and SQLite
	STORAGE=memory go run . serve
	STORAGE=sqlite go run . serve

Environment variables:

	STORAGE   memory | sqlite   Default: sqlite
	DB_PATH   SQLite path.      Default: :memory:
	PORT      HTTP port.        Default: 8080

Test:

	go test ./...

The wiring below is the whole point: main.go is the only place that decides
which storage engine exists. Handlers, service and domain never find out.
*/

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day46: ")

	ctx := context.Background()

	if len(os.Args) > 1 && os.Args[1] == "serve" {
		if err := serve(ctx); err != nil {
			log.Fatalf("serve: %v", err)
		}

		return
	}

	// Default: prove both implementations are interchangeable.
	memory := NewMemoryProductRepository()

	if err := runScenario(ctx, "memory", memory); err != nil {
		log.Fatalf("memory scenario: %v", err)
	}

	db, err := openProductDB(ctx, ":memory:")
	if err != nil {
		log.Fatalf("open database: %v", err)
	}

	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	}()

	if err := runScenario(ctx, "sqlite", NewSQLProductRepository(db)); err != nil {
		log.Fatalf("sqlite scenario: %v", err)
	}

	fmt.Println("\nIdentical output from two different storage engines.")
	fmt.Println("The service and handlers were compiled once and never told which was which.")
}

// runScenario drives the service through its whole surface. It accepts the
// interface, so the same function tests a map and a database.
func runScenario(ctx context.Context, label string, repository ProductRepository) error {
	fmt.Printf("\n=== storage: %s ===\n", label)

	catalog := NewCatalogService(repository)

	seed := []Product{
		{SKU: "kb-01", Name: "Mechanical Keyboard", PriceCent: 129_00, CostCents: 71_00, Stock: 12},
		{SKU: "ms-02", Name: "Wireless Mouse", PriceCent: 49_00, CostCents: 22_00, Stock: 40},
		{SKU: "hp-03", Name: "Studio Headphones", PriceCent: 199_00, CostCents: 110_00, Stock: 0},
	}

	for _, product := range seed {
		created, err := catalog.AddProduct(ctx, product)
		if err != nil {
			return fmt.Errorf("seed %s: %w", product.SKU, err)
		}

		fmt.Printf("  created id=%d sku=%s margin=%.1f%%\n",
			created.ID, created.SKU, created.MarginPercent())
	}

	// Business rule: duplicate SKUs are rejected above storage.
	_, err := catalog.AddProduct(ctx, Product{SKU: "KB-01", Name: "Copy", PriceCent: 1_00})

	if !errors.Is(err, ErrConflict) {
		return fmt.Errorf("expected ErrConflict, got %v", err)
	}

	fmt.Printf("  duplicate rejected: %v\n", err)

	// Validation never reaches storage at all.
	_, err = catalog.AddProduct(ctx, Product{SKU: "bad", Name: "", PriceCent: 0})

	if !errors.Is(err, ErrValidation) {
		return fmt.Errorf("expected ErrValidation, got %v", err)
	}

	fmt.Printf("  validation rejected: %v\n", err)

	// Listing with a filter.
	inStock, err := catalog.Catalog(ctx, ProductFilter{InStockOnly: true})
	if err != nil {
		return fmt.Errorf("catalog: %w", err)
	}

	skus := make([]string, 0, len(inStock))

	for _, product := range inStock {
		skus = append(skus, product.SKU)
	}

	fmt.Printf("  in stock: %s\n", strings.Join(skus, ", "))

	// Selling below zero must fail in both implementations.
	sold, err := catalog.Sell(ctx, inStock[0].ID, 5)
	if err != nil {
		return fmt.Errorf("sell: %w", err)
	}

	fmt.Printf("  sold 5 of %s, %d left\n", sold.SKU, sold.Stock)

	_, err = catalog.Sell(ctx, inStock[0].ID, 1_000)

	if !errors.Is(err, ErrValidation) {
		return fmt.Errorf("expected oversell to fail, got %v", err)
	}

	fmt.Printf("  oversell rejected: %v\n", err)

	// Not found behaves the same way regardless of storage.
	_, err = catalog.Get(ctx, 987654)

	if !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("expected ErrNotFound, got %v", err)
	}

	fmt.Printf("  missing product: %v\n", err)

	// And what the API would actually return for the first product.
	response := toProductResponse(sold)

	fmt.Printf("  DTO: id=%s sku=%s price=%s in_stock=%t (cost_cents absent)\n",
		response.ID, response.SKU, response.Price, response.InStock)

	return nil
}

// serve wires storage, service and handler and starts the HTTP server. This
// is the composition root: the only function that knows every concrete type.
func serve(ctx context.Context) error {
	var (
		repository ProductRepository
		closeFn    = func() error { return nil }
	)

	switch storage := envOr("STORAGE", "sqlite"); storage {
	case "memory":
		repository = NewMemoryProductRepository()

	case "sqlite":
		db, err := openProductDB(ctx, envOr("DB_PATH", ":memory:"))
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}

		repository = NewSQLProductRepository(db)
		closeFn = db.Close

	default:
		return fmt.Errorf("unknown STORAGE=%q, want memory or sqlite", storage)
	}

	defer func() {
		if err := closeFn(); err != nil {
			log.Printf("close storage: %v", err)
		}
	}()

	handler := NewProductHandler(NewCatalogService(repository))

	server := &http.Server{
		Addr:              ":" + envOr("PORT", "8080"),
		Handler:           handler.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)

	go func() {
		log.Printf("listening on %s storage=%s", server.Addr, envOr("STORAGE", "sqlite"))

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		return err

	case received := <-shutdown:
		log.Printf("shutdown signal: %s", received)
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	return server.Shutdown(shutdownCtx)
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
