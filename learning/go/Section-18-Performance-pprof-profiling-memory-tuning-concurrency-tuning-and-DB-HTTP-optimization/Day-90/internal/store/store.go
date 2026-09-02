// Package store is the MVP's data layer, with the hot query written four ways.
//
// The versions are the four rounds of one optimization cycle. Each is kept, so
// the demo can measure them side by side and the tests can prove they all
// return the same answer:
//
//	V1  N+1 queries, no index          the honest first draft
//	V2  N+1 queries, with an index     one migration, no code change
//	V3  one query with a JOIN          the round trips go away
//	V4  the same query, preallocated   the allocations go away
//
// The order is not arbitrary. It is cheapest-first, and each round is measured
// before the next one starts - which is what stops "optimization" from
// becoming a rewrite nobody can review.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

const Schema = `
CREATE TABLE IF NOT EXISTS customers (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	name    TEXT    NOT NULL,
	tier    TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS orders (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	customer_id INTEGER NOT NULL REFERENCES customers(id),
	status      TEXT    NOT NULL,
	amount_cent INTEGER NOT NULL,
	placed_at   TEXT    NOT NULL
);`

// IndexSQL is round two: a migration, no application change.
const IndexSQL = `CREATE INDEX IF NOT EXISTS idx_orders_customer ON orders (customer_id, id);`

type Customer struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Tier string `json:"tier"`
}

type Order struct {
	ID         int64  `json:"id"`
	Status     string `json:"status"`
	AmountCent int64  `json:"amount_cent"`
	PlacedAt   string `json:"placed_at"`
}

// Row is what the dashboard renders: a customer, their orders, and a total.
type Row struct {
	Customer   Customer `json:"customer"`
	Orders     []Order  `json:"orders"`
	TotalCent  int64    `json:"total_cent"`
	OrderCount int      `json:"order_count"`
}

type Store struct {
	db *sql.DB

	// latency simulates the network hop to a database on another host.
	// SQLite runs in-process, where a query costs microseconds and N+1 looks
	// almost free - which is exactly the illusion that lets N+1 reach
	// production. Everything measured here uses a realistic 0.3ms round trip.
	latency time.Duration
}

func New(db *sql.DB, latency time.Duration) *Store {
	return &Store{db: db, latency: latency}
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) roundTrip() {
	if s.latency > 0 {
		time.Sleep(s.latency)
	}
}

func (s *Store) Exec(ctx context.Context, script string) error {
	for _, statement := range splitStatements(script) {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("exec %.40q: %w", statement, err)
		}
	}

	return nil
}

// splitStatements strips whole-line "--" comments BEFORE splitting on ";",
// because a semicolon inside a comment would cut a statement in half.
func splitStatements(script string) []string {
	var cleaned []string

	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}

		cleaned = append(cleaned, line)
	}

	var statements []string

	for _, statement := range strings.Split(strings.Join(cleaned, "\n"), ";") {
		if trimmed := strings.TrimSpace(statement); trimmed != "" {
			statements = append(statements, trimmed)
		}
	}

	return statements
}

// Queries counts round trips, which is the number the profile cannot show you.
type Queries struct {
	Count int
}

//
// V1/V2: N+1
//

// DashboardNPlusOne fetches the customers, then their orders one customer at a
// time. V1 and V2 run identical code - the only difference is whether the
// index exists, which is the point: a migration alone can be the whole fix.
func (s *Store) DashboardNPlusOne(ctx context.Context, tier string, limit int) ([]Row, Queries, error) {
	queries := Queries{}

	customers, err := s.customers(ctx, tier, limit, &queries)
	if err != nil {
		return nil, queries, err
	}

	rows := make([]Row, 0, len(customers))

	for _, customer := range customers {
		s.roundTrip()

		queries.Count++

		result, err := s.db.QueryContext(ctx,
			`SELECT id, status, amount_cent, placed_at FROM orders WHERE customer_id = ? ORDER BY id;`,
			customer.ID)
		if err != nil {
			return nil, queries, fmt.Errorf("select orders for %d: %w", customer.ID, err)
		}

		row := Row{Customer: customer}

		for result.Next() {
			var order Order

			if err := result.Scan(&order.ID, &order.Status, &order.AmountCent, &order.PlacedAt); err != nil {
				closeRows(result)

				return nil, queries, fmt.Errorf("scan order: %w", err)
			}

			row.Orders = append(row.Orders, order)
			row.TotalCent += order.AmountCent
		}

		if err := result.Err(); err != nil {
			closeRows(result)

			return nil, queries, fmt.Errorf("iterate orders: %w", err)
		}

		closeRows(result)

		row.OrderCount = len(row.Orders)

		rows = append(rows, row)
	}

	return rows, queries, nil
}

//
// V3/V4: one query
//

// DashboardJoined is round three: one round trip instead of limit+1.
//
// preallocate is round four. The query is identical; the difference is whether
// the result slices are sized up front or grown by append. Keeping them in one
// function makes the comparison exact - two implementations that differ only
// in the thing being measured.
func (s *Store) DashboardJoined(ctx context.Context, tier string, limit int, preallocate bool) ([]Row, Queries, error) {
	queries := Queries{Count: 1}

	s.roundTrip()

	result, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.name, c.tier, o.id, o.status, o.amount_cent, o.placed_at
		FROM (SELECT id, name, tier FROM customers WHERE tier = ? ORDER BY id LIMIT ?) AS c
		LEFT JOIN orders AS o ON o.customer_id = c.id
		ORDER BY c.id, o.id;`, tier, limit)
	if err != nil {
		return nil, queries, fmt.Errorf("select dashboard: %w", err)
	}

	defer closeRows(result)

	var rows []Row

	if preallocate {
		// One allocation for the whole page instead of log2(n) growths.
		rows = make([]Row, 0, limit)
	}

	var current *Row

	for result.Next() {
		var (
			customer Customer
			order    Order

			// LEFT JOIN: a customer with no orders produces NULLs, which only
			// scan into pointers or sql.Null* types.
			orderID sql.NullInt64
			status  sql.NullString
			amount  sql.NullInt64
			placed  sql.NullString
		)

		if err := result.Scan(&customer.ID, &customer.Name, &customer.Tier,
			&orderID, &status, &amount, &placed); err != nil {
			return nil, queries, fmt.Errorf("scan dashboard row: %w", err)
		}

		if current == nil || current.Customer.ID != customer.ID {
			row := Row{Customer: customer}

			if preallocate {
				// Sized from the fixture's shape. Being roughly right removes
				// nearly all the growth; being exact is not worth the query it
				// would take to find out.
				row.Orders = make([]Order, 0, 16)
			}

			rows = append(rows, row)
			current = &rows[len(rows)-1]
		}

		if !orderID.Valid {
			continue
		}

		order = Order{
			ID:         orderID.Int64,
			Status:     status.String,
			AmountCent: amount.Int64,
			PlacedAt:   placed.String,
		}

		current.Orders = append(current.Orders, order)
		current.TotalCent += order.AmountCent
		current.OrderCount++
	}

	if err := result.Err(); err != nil {
		return nil, queries, fmt.Errorf("iterate dashboard rows: %w", err)
	}

	return rows, queries, nil
}

func (s *Store) customers(ctx context.Context, tier string, limit int, queries *Queries) ([]Customer, error) {
	s.roundTrip()

	queries.Count++

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, tier FROM customers WHERE tier = ? ORDER BY id LIMIT ?;`, tier, limit)
	if err != nil {
		return nil, fmt.Errorf("select customers: %w", err)
	}

	defer closeRows(rows)

	customers := make([]Customer, 0, limit)

	for rows.Next() {
		var customer Customer

		if err := rows.Scan(&customer.ID, &customer.Name, &customer.Tier); err != nil {
			return nil, fmt.Errorf("scan customer: %w", err)
		}

		customers = append(customers, customer)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate customers: %w", err)
	}

	return customers, nil
}

func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		_ = err
	}
}

// Explain returns the query plan, so a test can assert the index is used
// rather than merely present.
func (s *Store) Explain(ctx context.Context, query string, args ...any) (string, error) {
	rows, err := s.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		return "", fmt.Errorf("explain: %w", err)
	}

	defer closeRows(rows)

	var lines []string

	for rows.Next() {
		var (
			id, parent, unused int
			detail             string
		)

		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			return "", fmt.Errorf("scan plan: %w", err)
		}

		lines = append(lines, detail)
	}

	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate plan: %w", err)
	}

	return strings.Join(lines, "\n"), nil
}

// Seeds fills the database deterministically.
func Seed(ctx context.Context, store *Store, customers, ordersPerCustomer int) error {
	source := rand.New(rand.NewSource(90))

	tiers := []string{"free", "pro", "enterprise"}
	statuses := []string{"placed", "paid", "shipped"}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}

	committed := false

	defer func() {
		if committed {
			return
		}

		if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
			_ = err
		}
	}()

	customerStatement, err := tx.PrepareContext(ctx, `INSERT INTO customers (name, tier) VALUES (?, ?);`)
	if err != nil {
		return fmt.Errorf("prepare customers: %w", err)
	}

	defer func() {
		if err := customerStatement.Close(); err != nil {
			_ = err
		}
	}()

	orderStatement, err := tx.PrepareContext(ctx,
		`INSERT INTO orders (customer_id, status, amount_cent, placed_at) VALUES (?, ?, ?, ?);`)
	if err != nil {
		return fmt.Errorf("prepare orders: %w", err)
	}

	defer func() {
		if err := orderStatement.Close(); err != nil {
			_ = err
		}
	}()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 1; i <= customers; i++ {
		result, err := customerStatement.ExecContext(ctx,
			fmt.Sprintf("customer-%05d", i), tiers[i%len(tiers)])
		if err != nil {
			return fmt.Errorf("insert customer: %w", err)
		}

		id, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("customer id: %w", err)
		}

		for j := 0; j < ordersPerCustomer; j++ {
			if _, err := orderStatement.ExecContext(ctx,
				id,
				statuses[source.Intn(len(statuses))],
				int64(500+source.Intn(90000)),
				base.Add(time.Duration(source.Intn(8000))*time.Hour).Format(time.RFC3339),
			); err != nil {
				return fmt.Errorf("insert order: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	committed = true

	return nil
}
