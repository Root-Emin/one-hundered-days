-- 0001_init.down.sql

DROP INDEX IF EXISTS idx_order_items_order;
DROP TABLE IF EXISTS order_items;
DROP INDEX IF EXISTS idx_orders_customer;
DROP TABLE IF EXISTS orders;
DROP INDEX IF EXISTS idx_products_sku;
DROP TABLE IF EXISTS products;
DROP INDEX IF EXISTS idx_customers_email;
DROP TABLE IF EXISTS customers;
