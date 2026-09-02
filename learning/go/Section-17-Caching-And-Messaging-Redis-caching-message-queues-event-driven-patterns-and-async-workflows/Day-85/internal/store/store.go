// Package store is the MVP's database layer: products, orders, an outbox and
// the consumer's dedupe table, all in one SQLite file so the API and the
// worker can be two separate processes sharing one database.
//
// The tables that matter for today:
//
//	outbox           - events written in the same transaction as the business
//	                   row, so a crash cannot create an order nobody hears about
//	processed_events - the consumer's claim table, so a redelivered event does
//	                   its work exactly once
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const Schema = `
CREATE TABLE IF NOT EXISTS products (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	name       TEXT    NOT NULL,
	price_cent INTEGER NOT NULL,
	version    INTEGER NOT NULL DEFAULT 1,
	updated_at TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS orders (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	product_id  INTEGER NOT NULL REFERENCES products(id),
	quantity    INTEGER NOT NULL,
	amount_cent INTEGER NOT NULL,
	status      TEXT    NOT NULL DEFAULT 'placed',
	created_at  TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS outbox (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	event_id     TEXT    NOT NULL UNIQUE,
	event_type   TEXT    NOT NULL,
	payload      TEXT    NOT NULL,
	created_at   TEXT    NOT NULL DEFAULT (datetime('now')),
	published_at TEXT,
	attempts     INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_outbox_unpublished
	ON outbox (id) WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS processed_events (
	claim        TEXT PRIMARY KEY,
	consumer     TEXT NOT NULL,
	event_id     TEXT NOT NULL,
	processed_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS receipts (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	order_id   INTEGER NOT NULL,
	body       TEXT    NOT NULL,
	created_at TEXT    NOT NULL DEFAULT (datetime('now'))
);`

// ErrNotFound is the store's own error, so handlers never import database/sql
// just to compare against sql.ErrNoRows.
var ErrNotFound = errors.New("not found")

// ErrAlreadyProcessed means a consumer already handled this event.
var ErrAlreadyProcessed = errors.New("event already processed")

type Product struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	PriceCent int64     `json:"price_cent"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Order struct {
	ID         int64  `json:"id"`
	ProductID  int64  `json:"product_id"`
	Quantity   int64  `json:"quantity"`
	AmountCent int64  `json:"amount_cent"`
	Status     string `json:"status"`
}

type Event struct {
	ID      int64
	EventID string
	Type    string
	Payload json.RawMessage
}

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// Open configures a connection for SQLite: WAL so a reader (the API) and a
// writer (the worker) do not block each other, and a busy timeout so a
// concurrent write waits instead of failing instantly.
func Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dsn, err)
	}

	if _, err := db.Exec(Schema); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return db, nil
}

//
// PRODUCTS - the read path that gets cached
//

func (s *Store) CreateProduct(ctx context.Context, name string, priceCent int64) (Product, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO products (name, price_cent) VALUES (?, ?);`, name, priceCent)
	if err != nil {
		return Product{}, fmt.Errorf("insert product: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Product{}, fmt.Errorf("read product id: %w", err)
	}

	return s.Product(ctx, id)
}

func (s *Store) Product(ctx context.Context, id int64) (Product, error) {
	var (
		product   Product
		updatedAt string
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, price_cent, version, updated_at FROM products WHERE id = ?;`, id).
		Scan(&product.ID, &product.Name, &product.PriceCent, &product.Version, &updatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return Product{}, fmt.Errorf("product %d: %w", id, ErrNotFound)
	}

	if err != nil {
		return Product{}, fmt.Errorf("select product %d: %w", id, err)
	}

	if product.UpdatedAt, err = time.Parse(time.DateTime, updatedAt); err != nil {
		return Product{}, fmt.Errorf("parse updated_at: %w", err)
	}

	return product, nil
}

// UpdatePrice bumps the version, which is what lets a cache entry be recognised
// as stale even if the invalidation was missed.
func (s *Store) UpdatePrice(ctx context.Context, id, priceCent int64) (Product, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE products SET price_cent = ?, version = version + 1, updated_at = datetime('now') WHERE id = ?;`,
		priceCent, id)
	if err != nil {
		return Product{}, fmt.Errorf("update product %d: %w", id, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return Product{}, fmt.Errorf("rows affected: %w", err)
	}

	if affected == 0 {
		return Product{}, fmt.Errorf("product %d: %w", id, ErrNotFound)
	}

	return s.Product(ctx, id)
}

//
// ORDERS - the write path that emits an event
//

// PlaceOrder writes the order AND its event in one transaction.
//
// This is the whole point of the outbox: there is no moment where the order
// exists but the event does not, and none where the event exists but the order
// does not.
func (s *Store) PlaceOrder(ctx context.Context, productID, quantity int64) (Order, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Order{}, fmt.Errorf("begin: %w", err)
	}

	committed := false

	defer func() {
		if committed {
			return
		}

		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			_ = err
		}
	}()

	var priceCent int64

	err = tx.QueryRowContext(ctx, `SELECT price_cent FROM products WHERE id = ?;`, productID).Scan(&priceCent)

	if errors.Is(err, sql.ErrNoRows) {
		return Order{}, fmt.Errorf("product %d: %w", productID, ErrNotFound)
	}

	if err != nil {
		return Order{}, fmt.Errorf("select price: %w", err)
	}

	order := Order{ProductID: productID, Quantity: quantity, AmountCent: priceCent * quantity, Status: "placed"}

	result, err := tx.ExecContext(ctx,
		`INSERT INTO orders (product_id, quantity, amount_cent) VALUES (?, ?, ?);`,
		order.ProductID, order.Quantity, order.AmountCent)
	if err != nil {
		return Order{}, fmt.Errorf("insert order: %w", err)
	}

	if order.ID, err = result.LastInsertId(); err != nil {
		return Order{}, fmt.Errorf("read order id: %w", err)
	}

	payload, err := json.Marshal(order)
	if err != nil {
		return Order{}, fmt.Errorf("encode event: %w", err)
	}

	// The event id is derived from the aggregate, not random: retrying the
	// whole operation cannot produce two events for one order.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO outbox (event_id, event_type, payload) VALUES (?, ?, ?);`,
		fmt.Sprintf("order-%d-placed", order.ID), "order.placed", string(payload)); err != nil {
		return Order{}, fmt.Errorf("insert outbox row: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Order{}, fmt.Errorf("commit: %w", err)
	}

	committed = true

	return order, nil
}

func (s *Store) Order(ctx context.Context, id int64) (Order, error) {
	var order Order

	err := s.db.QueryRowContext(ctx,
		`SELECT id, product_id, quantity, amount_cent, status FROM orders WHERE id = ?;`, id).
		Scan(&order.ID, &order.ProductID, &order.Quantity, &order.AmountCent, &order.Status)

	if errors.Is(err, sql.ErrNoRows) {
		return Order{}, fmt.Errorf("order %d: %w", id, ErrNotFound)
	}

	if err != nil {
		return Order{}, fmt.Errorf("select order %d: %w", id, err)
	}

	return order, nil
}

//
// OUTBOX
//

func (s *Store) UnpublishedEvents(ctx context.Context, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, event_id, event_type, payload FROM outbox
		 WHERE published_at IS NULL ORDER BY id LIMIT ?;`, limit)
	if err != nil {
		return nil, fmt.Errorf("select unpublished: %w", err)
	}

	defer func() {
		if err := rows.Close(); err != nil {
			_ = err
		}
	}()

	var events []Event

	for rows.Next() {
		var (
			event   Event
			payload string
		)

		if err := rows.Scan(&event.ID, &event.EventID, &event.Type, &payload); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}

		event.Payload = json.RawMessage(payload)

		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}

	return events, nil
}

func (s *Store) MarkPublished(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE outbox SET published_at = datetime('now') WHERE id = ?;`, id); err != nil {
		return fmt.Errorf("mark published: %w", err)
	}

	return nil
}

func (s *Store) RecordPublishFailure(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE outbox SET attempts = attempts + 1 WHERE id = ?;`, id); err != nil {
		return fmt.Errorf("record failure: %w", err)
	}

	return nil
}

func (s *Store) PendingEventCount(ctx context.Context) (int, error) {
	var count int

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox WHERE published_at IS NULL;`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count pending: %w", err)
	}

	return count, nil
}

//
// CONSUMER SIDE - exactly-once effects on top of at-least-once delivery
//

// ProcessOnce runs handler inside the transaction that claims the event.
//
// Claim and work commit together, so there is no state where the event is
// marked done but the work is missing (or the reverse).
func (s *Store) ProcessOnce(ctx context.Context, consumer, eventID string, handler func(context.Context, *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}

	committed := false

	defer func() {
		if committed {
			return
		}

		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			_ = err
		}
	}()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO processed_events (claim, consumer, event_id) VALUES (?, ?, ?);`,
		consumer+":"+eventID, consumer, eventID); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%s: %w", eventID, ErrAlreadyProcessed)
		}

		return fmt.Errorf("claim %s: %w", eventID, err)
	}

	if err := handler(ctx, tx); err != nil {
		// Rolling back drops the claim too, so a redelivery can retry.
		return fmt.Errorf("handle %s: %w", eventID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	committed = true

	return nil
}

func (s *Store) ReceiptCount(ctx context.Context, orderID int64) (int, error) {
	var count int

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM receipts WHERE order_id = ?;`, orderID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count receipts: %w", err)
	}

	return count, nil
}

func isUniqueViolation(err error) bool {
	message := strings.ToUpper(err.Error())

	return strings.Contains(message, "UNIQUE CONSTRAINT FAILED") ||
		strings.Contains(message, "SQLSTATE 23505")
}
