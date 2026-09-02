package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

/*
The SQL implementations of the repository interfaces.

Every method here follows the same four rules:

 1. take SQL from queries.go by name, never inline
 2. bind values as parameters, never format them into the statement
 3. translate driver errors into domain errors before returning
 4. close every *sql.Rows and check rows.Err()
*/

// dbtx is satisfied by *sql.DB and *sql.Tx, which is what lets the same
// repository code run inside or outside a transaction.
type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

//
// CUSTOMERS
//

type SQLCustomerRepository struct {
	db dbtx
}

func NewSQLCustomerRepository(db dbtx) *SQLCustomerRepository {
	return &SQLCustomerRepository{db: db}
}

var _ CustomerRepository = (*SQLCustomerRepository)(nil)

func (r *SQLCustomerRepository) Create(ctx context.Context, customer Customer) (Customer, error) {
	customer.Normalize()

	if err := customer.Validate(); err != nil {
		return Customer{}, err
	}

	result, err := r.db.ExecContext(ctx, InsertCustomerSQL, customer.Email, customer.Name)
	if err != nil {
		if isUniqueViolation(err) {
			return Customer{}, fmt.Errorf("create customer %s: %w", customer.Email, ErrConflict)
		}

		return Customer{}, fmt.Errorf("create customer %s: %w", customer.Email, err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Customer{}, fmt.Errorf("create customer %s: read id: %w", customer.Email, err)
	}

	return r.ByID(ctx, id)
}

func (r *SQLCustomerRepository) ByID(ctx context.Context, id int64) (Customer, error) {
	customer, err := scanCustomer(r.db.QueryRowContext(ctx, SelectCustomerByIDSQL, id))

	return customer, wrapLookup(err, fmt.Sprintf("customer %d", id))
}

func (r *SQLCustomerRepository) ByEmail(ctx context.Context, email string) (Customer, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	customer, err := scanCustomer(r.db.QueryRowContext(ctx, SelectCustomerByEmailSQL, email))

	return customer, wrapLookup(err, fmt.Sprintf("customer %s", email))
}

func (r *SQLCustomerRepository) List(ctx context.Context, limit, offset int) ([]Customer, error) {
	rows, err := r.db.QueryContext(ctx, SelectCustomersSQL, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list customers: %w", err)
	}

	defer closeRows(rows)

	customers := make([]Customer, 0, limit)

	for rows.Next() {
		customer, err := scanCustomer(rows)
		if err != nil {
			return nil, fmt.Errorf("list customers: %w", err)
		}

		customers = append(customers, customer)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list customers: %w", err)
	}

	return customers, nil
}

func (r *SQLCustomerRepository) Delete(ctx context.Context, id int64) error {
	return execExpectingRow(ctx, r.db, DeleteCustomerSQL, fmt.Sprintf("customer %d", id), id)
}

//
// PRODUCTS
//

type SQLProductRepository struct {
	db dbtx
}

func NewSQLProductRepository(db dbtx) *SQLProductRepository {
	return &SQLProductRepository{db: db}
}

var _ ProductRepository = (*SQLProductRepository)(nil)

func (r *SQLProductRepository) Create(ctx context.Context, product Product) (Product, error) {
	product.Normalize()

	if err := product.Validate(); err != nil {
		return Product{}, err
	}

	result, err := r.db.ExecContext(ctx, InsertProductSQL,
		product.SKU, product.Name, product.PriceCents, product.Stock)
	if err != nil {
		if isUniqueViolation(err) {
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
	product, err := scanProduct(r.db.QueryRowContext(ctx, SelectProductByIDSQL, id))

	return product, wrapLookup(err, fmt.Sprintf("product %d", id))
}

func (r *SQLProductRepository) BySKU(ctx context.Context, sku string) (Product, error) {
	sku = strings.ToUpper(strings.TrimSpace(sku))

	product, err := scanProduct(r.db.QueryRowContext(ctx, SelectProductBySKUSQL, sku))

	return product, wrapLookup(err, fmt.Sprintf("product %s", sku))
}

func (r *SQLProductRepository) List(ctx context.Context, limit, offset int) ([]Product, error) {
	rows, err := r.db.QueryContext(ctx, SelectProductsSQL, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}

	defer closeRows(rows)

	products := make([]Product, 0, limit)

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

func (r *SQLProductRepository) ReserveStock(ctx context.Context, productID int64, quantity int) error {
	if quantity <= 0 {
		return fmt.Errorf("reserve stock: %w: quantity must be positive", ErrValidation)
	}

	result, err := r.db.ExecContext(ctx, ReserveProductStockSQL, quantity, productID, quantity)
	if err != nil {
		return fmt.Errorf("reserve stock for product %d: %w", productID, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("reserve stock for product %d: rows affected: %w", productID, err)
	}

	if affected == 0 {
		// Distinguish "no such product" from "not enough left".
		if _, err := r.ByID(ctx, productID); err != nil {
			return err
		}

		return fmt.Errorf("reserve %d of product %d: %w", quantity, productID, ErrOutOfStock)
	}

	return nil
}

func (r *SQLProductRepository) ReleaseStock(ctx context.Context, productID int64, quantity int) error {
	if quantity <= 0 {
		return fmt.Errorf("release stock: %w: quantity must be positive", ErrValidation)
	}

	return execExpectingRow(ctx, r.db, ReleaseProductStockSQL,
		fmt.Sprintf("product %d", productID), quantity, productID)
}

//
// ORDERS
//

type SQLOrderRepository struct {
	db dbtx
}

func NewSQLOrderRepository(db dbtx) *SQLOrderRepository {
	return &SQLOrderRepository{db: db}
}

var _ OrderRepository = (*SQLOrderRepository)(nil)

// Create writes the order header and all of its items. It expects to be
// called inside a transaction (the service always does), so a failure halfway
// through leaves nothing behind.
func (r *SQLOrderRepository) Create(ctx context.Context, order Order) (Order, error) {
	if len(order.Items) == 0 {
		return Order{}, fmt.Errorf("create order: %w: at least one item is required", ErrValidation)
	}

	if order.Status == "" {
		order.Status = StatusPlaced
	}

	result, err := r.db.ExecContext(ctx, InsertOrderSQL,
		order.CustomerID, string(order.Status), order.TotalCents)
	if err != nil {
		if isForeignKeyViolation(err) {
			return Order{}, fmt.Errorf("create order for customer %d: %w", order.CustomerID, ErrNotFound)
		}

		return Order{}, fmt.Errorf("create order for customer %d: %w", order.CustomerID, err)
	}

	orderID, err := result.LastInsertId()
	if err != nil {
		return Order{}, fmt.Errorf("create order: read id: %w", err)
	}

	for _, item := range order.Items {
		if _, err := r.db.ExecContext(ctx, InsertOrderItemSQL,
			orderID, item.ProductID, item.Quantity, item.UnitCents); err != nil {
			if isForeignKeyViolation(err) {
				return Order{}, fmt.Errorf("create order item for product %d: %w", item.ProductID, ErrNotFound)
			}

			return Order{}, fmt.Errorf("create order item for product %d: %w", item.ProductID, err)
		}
	}

	return r.ByID(ctx, orderID)
}

func (r *SQLOrderRepository) ByID(ctx context.Context, id int64) (Order, error) {
	order, err := scanOrder(r.db.QueryRowContext(ctx, SelectOrderByIDSQL, id))
	if err != nil {
		return Order{}, wrapLookup(err, fmt.Sprintf("order %d", id))
	}

	items, err := r.itemsForOrders(ctx, []int64{id})
	if err != nil {
		return Order{}, err
	}

	order.Items = items[id]

	return order, nil
}

// ListByCustomer loads a page of orders and then all of their items in one
// extra query - two round trips regardless of page size.
func (r *SQLOrderRepository) ListByCustomer(ctx context.Context, customerID int64, limit, offset int) ([]Order, error) {
	rows, err := r.db.QueryContext(ctx, SelectOrdersByCustomerSQL, customerID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list orders of customer %d: %w", customerID, err)
	}

	defer closeRows(rows)

	orders := make([]Order, 0, limit)
	ids := make([]int64, 0, limit)

	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("list orders of customer %d: %w", customerID, err)
		}

		orders = append(orders, order)
		ids = append(ids, order.ID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list orders of customer %d: %w", customerID, err)
	}

	if len(orders) == 0 {
		return orders, nil
	}

	items, err := r.itemsForOrders(ctx, ids)
	if err != nil {
		return nil, err
	}

	for i := range orders {
		orders[i].Items = items[orders[i].ID]
	}

	return orders, nil
}

func (r *SQLOrderRepository) UpdateStatus(ctx context.Context, id int64, status OrderStatus) error {
	switch status {
	case StatusPlaced, StatusShipped, StatusCancelled:
	default:
		return fmt.Errorf("update order %d: %w: unknown status %q", id, ErrValidation, status)
	}

	return execExpectingRow(ctx, r.db, UpdateOrderStatusSQL,
		fmt.Sprintf("order %d", id), string(status), id)
}

// itemsForOrders is the batched child load shared by ByID and ListByCustomer:
// one query for any number of orders, grouped in Go.
func (r *SQLOrderRepository) itemsForOrders(ctx context.Context, orderIDs []int64) (map[int64][]OrderItem, error) {
	if len(orderIDs) == 0 {
		return map[int64][]OrderItem{}, nil
	}

	args := make([]any, 0, len(orderIDs))

	for _, id := range orderIDs {
		args = append(args, id)
	}

	// Only the placeholder count is interpolated; the ids stay parameters.
	statement := fmt.Sprintf(SelectOrderItemsByOrderIDsSQL, placeholders(len(args)))

	rows, err := r.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("load order items: %w", err)
	}

	defer closeRows(rows)

	items := make(map[int64][]OrderItem, len(orderIDs))

	for rows.Next() {
		var item OrderItem

		if err := rows.Scan(&item.ID, &item.OrderID, &item.ProductID, &item.Quantity, &item.UnitCents); err != nil {
			return nil, fmt.Errorf("scan order item: %w", err)
		}

		items[item.OrderID] = append(items[item.OrderID], item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load order items: %w", err)
	}

	return items, nil
}

//
// SHARED HELPERS
//

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCustomer(row rowScanner) (Customer, error) {
	var (
		customer  Customer
		createdAt string
	)

	if err := row.Scan(&customer.ID, &customer.Email, &customer.Name, &createdAt); err != nil {
		return Customer{}, err
	}

	var err error

	if customer.CreatedAt, err = parseStamp(createdAt); err != nil {
		return Customer{}, err
	}

	return customer, nil
}

func scanProduct(row rowScanner) (Product, error) {
	var (
		product   Product
		createdAt string
	)

	if err := row.Scan(&product.ID, &product.SKU, &product.Name,
		&product.PriceCents, &product.Stock, &createdAt); err != nil {
		return Product{}, err
	}

	var err error

	if product.CreatedAt, err = parseStamp(createdAt); err != nil {
		return Product{}, err
	}

	return product, nil
}

func scanOrder(row rowScanner) (Order, error) {
	var (
		order     Order
		status    string
		createdAt string
	)

	if err := row.Scan(&order.ID, &order.CustomerID, &status, &order.TotalCents, &createdAt); err != nil {
		return Order{}, err
	}

	order.Status = OrderStatus(status)

	var err error

	if order.CreatedAt, err = parseStamp(createdAt); err != nil {
		return Order{}, err
	}

	return order, nil
}

// wrapLookup is the single place where sql.ErrNoRows becomes ErrNotFound.
func wrapLookup(err error, subject string) error {
	switch {
	case err == nil:
		return nil

	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%s: %w", subject, ErrNotFound)

	default:
		return fmt.Errorf("lookup %s: %w", subject, err)
	}
}

// execExpectingRow runs a statement that must affect exactly one row, and
// reports ErrNotFound when it affects none.
func execExpectingRow(ctx context.Context, db dbtx, query, subject string, args ...any) error {
	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update %s: %w", subject, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update %s: rows affected: %w", subject, err)
	}

	if affected == 0 {
		return fmt.Errorf("%s: %w", subject, ErrNotFound)
	}

	return nil
}

func placeholders(n int) string {
	if n <= 0 {
		return "NULL"
	}

	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func parseStamp(value string) (time.Time, error) {
	stamp, err := time.Parse(time.DateTime, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse timestamp %q: %w", value, err)
	}

	return stamp.UTC(), nil
}

func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		log.Printf("close rows: %v", err)
	}
}

// isUniqueViolation and isForeignKeyViolation isolate the one genuinely
// driver-specific piece of this layer. Swapping to pgx means changing these
// two functions to inspect *pgconn.PgError codes 23505 and 23503.
func isUniqueViolation(err error) bool {
	return strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT FAILED")
}

func isForeignKeyViolation(err error) bool {
	return strings.Contains(strings.ToUpper(err.Error()), "FOREIGN KEY CONSTRAINT FAILED")
}
