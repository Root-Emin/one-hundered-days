package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

/*
The SQL implementation of ProductRepository.

This is the only file in the program that knows SQL exists. A Postgres version
would be the same file with $1/$2 placeholders, RETURNING clauses instead of
LastInsertId, and pgx error codes instead of the string check below - nothing
above this layer would change.
*/

type SQLProductRepository struct {
	db *sql.DB
}

func NewSQLProductRepository(db *sql.DB) *SQLProductRepository {
	return &SQLProductRepository{db: db}
}

// Compile-time proof that the implementation still satisfies the interface.
// If a method signature drifts, the build breaks here instead of at a call site.
var _ ProductRepository = (*SQLProductRepository)(nil)

const productColumns = `id, sku, name, price_cents, cost_cents, stock, created_at, updated_at`

func (r *SQLProductRepository) Create(ctx context.Context, product Product) (Product, error) {
	const query = `
		INSERT INTO products (sku, name, price_cents, cost_cents, stock)
		VALUES (?, ?, ?, ?, ?);`

	result, err := r.db.ExecContext(ctx, query,
		product.SKU, product.Name, product.PriceCent, product.CostCents, product.Stock)
	if err != nil {
		if strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT FAILED") {
			return Product{}, fmt.Errorf("create product %s: %w", product.SKU, ErrConflict)
		}

		return Product{}, fmt.Errorf("create product %s: %w", product.SKU, err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Product{}, fmt.Errorf("create product %s: read id: %w", product.SKU, err)
	}

	return r.ByID(ctx, id)
}

func (r *SQLProductRepository) ByID(ctx context.Context, id int64) (Product, error) {
	query := `SELECT ` + productColumns + ` FROM products WHERE id = ?;`

	product, err := scanProduct(r.db.QueryRowContext(ctx, query, id))

	switch {
	case errors.Is(err, sql.ErrNoRows):
		// The driver sentinel stops here. Callers only ever see ErrNotFound.
		return Product{}, fmt.Errorf("product %d: %w", id, ErrNotFound)

	case err != nil:
		return Product{}, fmt.Errorf("select product %d: %w", id, err)
	}

	return product, nil
}

func (r *SQLProductRepository) BySKU(ctx context.Context, sku string) (Product, error) {
	query := `SELECT ` + productColumns + ` FROM products WHERE sku = ?;`

	product, err := scanProduct(r.db.QueryRowContext(ctx, query, sku))

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Product{}, fmt.Errorf("product %s: %w", sku, ErrNotFound)

	case err != nil:
		return Product{}, fmt.Errorf("select product %s: %w", sku, err)
	}

	return product, nil
}

func (r *SQLProductRepository) List(ctx context.Context, filter ProductFilter) ([]Product, error) {
	query := `
		SELECT ` + productColumns + `
		FROM products
		WHERE (stock > 0 OR NOT ?)
		ORDER BY sku
		LIMIT ? OFFSET ?;`

	rows, err := r.db.QueryContext(ctx, query, filter.InStockOnly, filter.Limit, filter.Offset)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}

	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("list products: close rows: %v", err)
		}
	}()

	products := make([]Product, 0, filter.Limit)

	for rows.Next() {
		product, err := scanProduct(rows)
		if err != nil {
			return nil, fmt.Errorf("list products: %w", err)
		}

		products = append(products, product)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}

	return products, nil
}

// ReserveStock puts the "enough stock?" check inside the UPDATE so two
// concurrent sales cannot both pass a check that only one of them can honour.
func (r *SQLProductRepository) ReserveStock(ctx context.Context, id int64, quantity int) (Product, error) {
	const query = `
		UPDATE products
		SET stock = stock - ?, updated_at = datetime('now')
		WHERE id = ? AND stock >= ?;`

	result, err := r.db.ExecContext(ctx, query, quantity, id, quantity)
	if err != nil {
		return Product{}, fmt.Errorf("reserve stock for product %d: %w", id, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return Product{}, fmt.Errorf("reserve stock for product %d: rows affected: %w", id, err)
	}

	if affected == 0 {
		// Missing row or not enough stock: ask the database which one.
		product, err := r.ByID(ctx, id)
		if err != nil {
			return Product{}, err
		}

		return Product{}, fmt.Errorf(
			"reserve %d of product %d: %w: only %d in stock",
			quantity, id, ErrValidation, product.Stock,
		)
	}

	return r.ByID(ctx, id)
}

func (r *SQLProductRepository) Delete(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM products WHERE id = ?;`, id)
	if err != nil {
		return fmt.Errorf("delete product %d: %w", id, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete product %d: rows affected: %w", id, err)
	}

	if affected == 0 {
		return fmt.Errorf("product %d: %w", id, ErrNotFound)
	}

	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

// scanProduct is the mapping between the database row shape and the domain
// type. Column order lives in exactly one place: productColumns.
func scanProduct(row rowScanner) (Product, error) {
	var (
		product              Product
		createdAt, updatedAt string
	)

	if err := row.Scan(
		&product.ID,
		&product.SKU,
		&product.Name,
		&product.PriceCent,
		&product.CostCents,
		&product.Stock,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Product{}, err
	}

	var err error

	if product.CreatedAt, err = time.Parse(time.DateTime, createdAt); err != nil {
		return Product{}, fmt.Errorf("parse created_at %q: %w", createdAt, err)
	}

	if product.UpdatedAt, err = time.Parse(time.DateTime, updatedAt); err != nil {
		return Product{}, fmt.Errorf("parse updated_at %q: %w", updatedAt, err)
	}

	return product, nil
}

const productSchema = `
CREATE TABLE IF NOT EXISTS products (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	sku         TEXT    NOT NULL UNIQUE,
	name        TEXT    NOT NULL,
	price_cents INTEGER NOT NULL CHECK (price_cents > 0),
	cost_cents  INTEGER NOT NULL DEFAULT 0 CHECK (cost_cents >= 0),
	stock       INTEGER NOT NULL DEFAULT 0 CHECK (stock >= 0),
	created_at  TEXT    NOT NULL DEFAULT (datetime('now')),
	updated_at  TEXT    NOT NULL DEFAULT (datetime('now'))
);`

func openProductDB(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close database: %v", closeErr)
		}

		return nil, fmt.Errorf("ping database: %w", err)
	}

	if _, err := db.ExecContext(ctx, productSchema); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close database: %v", closeErr)
		}

		return nil, fmt.Errorf("create schema: %w", err)
	}

	return db, nil
}
