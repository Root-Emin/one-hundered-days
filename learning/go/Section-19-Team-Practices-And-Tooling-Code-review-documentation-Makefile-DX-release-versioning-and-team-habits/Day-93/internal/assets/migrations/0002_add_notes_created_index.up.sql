-- Listing is ordered by id, but the archive view filters by date; without this
-- index that query scans the whole table.
CREATE INDEX IF NOT EXISTS idx_notes_created_at ON notes (created_at);
