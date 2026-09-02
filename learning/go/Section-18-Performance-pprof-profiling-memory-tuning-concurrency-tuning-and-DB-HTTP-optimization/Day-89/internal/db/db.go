// Package db is the database half of Day 89: query plans, indexes, N+1, and
// batching.
//
// The theme is round trips. A query that takes 200 microseconds is not slow;
// ten thousand of them in a loop is four seconds of latency that no amount of
// CPU tuning will recover. Most "slow service" tickets are round trips, not
// algorithms.
package db

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
	country TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS orders (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	customer_id INTEGER NOT NULL REFERENCES customers(id),
	status      TEXT    NOT NULL,
	amount_cent INTEGER NOT NULL,
	created_at  TEXT    NOT NULL
);`

// IndexSQL is applied separately so a demo can measure the same query with and
// without it.
//
// The column order matters: (customer_id, status) serves a lookup by customer,
// and a lookup by customer AND status. It does NOT serve a lookup by status
// alone - an index is usable left-to-right, like a phone book sorted by
// surname then first name.
const IndexSQL = `
CREATE INDEX IF NOT EXISTS idx_orders_customer_status ON orders (customer_id, status);
CREATE INDEX IF NOT EXISTS idx_orders_created_at ON orders (created_at);`

const DropIndexSQL = `
DROP INDEX IF EXISTS idx_orders_customer_status;
DROP INDEX IF EXISTS idx_orders_created_at;`

type Order struct {
	ID         int64
	CustomerID int64
	Status     string
	AmountCent int64
	CreatedAt  string
}

type Customer struct {
	ID      int64
	Name    string
	Country string
}

// CustomerOrders is the shape a page actually needs: a customer plus their
// orders. Fetching it is where N+1 comes from.
type CustomerOrders struct {
	Customer Customer
	Orders   []Order
}

type Store struct {
	db *sql.DB

	// latency simulates network round-trip time.
	//
	// SQLite runs inside the process, so a query costs microseconds and N+1
	// looks almost free - which is exactly the trap. A real database sits
	// across a network, and every round trip pays for it. Setting this makes
	// the demo behave like a database on another host, and it is the honest
	// way to show a cost that localhost hides.
	latency time.Duration
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// SetLatency makes every query pay a simulated network round trip.
func (s *Store) SetLatency(latency time.Duration) {
	s.latency = latency
}

// roundTrip is called once per query, at the point where a real driver would
// be writing bytes to a socket.
func (s *Store) roundTrip() {
	if s.latency > 0 {
		time.Sleep(s.latency)
	}
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) Exec(ctx context.Context, statements string) error {
	for _, statement := range splitStatements(statements) {
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

//
// 1. QUERY PLANS
//

// Explain runs EXPLAIN QUERY PLAN and returns the plan as text.
//
// This is the only way to know whether an index is being used. "It has an
// index" is not the same as "the query uses the index" - a function around the
// column, a leading wildcard in LIKE, or a type mismatch all quietly disable
// it, and the query still returns the right answer, just slowly.
//
// Read for: SCAN (reads every row) versus SEARCH ... USING INDEX.
func (s *Store) Explain(ctx context.Context, query string, args ...any) (string, error) {
	rows, err := s.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		return "", fmt.Errorf("explain: %w", err)
	}

	defer func() {
		if err := rows.Close(); err != nil {
			_ = err
		}
	}()

	var lines []string

	for rows.Next() {
		var (
			id, parent, notUsed int
			detail              string
		)

		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			return "", fmt.Errorf("scan plan row: %w", err)
		}

		lines = append(lines, detail)
	}

	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate plan: %w", err)
	}

	return strings.Join(lines, "\n"), nil
}

// UsesIndex reports whether a plan touches an index at all.
//
// This is the weaker of the two checks, and on its own it is misleading: a
// plan can say "SCAN orders USING COVERING INDEX" and still read every entry
// from one end to the other. Cheaper than reading the table, still O(n).
func UsesIndex(plan string) bool {
	return strings.Contains(plan, "USING INDEX") || strings.Contains(plan, "USING COVERING INDEX")
}

// Seeks reports whether the plan SEARCHes rather than SCANs - that is, whether
// the database jumps straight to the matching rows.
//
// This is the question worth asking. SEARCH is a seek: log(n) to find the
// range, then read only what matches. SCAN reads everything, index or not.
func Seeks(plan string) bool {
	return strings.Contains(plan, "SEARCH")
}

// OrdersForCustomer is the query the index exists for.
func (s *Store) OrdersForCustomer(ctx context.Context, customerID int64, status string) ([]Order, error) {
	s.roundTrip()

	rows, err := s.db.QueryContext(ctx, OrdersForCustomerSQL, customerID, status)
	if err != nil {
		return nil, fmt.Errorf("select orders: %w", err)
	}

	return scanOrders(rows)
}

const OrdersForCustomerSQL = `
SELECT id, customer_id, status, amount_cent, created_at
FROM orders
WHERE customer_id = ? AND status = ?
ORDER BY id;`

//
// 2. N+1 VERSUS ONE QUERY
//

// LoadNPlusOne is the shape almost every ORM produces by default: one query
// for the list, then one more per row.
//
// Each individual query is fast. The problem is arithmetic: 200 customers at
// 0.2 ms is 40 ms of pure round trips, and it grows linearly with the page
// size while the CPU sits idle.
func (s *Store) LoadNPlusOne(ctx context.Context, country string, limit int) ([]CustomerOrders, int, error) {
	queries := 0

	customers, err := s.customers(ctx, country, limit)
	if err != nil {
		return nil, queries, err
	}

	queries++

	result := make([]CustomerOrders, 0, len(customers))

	for _, customer := range customers {
		s.roundTrip()

		rows, err := s.db.QueryContext(ctx,
			`SELECT id, customer_id, status, amount_cent, created_at FROM orders WHERE customer_id = ? ORDER BY id;`,
			customer.ID)
		if err != nil {
			return nil, queries, fmt.Errorf("select orders for %d: %w", customer.ID, err)
		}

		queries++

		orders, err := scanOrders(rows)
		if err != nil {
			return nil, queries, err
		}

		result = append(result, CustomerOrders{Customer: customer, Orders: orders})
	}

	return result, queries, nil
}

// LoadJoined does it in one round trip.
//
// The rows come back denormalised - the customer repeats on every order - and
// the code stitches them back into a tree. That stitching is the price, and it
// is paid in CPU, which is enormously cheaper than a network round trip.
func (s *Store) LoadJoined(ctx context.Context, country string, limit int) ([]CustomerOrders, int, error) {
	s.roundTrip()

	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.name, c.country,
		       o.id, o.customer_id, o.status, o.amount_cent, o.created_at
		FROM (SELECT id, name, country FROM customers WHERE country = ? ORDER BY id LIMIT ?) AS c
		LEFT JOIN orders AS o ON o.customer_id = c.id
		ORDER BY c.id, o.id;`, country, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("select joined: %w", err)
	}

	defer func() {
		if err := rows.Close(); err != nil {
			_ = err
		}
	}()

	var (
		result  []CustomerOrders
		current *CustomerOrders
	)

	for rows.Next() {
		var (
			customer Customer
			order    Order

			// LEFT JOIN means a customer with no orders produces NULLs, which
			// only scan into pointers or sql.Null* types.
			orderID    sql.NullInt64
			customerID sql.NullInt64
			status     sql.NullString
			amount     sql.NullInt64
			createdAt  sql.NullString
		)

		if err := rows.Scan(&customer.ID, &customer.Name, &customer.Country,
			&orderID, &customerID, &status, &amount, &createdAt); err != nil {
			return nil, 0, fmt.Errorf("scan joined row: %w", err)
		}

		if current == nil || current.Customer.ID != customer.ID {
			result = append(result, CustomerOrders{Customer: customer})
			current = &result[len(result)-1]
		}

		if !orderID.Valid {
			continue
		}

		order = Order{
			ID:         orderID.Int64,
			CustomerID: customerID.Int64,
			Status:     status.String,
			AmountCent: amount.Int64,
			CreatedAt:  createdAt.String,
		}

		current.Orders = append(current.Orders, order)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate joined rows: %w", err)
	}

	return result, 1, nil
}

// LoadBatched is the middle road: two queries, one for the parents and one
// with IN (...) for all the children.
//
// Often the best of the three: no denormalised duplication over the wire, no
// per-row round trip, and it composes when the children come from a different
// service or a different database, where a JOIN is not available at all.
func (s *Store) LoadBatched(ctx context.Context, country string, limit int) ([]CustomerOrders, int, error) {
	customers, err := s.customers(ctx, country, limit)
	if err != nil {
		return nil, 0, err
	}

	if len(customers) == 0 {
		return nil, 1, nil
	}

	// Placeholders have to be built for the exact number of ids. Do not build
	// this by concatenating the values themselves - that is how SQL injection
	// gets in, and it also defeats the database's statement cache.
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(customers)), ",")

	args := make([]any, len(customers))
	index := make(map[int64]*CustomerOrders, len(customers))

	result := make([]CustomerOrders, len(customers))

	for i, customer := range customers {
		args[i] = customer.ID

		result[i] = CustomerOrders{Customer: customer}
		index[customer.ID] = &result[i]
	}

	s.roundTrip()

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(
		`SELECT id, customer_id, status, amount_cent, created_at
		 FROM orders WHERE customer_id IN (%s) ORDER BY customer_id, id;`, placeholders), args...)
	if err != nil {
		return nil, 2, fmt.Errorf("select orders batch: %w", err)
	}

	orders, err := scanOrders(rows)
	if err != nil {
		return nil, 2, err
	}

	for _, order := range orders {
		if parent, found := index[order.CustomerID]; found {
			parent.Orders = append(parent.Orders, order)
		}
	}

	return result, 2, nil
}

func (s *Store) customers(ctx context.Context, country string, limit int) ([]Customer, error) {
	s.roundTrip()

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, country FROM customers WHERE country = ? ORDER BY id LIMIT ?;`, country, limit)
	if err != nil {
		return nil, fmt.Errorf("select customers: %w", err)
	}

	defer func() {
		if err := rows.Close(); err != nil {
			_ = err
		}
	}()

	var customers []Customer

	for rows.Next() {
		var customer Customer

		if err := rows.Scan(&customer.ID, &customer.Name, &customer.Country); err != nil {
			return nil, fmt.Errorf("scan customer: %w", err)
		}

		customers = append(customers, customer)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate customers: %w", err)
	}

	return customers, nil
}

func scanOrders(rows *sql.Rows) ([]Order, error) {
	defer func() {
		if err := rows.Close(); err != nil {
			_ = err
		}
	}()

	var orders []Order

	for rows.Next() {
		var order Order

		if err := rows.Scan(&order.ID, &order.CustomerID, &order.Status,
			&order.AmountCent, &order.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan order: %w", err)
		}

		orders = append(orders, order)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate orders: %w", err)
	}

	return orders, nil
}

//
// 3. BATCHED WRITES
//

// InsertOneByOne is the baseline: a statement per row, each in its own
// implicit transaction.
//
// On a durable database that means a disk sync per row. It is the difference
// between "the import takes a minute" and "the import takes an hour".
func (s *Store) InsertOneByOne(ctx context.Context, orders []Order) error {
	for _, order := range orders {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO orders (customer_id, status, amount_cent, created_at) VALUES (?, ?, ?, ?);`,
			order.CustomerID, order.Status, order.AmountCent, order.CreatedAt); err != nil {
			return fmt.Errorf("insert order: %w", err)
		}
	}

	return nil
}

// InsertInTransaction wraps the same statements in one transaction.
//
// Usually the single biggest win available, and the smallest change: one
// commit, one sync, instead of one per row.
func (s *Store) InsertInTransaction(ctx context.Context, orders []Order) error {
	tx, err := s.db.BeginTx(ctx, nil)
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

	// Prepared once, executed n times: the database parses and plans the
	// statement a single time.
	statement, err := tx.PrepareContext(ctx,
		`INSERT INTO orders (customer_id, status, amount_cent, created_at) VALUES (?, ?, ?, ?);`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}

	defer func() {
		if err := statement.Close(); err != nil {
			_ = err
		}
	}()

	for _, order := range orders {
		if _, err := statement.ExecContext(ctx,
			order.CustomerID, order.Status, order.AmountCent, order.CreatedAt); err != nil {
			return fmt.Errorf("insert order: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	committed = true

	return nil
}

// MaxBatchRows keeps a multi-row INSERT under the driver's parameter limit.
//
// SQLite's default is 32,766 variables; PostgreSQL's protocol caps at 65,535.
// Four columns per row means 8,000 rows is a safe, boring choice.
const MaxBatchRows = 500

// InsertBatched builds multi-row INSERT statements: one round trip per chunk.
//
// Chunking is not optional. An unbounded batch either exceeds the parameter
// limit or produces a statement so large the database spends its time parsing.
func (s *Store) InsertBatched(ctx context.Context, orders []Order, batchSize int) (int, error) {
	if batchSize < 1 || batchSize > MaxBatchRows {
		batchSize = MaxBatchRows
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
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

	statements := 0

	for start := 0; start < len(orders); start += batchSize {
		end := min(start+batchSize, len(orders))

		chunk := orders[start:end]

		var (
			builder strings.Builder
			args    = make([]any, 0, len(chunk)*4)
		)

		builder.WriteString(`INSERT INTO orders (customer_id, status, amount_cent, created_at) VALUES `)

		for i, order := range chunk {
			if i > 0 {
				builder.WriteByte(',')
			}

			builder.WriteString("(?,?,?,?)")

			args = append(args, order.CustomerID, order.Status, order.AmountCent, order.CreatedAt)
		}

		builder.WriteByte(';')

		if _, err := tx.ExecContext(ctx, builder.String(), args...); err != nil {
			return statements, fmt.Errorf("insert batch: %w", err)
		}

		statements++
	}

	if err := tx.Commit(); err != nil {
		return statements, fmt.Errorf("commit: %w", err)
	}

	committed = true

	return statements, nil
}

func (s *Store) CountOrders(ctx context.Context) (int, error) {
	var count int

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders;`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count orders: %w", err)
	}

	return count, nil
}

//
// SEED DATA
//

// Seed fills the database deterministically so every measurement sees the same
// rows.
func Seed(ctx context.Context, store *Store, customers, ordersPerCustomer int) error {
	source := rand.New(rand.NewSource(42))

	countries := []string{"TR", "DE", "US", "JP"}
	statuses := []string{"placed", "paid", "shipped", "cancelled"}

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

	customerStatement, err := tx.PrepareContext(ctx,
		`INSERT INTO customers (name, country) VALUES (?, ?);`)
	if err != nil {
		return fmt.Errorf("prepare customer insert: %w", err)
	}

	defer func() {
		if err := customerStatement.Close(); err != nil {
			_ = err
		}
	}()

	orderStatement, err := tx.PrepareContext(ctx,
		`INSERT INTO orders (customer_id, status, amount_cent, created_at) VALUES (?, ?, ?, ?);`)
	if err != nil {
		return fmt.Errorf("prepare order insert: %w", err)
	}

	defer func() {
		if err := orderStatement.Close(); err != nil {
			_ = err
		}
	}()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 1; i <= customers; i++ {
		result, err := customerStatement.ExecContext(ctx,
			fmt.Sprintf("customer-%04d", i), countries[source.Intn(len(countries))])
		if err != nil {
			return fmt.Errorf("insert customer: %w", err)
		}

		customerID, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("read customer id: %w", err)
		}

		for j := 0; j < ordersPerCustomer; j++ {
			if _, err := orderStatement.ExecContext(ctx,
				customerID,
				statuses[source.Intn(len(statuses))],
				int64(100+source.Intn(50000)),
				base.Add(time.Duration(source.Intn(10000))*time.Hour).Format(time.RFC3339),
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

// SampleOrders builds rows for the write benchmarks.
func SampleOrders(count int, customerID int64) []Order {
	orders := make([]Order, count)

	for i := range orders {
		orders[i] = Order{
			CustomerID: customerID,
			Status:     "placed",
			AmountCent: int64(1000 + i),
			CreatedAt:  time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
		}
	}

	return orders
}
