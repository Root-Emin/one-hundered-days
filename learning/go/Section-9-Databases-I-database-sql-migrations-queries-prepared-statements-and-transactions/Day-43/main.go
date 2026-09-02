package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

/*
Day 43 - Databases (I): Queries and Prepared Statements

Tasks covered:

 1. Prepare repeated queries once and reuse them
 2. Insert rows with parameterized queries, never string concatenation
 3. Translate sql.ErrNoRows into a domain-level "not found" error
 4. Wrap DB errors with operation context, without leaking secrets into logs

Run:

	go run main.go

Environment variables:

	DB_PATH  SQLite file path. Default: :memory:

The program:
  - creates a customers table
  - inserts a batch through one prepared statement
  - looks up an existing and a missing customer
  - shows what a hostile input does to a parameterized query (nothing)
  - shows the difference between logging a wrapped error and leaking a secret
*/

const (
	defaultDBPath = ":memory:"
	queryTimeout  = 3 * time.Second
)

//
// DOMAIN ERRORS
//
// Handlers must not import database/sql just to understand a lookup result.
// The store translates driver-level facts into domain-level meaning.
//

var (
	ErrCustomerNotFound = errors.New("customer not found")
	ErrDuplicateEmail   = errors.New("email already registered")
	ErrInvalidCustomer  = errors.New("invalid customer")
)

//
// MODEL
//

type Customer struct {
	ID    int64
	Email string
	Name  string
	Plan  string

	// apiToken is a secret. It is deliberately unexported and never included
	// in String(), so it cannot slip into a log line by accident.
	apiToken string
}

// String is what ends up in logs, so it carries no secret.
func (c Customer) String() string {
	return fmt.Sprintf("#%d %s <%s> plan=%s", c.ID, c.Name, c.Email, c.Plan)
}

// TokenHint is the only view of the token any log or support screen gets.
func (c Customer) TokenHint() string {
	if len(c.apiToken) < 4 {
		return "****"
	}

	return "****" + c.apiToken[len(c.apiToken)-4:]
}

func (c Customer) validate() error {
	switch {
	case strings.TrimSpace(c.Email) == "":
		return fmt.Errorf("%w: email is required", ErrInvalidCustomer)

	case !strings.Contains(c.Email, "@"):
		return fmt.Errorf("%w: email %q is malformed", ErrInvalidCustomer, c.Email)

	case strings.TrimSpace(c.Name) == "":
		return fmt.Errorf("%w: name is required", ErrInvalidCustomer)

	case c.apiToken == "":
		return fmt.Errorf("%w: api token is required", ErrInvalidCustomer)
	}

	return nil
}

//
// STORE
//
// The store owns its prepared statements. Preparing once at construction and
// reusing them beats re-parsing the same SQL on every call, and it keeps the
// SQL text in one place instead of scattered through handlers.
//

type CustomerStore struct {
	db *sql.DB

	insertStmt    *sql.Stmt
	byIDStmt      *sql.Stmt
	byEmailStmt   *sql.Stmt
	upgradePlanFn *sql.Stmt
}

const (
	insertCustomerSQL = `
		INSERT INTO customers (email, name, plan, api_token)
		VALUES (?, ?, ?, ?);`

	selectCustomerByIDSQL = `
		SELECT id, email, name, plan, api_token
		FROM customers
		WHERE id = ?;`

	selectCustomerByEmailSQL = `
		SELECT id, email, name, plan, api_token
		FROM customers
		WHERE email = ?;`

	updatePlanSQL = `
		UPDATE customers
		SET plan = ?
		WHERE id = ?;`
)

func NewCustomerStore(ctx context.Context, db *sql.DB) (*CustomerStore, error) {
	store := &CustomerStore{db: db}

	// Each Stmt holds driver-side state and must be closed (see Close below).
	statements := []struct {
		query string
		into  **sql.Stmt
	}{
		{insertCustomerSQL, &store.insertStmt},
		{selectCustomerByIDSQL, &store.byIDStmt},
		{selectCustomerByEmailSQL, &store.byEmailStmt},
		{updatePlanSQL, &store.upgradePlanFn},
	}

	for _, statement := range statements {
		prepared, err := db.PrepareContext(ctx, statement.query)
		if err != nil {
			// Do not leak half-prepared state if one of them fails.
			if closeErr := store.Close(); closeErr != nil {
				log.Printf("close store after failed prepare: %v", closeErr)
			}

			return nil, fmt.Errorf("prepare %q: %w", firstLine(statement.query), err)
		}

		*statement.into = prepared
	}

	return store, nil
}

func (s *CustomerStore) Close() error {
	var errs []error

	for _, statement := range []*sql.Stmt{s.insertStmt, s.byIDStmt, s.byEmailStmt, s.upgradePlanFn} {
		if statement == nil {
			continue
		}

		if err := statement.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// Create inserts one customer. Values travel as parameters, so the database
// treats them as data even when they contain SQL syntax.
func (s *CustomerStore) Create(ctx context.Context, customer Customer) (Customer, error) {
	if err := customer.validate(); err != nil {
		return Customer{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	result, err := s.insertStmt.ExecContext(
		ctx,
		customer.Email,
		customer.Name,
		customer.Plan,
		customer.apiToken,
	)
	if err != nil {
		// The email is business data and safe to log; the token is not, so it
		// is not part of the error either.
		if isUniqueViolation(err) {
			return Customer{}, fmt.Errorf("create customer %q: %w", customer.Email, ErrDuplicateEmail)
		}

		return Customer{}, fmt.Errorf("create customer %q: %w", customer.Email, err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Customer{}, fmt.Errorf("create customer %q: read generated id: %w", customer.Email, err)
	}

	customer.ID = id

	return customer, nil
}

func (s *CustomerStore) ByID(ctx context.Context, id int64) (Customer, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	return scanCustomer(s.byIDStmt.QueryRowContext(ctx, id), fmt.Sprintf("id %d", id))
}

func (s *CustomerStore) ByEmail(ctx context.Context, email string) (Customer, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	return scanCustomer(s.byEmailStmt.QueryRowContext(ctx, email), fmt.Sprintf("email %q", email))
}

func (s *CustomerStore) UpgradePlan(ctx context.Context, id int64, plan string) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	result, err := s.upgradePlanFn.ExecContext(ctx, plan, id)
	if err != nil {
		return fmt.Errorf("upgrade plan for customer %d: %w", id, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("upgrade plan for customer %d: read rows affected: %w", id, err)
	}

	// UPDATE against a missing row is not a driver error: zero rows changed is
	// a successful statement. The domain still needs to hear about it.
	if affected == 0 {
		return fmt.Errorf("upgrade plan for customer %d: %w", id, ErrCustomerNotFound)
	}

	return nil
}

// scanCustomer centralises the ErrNoRows translation so no caller ever has to
// compare against database/sql sentinels itself.
func scanCustomer(row *sql.Row, subject string) (Customer, error) {
	var customer Customer

	err := row.Scan(
		&customer.ID,
		&customer.Email,
		&customer.Name,
		&customer.Plan,
		&customer.apiToken,
	)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		// The query worked. It simply matched nothing.
		//
		// Two %w verbs: callers can match the domain error, while the driver
		// sentinel stays reachable underneath for low-level debugging.
		return Customer{}, fmt.Errorf("lookup customer by %s: %w: %w", subject, ErrCustomerNotFound, err)

	case err != nil:
		return Customer{}, fmt.Errorf("lookup customer by %s: %w", subject, err)
	}

	return customer, nil
}

// isUniqueViolation keeps the driver-specific string check in one place. With
// Postgres this would inspect a *pgconn.PgError code (23505) instead.
func isUniqueViolation(err error) bool {
	return strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT FAILED")
}

func firstLine(query string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(query), "\n")

	return strings.TrimSpace(line)
}

//
// SETUP
//

const schema = `
CREATE TABLE IF NOT EXISTS customers (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	email      TEXT NOT NULL UNIQUE,
	name       TEXT NOT NULL,
	plan       TEXT NOT NULL DEFAULT 'free',
	api_token  TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);`

func openDB(ctx context.Context) (*sql.DB, error) {
	path := os.Getenv("DB_PATH")

	if path == "" {
		path = defaultDBPath
	}

	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close database: %v", closeErr)
		}

		return nil, fmt.Errorf("ping database: %w", err)
	}

	if _, err := db.ExecContext(ctx, schema); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close database: %v", closeErr)
		}

		return nil, fmt.Errorf("create schema: %w", err)
	}

	return db, nil
}

//
// MAIN
//

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day43: ")

	ctx := context.Background()

	db, err := openDB(ctx)
	if err != nil {
		log.Fatalf("database unavailable: %v", err)
	}

	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	}()

	store, err := NewCustomerStore(ctx, db)
	if err != nil {
		log.Fatalf("prepare statements: %v", err)
	}

	// Statements outlive individual calls but not the process.
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("close statements: %v", err)
		}
	}()

	//
	// 1. One prepared INSERT, reused for the whole batch
	//

	fmt.Println("\n1) Batch insert through a single prepared statement")
	fmt.Println(strings.Repeat("-", 62))

	seed := []Customer{
		{Email: "ada@example.com", Name: "Ada Lovelace", Plan: "pro", apiToken: "tok_live_9f2c41a7"},
		{Email: "alan@example.com", Name: "Alan Turing", Plan: "free", apiToken: "tok_live_5b7e30d2"},
		{Email: "grace@example.com", Name: "Grace Hopper", Plan: "pro", apiToken: "tok_live_1c8a62f5"},
	}

	start := time.Now()

	for _, customer := range seed {
		created, err := store.Create(ctx, customer)
		if err != nil {
			log.Fatalf("seed: %v", err)
		}

		fmt.Printf("  created %s token=%s\n", created, created.TokenHint())
	}

	fmt.Printf("  %d rows in %s\n", len(seed), time.Since(start).Round(time.Microsecond))

	//
	// 2. Duplicate insert: a constraint violation, mapped to a domain error
	//

	fmt.Println("\n2) Duplicate email")
	fmt.Println(strings.Repeat("-", 62))

	_, err = store.Create(ctx, Customer{
		Email:    "ada@example.com",
		Name:     "Ada (second account)",
		Plan:     "free",
		apiToken: "tok_live_deadbeef",
	})

	if errors.Is(err, ErrDuplicateEmail) {
		fmt.Printf("  rejected as expected: %v\n", err)
	} else {
		log.Fatalf("expected ErrDuplicateEmail, got: %v", err)
	}

	//
	// 3. Found vs not found
	//

	fmt.Println("\n3) Lookups")
	fmt.Println(strings.Repeat("-", 62))

	found, err := store.ByEmail(ctx, "grace@example.com")
	if err != nil {
		log.Fatalf("lookup: %v", err)
	}

	fmt.Printf("  found: %s\n", found)

	_, err = store.ByEmail(ctx, "nobody@example.com")

	switch {
	case errors.Is(err, ErrCustomerNotFound):
		// This is the branch an HTTP handler turns into 404.
		fmt.Printf("  missing: %v -> HTTP 404\n", err)

	case err != nil:
		// Anything else is a real failure -> HTTP 500.
		log.Fatalf("unexpected lookup failure: %v", err)
	}

	// sql.ErrNoRows stays wrapped underneath, so low-level debugging is still
	// possible without handlers having to know about it.
	fmt.Printf("  errors.Is(err, sql.ErrNoRows) = %t (wrapped, not swallowed)\n",
		errors.Is(err, sql.ErrNoRows))

	//
	// 4. Hostile input against a parameterized query
	//

	fmt.Println("\n4) SQL injection attempt")
	fmt.Println(strings.Repeat("-", 62))

	hostile := "ada@example.com' OR '1'='1"

	fmt.Printf("  input: %s\n", hostile)

	_, err = store.ByEmail(ctx, hostile)

	if errors.Is(err, ErrCustomerNotFound) {
		fmt.Println("  result: no rows - the payload was compared as a literal string")
	} else {
		log.Fatalf("injection defence failed: %v", err)
	}

	// The unsafe version, for contrast. It is built but never executed.
	unsafe := fmt.Sprintf("SELECT id FROM customers WHERE email = '%s';", hostile)
	fmt.Printf("  never do this: %s\n", unsafe)
	fmt.Println("  ^ that string would return every customer row")

	//
	// 5. Update against a missing row
	//

	fmt.Println("\n5) Update paths")
	fmt.Println(strings.Repeat("-", 62))

	if err := store.UpgradePlan(ctx, found.ID, "enterprise"); err != nil {
		log.Fatalf("upgrade: %v", err)
	}

	fmt.Printf("  customer %d upgraded to enterprise\n", found.ID)

	err = store.UpgradePlan(ctx, 4242, "enterprise")

	if errors.Is(err, ErrCustomerNotFound) {
		fmt.Printf("  missing row: %v\n", err)
	} else {
		log.Fatalf("expected ErrCustomerNotFound, got: %v", err)
	}

	//
	// 6. Logging discipline
	//

	fmt.Println("\n6) What goes into the log")
	fmt.Println(strings.Repeat("-", 62))

	log.Printf("customer_lookup id=%d email=%s token=%s", found.ID, found.Email, found.TokenHint())
	fmt.Println("  the api_token column was read into memory but only its last 4 chars are logged")
}
