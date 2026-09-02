-- 0001_create_users.up.sql
-- Users are the owner of every other row in the MVP schema.

CREATE TABLE users (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	email      TEXT    NOT NULL,
	full_name  TEXT    NOT NULL,
	created_at TEXT    NOT NULL DEFAULT (datetime('now')),
	updated_at TEXT    NOT NULL DEFAULT (datetime('now'))
);

-- Email is the natural login key: uniqueness is a data rule, not an
-- application rule, so the database enforces it. The unique index also
-- serves the "find user by email" lookup on every sign-in.
CREATE UNIQUE INDEX idx_users_email ON users (email);
