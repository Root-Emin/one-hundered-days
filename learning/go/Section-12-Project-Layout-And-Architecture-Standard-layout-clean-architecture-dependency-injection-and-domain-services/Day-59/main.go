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
Day 59 - Project Layout & Architecture: Domain Models and Services

Tasks covered:

 1. Rich domain types that enforce their invariants (Email, SKU, Quantity,
    Money, Order) instead of anemic structs of strings and ints
 2. Validation in the domain, so invalid state cannot reach storage
 3. Typed domain errors, mapped to HTTP in one place in the transport layer
 4. Handlers that only parse, call and render

Files:

	errors.go    typed errors: FieldError, ValidationError, NotFoundError, StateError
	domain.go    value objects and the Order entity with its invariants
	service.go   use cases, repository and catalog adapters
	http.go      thin handlers and the error-to-status mapping
	main.go      wiring, plus a demo of what the invariants block

Run:

	go run .          # HTTP server on :8080
	go run . demo     # walk an order through its lifecycle, no server

Try it:

	curl -XPOST localhost:8080/orders -d '{"customer_email":"ada@example.com","lines":[{"sku":"kb-01","quantity":2}]}'
	curl -XPOST localhost:8080/orders -d '{"customer_email":"nope","lines":[{"sku":"","quantity":0}]}'
	curl -XPOST localhost:8080/orders/1/submit
	curl -XPOST localhost:8080/orders/1/lines -d '{"sku":"ms-02","quantity":1}'   # 409
	curl localhost:8080/orders/1

Test:

	go test ./...
*/

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day59: ")

	orders := NewOrderService(NewMemoryOrderRepository(), NewStaticCatalog(), SystemClock{})

	if len(os.Args) > 1 && os.Args[1] == "demo" {
		if err := runDemo(context.Background(), orders); err != nil {
			log.Fatalf("demo: %v", err)
		}

		return
	}

	if err := serve(orders); err != nil {
		log.Fatalf("%v", err)
	}
}

// runDemo shows what the rich types refuse to do. Each failure below is a bug
// that an anemic model would have let through to the database.
func runDemo(ctx context.Context, orders *OrderService) error {
	fmt.Println("\nWhat the domain types refuse")
	fmt.Println(strings.Repeat("-", 74))

	// 1. Value objects reject bad input at construction.
	for _, attempt := range []struct {
		label string
		build func() error
	}{
		{"email without @", func() error { _, err := NewEmail("ada.example.com"); return err }},
		{"empty sku", func() error { _, err := NewSKU("   "); return err }},
		{"sku with spaces", func() error { _, err := NewSKU("KB 01"); return err }},
		{"quantity of zero", func() error { _, err := NewQuantity(0); return err }},
		{"quantity of 5000", func() error { _, err := NewQuantity(5000); return err }},
		{"negative money", func() error { _, err := NewMoney(-100, "EUR"); return err }},
		{"currency 'EURO'", func() error { _, err := NewMoney(100, "EURO"); return err }},
	} {
		fmt.Printf("  %-22s -> %v\n", attempt.label, attempt.build())
	}

	// 2. Several validation failures are reported together.
	fmt.Println("\nAll validation errors at once")
	fmt.Println(strings.Repeat("-", 74))

	_, err := orders.CreateOrder(ctx, "not-an-email", []LineRequest{
		{SKU: "", Quantity: 0},
		{SKU: "KB 01", Quantity: -3},
	})

	var validation *ValidationError

	if errors.As(err, &validation) {
		for _, field := range validation.Fields {
			fmt.Printf("  field=%-10s rule=%-10s %s\n", field.Field, field.Rule, field.Message)
		}
	}

	// 3. A real order, and the state machine that guards it.
	fmt.Println("\nOrder lifecycle")
	fmt.Println(strings.Repeat("-", 74))

	order, err := orders.CreateOrder(ctx, "ADA@Example.com ", []LineRequest{
		{SKU: "kb-01", Quantity: 1},
		{SKU: "ms-02", Quantity: 2},
		{SKU: "kb-01", Quantity: 1}, // merges with the first line, not a duplicate
	})
	if err != nil {
		return err
	}

	fmt.Printf("  created  id=%d customer=%s lines=%d total=%s\n",
		order.ID(), order.Customer(), len(order.Lines()), order.Total())

	for _, line := range order.Lines() {
		fmt.Printf("           %-6s x%-3d %s = %s\n",
			line.SKU, line.Quantity.Int(), line.UnitPrice, line.Total())
	}

	if _, err := orders.Submit(ctx, order.ID()); err != nil {
		return err
	}

	fmt.Println("  submitted")

	// Adding a line to a submitted order is a state error, not a validation
	// error - and the transport layer turns it into 409, not 422.
	_, err = orders.AddLine(ctx, order.ID(), LineRequest{SKU: "hp-03", Quantity: 1})
	fmt.Printf("  add line after submit -> %v\n", err)

	if _, err := orders.Pay(ctx, order.ID()); err != nil {
		return err
	}

	fmt.Println("  paid")

	_, err = orders.Cancel(ctx, order.ID())
	fmt.Printf("  cancel after payment  -> %v\n", err)

	// 4. Unknown product.
	_, err = orders.CreateOrder(ctx, "ada@example.com", []LineRequest{{SKU: "zz-99", Quantity: 1}})
	fmt.Printf("  unknown sku           -> %v\n", err)

	fmt.Println("\nEvery message above came from the domain layer. The HTTP layer only")
	fmt.Println("chooses the status code: 422 for validation, 404 for missing, 409 for")
	fmt.Println("a wrong state - see respondError in http.go.")

	return nil
}

func serve(orders *OrderService) error {
	server := &http.Server{
		Addr:              ":" + envOr("PORT", "8080"),
		Handler:           NewHandler(orders).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)

	go func() {
		log.Printf("listening on %s", server.Addr)

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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		return err
	}

	log.Printf("stopped cleanly")

	return nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
