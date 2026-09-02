-- 0002_create_notes.up.sql
-- Notes belong to a user and are always listed newest-first per user.

CREATE TABLE notes (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	title      TEXT    NOT NULL,
	body       TEXT    NOT NULL DEFAULT '',
	created_at TEXT    NOT NULL DEFAULT (datetime('now')),
	updated_at TEXT    NOT NULL DEFAULT (datetime('now'))
);

-- The composite index matches the only list query we already know we need:
--   WHERE user_id = ? ORDER BY created_at DESC
-- Column order matters: equality column first, sort column second.
CREATE INDEX idx_notes_user_created ON notes (user_id, created_at DESC);
