package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

/*
Day 58 - Project Layout & Architecture: Dependency Injection

Tasks covered:

 1. Services and handlers built through constructors with explicit dependencies
 2. Interfaces at the boundaries: repository, clock, id generator, mailer
 3. No service locator - and locator.go shows exactly what is lost when one
    is used instead
 4. Tests inject fakes and run without a database, a clock, or an SMTP server

Files:

	ports.go        the interfaces (the seams)
	service.go      the service, wired through its constructor
	adapters.go     production implementations
	locator.go      the anti-pattern, annotated
	main.go         the composition root and a comparison demo
	service_test.go tests with injected fakes
	locator_test.go the failure mode of shared global state

Run:

	go run .
	go run . wiring    # print the dependency graph this binary builds

Test:

	go test ./...
	go test -race -count=1 ./...
*/

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day58: ")

	if len(os.Args) > 1 && os.Args[1] == "wiring" {
		printWiring()

		return
	}

	if err := run(); err != nil {
		log.Fatalf("%v", err)
	}
}

// run is the composition root: every concrete type in the program is chosen
// here, in one place, and passed inward. Nothing below this function knows
// which implementations it received.
func run() error {
	ctx := context.Background()

	// 1. Build the adapters.
	users := NewMemoryUserRepository()
	clock := SystemClock{}
	ids := RandomIDGenerator{Prefix: "usr"}

	var mailer Mailer = LogMailer{}

	if strings.EqualFold(os.Getenv("MAIL"), "off") {
		// Swapping an implementation is a line in main, not a change in the
		// service.
		mailer = NoopMailer{}
	}

	// 2. Inject them.
	signup, err := NewSignupService(SignupConfig{
		Users:       users,
		Clock:       clock,
		IDs:         ids,
		Mailer:      mailer,
		Audit:       LogAudit{},
		TrialLength: 14 * 24 * time.Hour,
	})
	if err != nil {
		return err
	}

	// 3. Use them.
	fmt.Println("\nSignups")
	fmt.Println(strings.Repeat("-", 68))

	for _, candidate := range []struct {
		email string
		name  string
		plan  string
	}{
		{"ada@example.com", "Ada Lovelace", "pro"},
		{"alan@example.com", "Alan Turing", "free"},
		{"ada@example.com", "Ada Again", "pro"},
		{"not-an-email", "Nobody", "pro"},
		{"grace@example.com", "Grace Hopper", "platinum"},
	} {
		user, err := signup.Signup(ctx, candidate.email, candidate.name, candidate.plan)

		switch {
		case errors.Is(err, ErrDuplicate):
			fmt.Printf("  %-22s rejected: already registered\n", candidate.email)

		case errors.Is(err, ErrValidation):
			fmt.Printf("  %-22s rejected: %v\n", candidate.email, err)

		case err != nil:
			return err

		default:
			trial := "no trial"

			if !user.TrialEnds.IsZero() {
				trial = "trial until " + user.TrialEnds.Format(time.DateOnly)
			}

			fmt.Printf("  %-22s created id=%s plan=%s (%s)\n",
				user.Email, user.ID, user.Plan, trial)
		}
	}

	count, err := signup.Users(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("\n%d user(s) stored\n", count)

	printComparison()

	return nil
}

func printWiring() {
	fmt.Println(`
Dependency graph built by run():

    main
     ├── MemoryUserRepository ──┐
     ├── SystemClock ───────────┤
     ├── RandomIDGenerator ─────┼──► NewSignupService(SignupConfig{...})
     ├── LogMailer ─────────────┤
     └── LogAudit ──────────────┘

    SignupService knows only the interfaces:
        UserRepository, Clock, IDGenerator, Mailer, AuditLogger

Reading the constructor call tells you the entire coupling of the service.
Reading a service-locator equivalent tells you nothing at all.`)
}

func printComparison() {
	fmt.Println("\nConstructor injection vs service locator")
	fmt.Println(strings.Repeat("-", 68))

	rows := []struct {
		aspect  string
		di      string
		locator string
	}{
		{"Dependencies visible", "in the constructor signature", "nowhere"},
		{"Missing dependency", "startup error", "runtime panic, on that code path"},
		{"Test setup", "pass a fake", "mutate a global registry"},
		{"Parallel tests", "safe", "shared state, order dependent"},
		{"Swapping an implementation", "one line in main", "one line, anywhere, at any time"},
		{"Compiler help", "type checked at the call site", "type assertion at run time"},
	}

	fmt.Printf("%-28s %-32s %s\n", "ASPECT", "CONSTRUCTOR INJECTION", "SERVICE LOCATOR")

	for _, row := range rows {
		fmt.Printf("%-28s %-32s %s\n", row.aspect, row.di, row.locator)
	}

	fmt.Println("\nThe locator version is in locator.go, and locator_test.go shows the")
	fmt.Println("test-pollution failure it causes. Wiring frameworks (google/wire,")
	fmt.Println("uber/fx) generate or automate the constructor calls - they do not")
	fmt.Println("bring the globals back.")
}
