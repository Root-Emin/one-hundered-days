package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

/*
Day 50 - Databases (II) & Repositories: Practice Project & Review

Section 10 capstone. See README.md for the schema, the migration commands,
the pool settings and how to run the tests.

Tasks covered:

 1. All persistence goes through repository interfaces (domain.go), tested
    against a real database (repository_test.go)
 2. SQL is centralized and de-duplicated in queries.go
 3. Integration tests cover happy and not-found paths for every repository
    method, plus the HTTP surface (api_test.go)
 4. The data layer is documented: README.md

Layering, bottom to top:

	migrations/    schema, versioned
	queries.go     every SQL statement, named after its operation
	repository.go  interface implementations, driver errors -> domain errors
	database.go    pool, migrations runner, transaction manager
	domain.go      types, rules, repository interfaces
	service.go     business logic and units of work
	api.go         HTTP, DTOs, status codes
	main.go        composition root

Run:

	go run . migrate up
	go run . migrate status
	go run . serve
	go run . demo          # scripted end-to-end run against a temp database

Test:

	go test ./...
	go test -race -count=1 ./...
*/

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day50: ")

	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	ctx := context.Background()
	config := DefaultDBConfig()

	if command == "demo" {
		config.Path = ":memory:"
	}

	db, err := OpenDB(ctx, config)
	if err != nil {
		log.Fatalf("database unavailable: %v", err)
	}

	// The pool is closed on every exit path, including migration commands.
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	}()

	switch command {
	case "migrate":
		sub := "up"
		if len(os.Args) > 2 {
			sub = os.Args[2]
		}

		switch sub {
		case "up":
			err = MigrateUp(ctx, db)
		case "down":
			err = MigrateDown(ctx, db)
		case "status":
			err = MigrationStatus(ctx, db)
		default:
			usage()
			os.Exit(2)
		}

	case "demo":
		if err = MigrateUp(ctx, db); err == nil {
			err = runDemo(ctx, db)
		}

	case "serve":
		if err = MigrateUp(ctx, db); err == nil {
			err = serve(ctx, db, config)
		}

	default:
		usage()
		os.Exit(2)
	}

	if err != nil {
		log.Fatalf("%s: %v", command, err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: go run . <serve|demo|migrate up|migrate down|migrate status>")
}

func serve(ctx context.Context, db *sql.DB, config DBConfig) error {
	shop := NewShopService(RepositoriesFor(db), NewSQLTxManager(db))
	api := NewAPI(shop, db)

	server := &http.Server{
		Addr:              ":" + envOr("PORT", "8080"),
		Handler:           api.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)

	go func() {
		log.Printf("listening on %s db=%s pool_max_open=%d",
			server.Addr, config.Path, config.MaxOpenConns)

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

	// Stop accepting requests first, let the in-flight ones finish, and only
	// then let main's deferred db.Close release the pool.
	if err := server.Shutdown(shutdownCtx); err != nil {
		if closeErr := server.Close(); closeErr != nil {
			log.Printf("force close: %v", closeErr)
		}

		return fmt.Errorf("graceful shutdown: %w", err)
	}

	log.Printf("stopped cleanly")

	return nil
}

// runDemo exercises the whole stack without a network: register, stock,
// order, oversell, cancel.
func runDemo(ctx context.Context, db *sql.DB) error {
	shop := NewShopService(RepositoriesFor(db), NewSQLTxManager(db))

	customer, err := shop.RegisterCustomer(ctx, "ada@example.com", "Ada Lovelace")
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}

	fmt.Printf("\ncustomer  #%d %s\n", customer.ID, customer.Email)

	keyboard, err := shop.AddProduct(ctx, Product{SKU: "kb-01", Name: "Keyboard", PriceCents: 129_00, Stock: 5})
	if err != nil {
		return fmt.Errorf("add product: %w", err)
	}

	mouse, err := shop.AddProduct(ctx, Product{SKU: "ms-02", Name: "Mouse", PriceCents: 49_00, Stock: 10})
	if err != nil {
		return fmt.Errorf("add product: %w", err)
	}

	fmt.Printf("products  %s stock=%d, %s stock=%d\n",
		keyboard.SKU, keyboard.Stock, mouse.SKU, mouse.Stock)

	order, err := shop.PlaceOrder(ctx, customer.ID, []OrderLine{
		{ProductID: keyboard.ID, Quantity: 2},
		{ProductID: mouse.ID, Quantity: 1},
	})
	if err != nil {
		return fmt.Errorf("place order: %w", err)
	}

	fmt.Printf("order     #%d total=%s items=%d status=%s\n",
		order.ID, money(order.TotalCents), len(order.Items), order.Status)

	// The oversell attempt must leave no trace: no order row, no stock change.
	_, err = shop.PlaceOrder(ctx, customer.ID, []OrderLine{
		{ProductID: keyboard.ID, Quantity: 2},
		{ProductID: mouse.ID, Quantity: 99},
	})

	if !errors.Is(err, ErrOutOfStock) {
		return fmt.Errorf("expected ErrOutOfStock, got %v", err)
	}

	fmt.Printf("rejected  %v\n", err)

	after, err := shop.Products(ctx, 10, 0)
	if err != nil {
		return fmt.Errorf("list products: %w", err)
	}

	for _, product := range after {
		fmt.Printf("stock     %s = %d\n", product.SKU, product.Stock)
	}

	fmt.Println("          (the keyboard reservation from the failed order was rolled back)")

	cancelled, err := shop.CancelOrder(ctx, order.ID)
	if err != nil {
		return fmt.Errorf("cancel: %w", err)
	}

	fmt.Printf("cancelled #%d status=%s\n", cancelled.ID, cancelled.Status)

	restored, err := shop.Products(ctx, 10, 0)
	if err != nil {
		return fmt.Errorf("list products: %w", err)
	}

	for _, product := range restored {
		fmt.Printf("stock     %s = %d (returned)\n", product.SKU, product.Stock)
	}

	orders, err := shop.Orders(ctx, customer.ID, 10, 0)
	if err != nil {
		return fmt.Errorf("list orders: %w", err)
	}

	fmt.Printf("history   %d order(s) for customer #%d\n\n", len(orders), customer.ID)

	return nil
}
