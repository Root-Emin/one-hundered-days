-- 0003_add_notes_archived.down.sql

DROP INDEX IF EXISTS idx_notes_active;
ALTER TABLE notes DROP COLUMN archived;
