package main

/*
Every SQL statement in the service, grouped by aggregate and named after the
operation it performs. Reviewing the data access surface means reading this
file - nothing else in the program contains SQL text.

Naming: <Verb><Entity><Qualifier>SQL, matching the repository method that
uses it, so a reader can jump between the two without searching.
*/

//
// CUSTOMERS
//

const (
	InsertCustomerSQL = `
		INSERT INTO customers (email, name)
		VALUES (?, ?);`

	SelectCustomerByIDSQL = `
		SELECT id, email, name, created_at
		FROM customers
		WHERE id = ?;`

	SelectCustomerByEmailSQL = `
		SELECT id, email, name, created_at
		FROM customers
		WHERE email = ?;`

	SelectCustomersSQL = `
		SELECT id, email, name, created_at
		FROM customers
		ORDER BY created_at DESC, id DESC
		LIMIT ? OFFSET ?;`

	DeleteCustomerSQL = `
		DELETE FROM customers
		WHERE id = ?;`
)

//
// PRODUCTS
//

const (
	InsertProductSQL = `
		INSERT INTO products (sku, name, price_cents, stock)
		VALUES (?, ?, ?, ?);`

	SelectProductByIDSQL = `
		SELECT id, sku, name, price_cents, stock, created_at
		FROM products
		WHERE id = ?;`

	SelectProductBySKUSQL = `
		SELECT id, sku, name, price_cents, stock, created_at
		FROM products
		WHERE sku = ?;`

	SelectProductsSQL = `
		SELECT id, sku, name, price_cents, stock, created_at
		FROM products
		ORDER BY sku
		LIMIT ? OFFSET ?;`

	// The stock check lives in the WHERE clause so that two concurrent orders
	// cannot both pass it. Zero rows affected means "not enough stock".
	ReserveProductStockSQL = `
		UPDATE products
		SET stock = stock - ?
		WHERE id = ? AND stock >= ?;`

	ReleaseProductStockSQL = `
		UPDATE products
		SET stock = stock + ?
		WHERE id = ?;`
)

//
// ORDERS
//

const (
	InsertOrderSQL = `
		INSERT INTO orders (customer_id, status, total_cents)
		VALUES (?, ?, ?);`

	InsertOrderItemSQL = `
		INSERT INTO order_items (order_id, product_id, quantity, unit_cents)
		VALUES (?, ?, ?, ?);`

	SelectOrderByIDSQL = `
		SELECT id, customer_id, status, total_cents, created_at
		FROM orders
		WHERE id = ?;`

	SelectOrdersByCustomerSQL = `
		SELECT id, customer_id, status, total_cents, created_at
		FROM orders
		WHERE customer_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ? OFFSET ?;`

	SelectOrderItemsByOrderIDSQL = `
		SELECT id, order_id, product_id, quantity, unit_cents
		FROM order_items
		WHERE order_id = ?
		ORDER BY id;`

	// Batched sibling of the query above: one round trip for a whole page of
	// orders instead of one per order. %s is filled with placeholders only.
	SelectOrderItemsByOrderIDsSQL = `
		SELECT id, order_id, product_id, quantity, unit_cents
		FROM order_items
		WHERE order_id IN (%s)
		ORDER BY order_id, id;`

	UpdateOrderStatusSQL = `
		UPDATE orders
		SET status = ?
		WHERE id = ?;`
)
