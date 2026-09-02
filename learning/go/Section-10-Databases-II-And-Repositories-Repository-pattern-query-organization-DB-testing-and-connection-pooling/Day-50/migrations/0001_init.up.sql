-- 0001_init.up.sql - the shop schema.
--
-- Design notes:
--   * money is stored in integer minor units, never floating point
--   * every table carries created_at for auditing and stable ordering
--   * indexes are created for the queries in queries.go, not speculatively

CREATE TABLE customers (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	email      TEXT NOT NULL,
	name       TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX idx_customers_email ON customers (email);

CREATE TABLE products (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	sku         TEXT    NOT NULL,
	name        TEXT    NOT NULL,
	price_cents INTEGER NOT NULL CHECK (price_cents > 0),
	stock       INTEGER NOT NULL DEFAULT 0 CHECK (stock >= 0),
	created_at  TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX idx_products_sku ON products (sku);

CREATE TABLE orders (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	customer_id INTEGER NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
	status      TEXT    NOT NULL DEFAULT 'placed',
	total_cents INTEGER NOT NULL CHECK (total_cents >= 0),
	created_at  TEXT    NOT NULL DEFAULT (datetime('now'))
);

-- Matches "orders of this customer, newest first", the only list query we ship.
CREATE INDEX idx_orders_customer ON orders (customer_id, created_at DESC);

CREATE TABLE order_items (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	order_id    INTEGER NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
	product_id  INTEGER NOT NULL REFERENCES products (id),
	quantity    INTEGER NOT NULL CHECK (quantity > 0),
	unit_cents  INTEGER NOT NULL CHECK (unit_cents > 0)
);

-- Supports the batched item load that keeps order listing out of N+1.
CREATE INDEX idx_order_items_order ON order_items (order_id);
