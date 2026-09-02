-- 0001_init.up.sql
-- MVP schema: a user owns notes; note_count is maintained transactionally.

CREATE TABLE users (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	email      TEXT    NOT NULL,
	note_count INTEGER NOT NULL DEFAULT 0 CHECK (note_count >= 0),
	created_at TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX idx_users_email ON users (email);

CREATE TABLE notes (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	title      TEXT    NOT NULL,
	body       TEXT    NOT NULL DEFAULT '',
	archived   INTEGER NOT NULL DEFAULT 0,
	created_at TEXT    NOT NULL DEFAULT (datetime('now')),
	updated_at TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_notes_user_created ON notes (user_id, created_at DESC);
