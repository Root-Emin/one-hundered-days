-- 0003_add_notes_archived.up.sql
-- Additive change: a new column with a default, so existing rows stay valid
-- and older application versions keep working during a rolling deploy.

ALTER TABLE notes ADD COLUMN archived INTEGER NOT NULL DEFAULT 0;

-- Partial index: only active notes are listed on the hot path, so only they
-- need to be indexed.
CREATE INDEX idx_notes_active ON notes (user_id, created_at DESC) WHERE archived = 0;
