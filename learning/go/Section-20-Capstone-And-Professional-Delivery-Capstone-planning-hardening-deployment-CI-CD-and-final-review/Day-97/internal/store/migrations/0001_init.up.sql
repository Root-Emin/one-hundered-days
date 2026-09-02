-- The initial schema.
--
-- links.code is the primary key: it is what every read looks up by, and making
-- it the key means the hot path is a single index seek with no secondary
-- lookup.
CREATE TABLE IF NOT EXISTS links (
	code       TEXT    PRIMARY KEY,
	owner      TEXT    NOT NULL,
	target     TEXT    NOT NULL,
	active     INTEGER NOT NULL DEFAULT 1,
	expires_at TEXT,
	created_at TEXT    NOT NULL
);

-- Listing is always scoped to one owner, newest first.
CREATE INDEX IF NOT EXISTS idx_links_owner ON links (owner, created_at DESC);

-- Keys are stored as a SHA-256 hash: a leaked database gives an attacker
-- hashes, not working credentials.
CREATE TABLE IF NOT EXISTS api_keys (
	id           TEXT PRIMARY KEY,
	owner        TEXT NOT NULL,
	hash         TEXT NOT NULL UNIQUE,
	created_at   TEXT NOT NULL,
	last_used_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys (hash);

-- Raw clicks. The stats endpoint never reads this table; the worker aggregates
-- it into click_daily.
CREATE TABLE IF NOT EXISTS clicks (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	code        TEXT NOT NULL,
	occurred_at TEXT NOT NULL,
	referrer    TEXT NOT NULL DEFAULT '',
	user_agent  TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_clicks_code ON clicks (code, occurred_at);

-- The aggregate the stats endpoint reads: one row per code per UTC day.
CREATE TABLE IF NOT EXISTS click_daily (
	code  TEXT    NOT NULL,
	day   TEXT    NOT NULL,
	count INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (code, day)
);

-- The outbox: a click event is written here in the request, and the worker
-- turns it into an aggregate later.
CREATE TABLE IF NOT EXISTS outbox (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	event_id     TEXT    NOT NULL UNIQUE,
	event_type   TEXT    NOT NULL,
	payload      TEXT    NOT NULL,
	created_at   TEXT    NOT NULL,
	published_at TEXT,
	attempts     INTEGER NOT NULL DEFAULT 0
);

-- A partial index, so the relay's poll is a seek rather than a scan of every
-- event ever published.
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished ON outbox (id) WHERE published_at IS NULL;
