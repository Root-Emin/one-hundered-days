package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

/*
Day 45 - Databases (I): Practice Project & Review

Tasks covered:

 1. MVP data persisted in SQL instead of a map in memory
 2. Migrations checked into the repo (./migrations) and applied by the binary
 3. A transaction where the domain needs one (create/delete note also keeps
    users.note_count correct)
 4. An integration smoke test over every CRUD path (see main_test.go)

Run:

	go run . migrate up          # apply migrations
	go run . migrate down        # revert the newest migration
	go run . migrate status      # show applied / pending
	go run . serve               # start the API (migrates on boot by default)

Environment variables:

	PORT               HTTP port.                         Default: 8080
	DB_PATH            SQLite file path.                  Default: ./data/day45.db
	AUTO_MIGRATE       Run migrations on boot (true/false). Default: true
	SHUTDOWN_TIMEOUT   Graceful shutdown budget.          Default: 10s

Smoke test by hand:

	go run . serve &
	curl -s -XPOST localhost:8080/users -d '{"email":"ada@example.com"}'
	curl -s -XPOST localhost:8080/notes -d '{"user_id":1,"title":"first","body":"hello"}'
	curl -s localhost:8080/notes?user_id=1
	curl -s localhost:8080/notes/1
	curl -s -XPUT localhost:8080/notes/1 -d '{"title":"edited","body":"changed"}'
	curl -s -XPOST localhost:8080/notes/1/archive
	curl -s -XDELETE localhost:8080/notes/1
	curl -s localhost:8080/healthz

Or automated:

	go test ./...

The API survives restarts: stop the process, start it again, the notes are
still there. That is the whole point of today.
*/

//go:embed migrations/*.sql
var migrationFS embed.FS

//
// CONFIG
//

type Config struct {
	Port            string
	DBPath          string
	AutoMigrate     bool
	ShutdownTimeout time.Duration
}

func loadConfig() Config {
	return Config{
		Port:            envString("PORT", "8080"),
		DBPath:          envString("DB_PATH", "data/day45.db"),
		AutoMigrate:     envBool("AUTO_MIGRATE", true),
		ShutdownTimeout: envDuration("SHUTDOWN_TIMEOUT", 10*time.Second),
	}
}

func envString(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))

	if value == "" {
		return fallback
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		log.Printf("invalid %s=%q, using %t", key, value, fallback)
		return fallback
	}

	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))

	if value == "" {
		return fallback
	}

	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		log.Printf("invalid %s=%q, using %s", key, value, fallback)
		return fallback
	}

	return parsed
}

//
// DOMAIN
//

var (
	ErrNotFound    = errors.New("not found")
	ErrValidation  = errors.New("validation failed")
	ErrEmailExists = errors.New("email already registered")
)

type User struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	NoteCount int       `json:"note_count"`
	CreatedAt time.Time `json:"created_at"`
}

type Note struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Archived  bool      `json:"archived"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

//
// REPOSITORY
//
// Handlers never see SQL. Repository functions stay small enough to read in
// one screen and to test directly against a real database.
//

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) CreateUser(ctx context.Context, email string) (User, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	if email == "" || !strings.Contains(email, "@") {
		return User{}, fmt.Errorf("%w: email must look like an address", ErrValidation)
	}

	result, err := r.db.ExecContext(ctx, `INSERT INTO users (email) VALUES (?);`, email)
	if err != nil {
		if strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT FAILED") {
			return User{}, fmt.Errorf("create user %q: %w", email, ErrEmailExists)
		}

		return User{}, fmt.Errorf("create user %q: %w", email, err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("create user %q: read id: %w", email, err)
	}

	return r.UserByID(ctx, id)
}

func (r *Repository) UserByID(ctx context.Context, id int64) (User, error) {
	const query = `SELECT id, email, note_count, created_at FROM users WHERE id = ?;`

	var (
		user      User
		createdAt string
	)

	err := r.db.QueryRowContext(ctx, query, id).
		Scan(&user.ID, &user.Email, &user.NoteCount, &createdAt)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return User{}, fmt.Errorf("user %d: %w", id, ErrNotFound)
	case err != nil:
		return User{}, fmt.Errorf("select user %d: %w", id, err)
	}

	user.CreatedAt, err = parseStamp(createdAt)
	if err != nil {
		return User{}, err
	}

	return user, nil
}

// CreateNote inserts the note and bumps the owner's counter in one unit of
// work: a note that exists without being counted is a bug support cannot
// explain, and the two writes must therefore commit together.
func (r *Repository) CreateNote(ctx context.Context, note Note) (Note, error) {
	note.Title = strings.TrimSpace(note.Title)
	note.Body = strings.TrimSpace(note.Body)

	switch {
	case note.UserID <= 0:
		return Note{}, fmt.Errorf("%w: user_id is required", ErrValidation)
	case note.Title == "":
		return Note{}, fmt.Errorf("%w: title is required", ErrValidation)
	case len(note.Title) > 200:
		return Note{}, fmt.Errorf("%w: title must be at most 200 characters", ErrValidation)
	case len(note.Body) > 10_000:
		return Note{}, fmt.Errorf("%w: body must be at most 10000 characters", ErrValidation)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Note{}, fmt.Errorf("create note: begin: %w", err)
	}

	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			log.Printf("create note: rollback: %v", err)
		}
	}()

	result, err := tx.ExecContext(
		ctx,
		`INSERT INTO notes (user_id, title, body) VALUES (?, ?, ?);`,
		note.UserID, note.Title, note.Body,
	)
	if err != nil {
		if strings.Contains(strings.ToUpper(err.Error()), "FOREIGN KEY CONSTRAINT FAILED") {
			return Note{}, fmt.Errorf("create note: user %d: %w", note.UserID, ErrNotFound)
		}

		return Note{}, fmt.Errorf("create note: insert: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Note{}, fmt.Errorf("create note: read id: %w", err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE users SET note_count = note_count + 1 WHERE id = ?;`,
		note.UserID,
	); err != nil {
		return Note{}, fmt.Errorf("create note: bump counter: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Note{}, fmt.Errorf("create note: commit: %w", err)
	}

	return r.NoteByID(ctx, id)
}

func (r *Repository) NoteByID(ctx context.Context, id int64) (Note, error) {
	const query = `
		SELECT id, user_id, title, body, archived, created_at, updated_at
		FROM notes
		WHERE id = ?;`

	note, err := scanNote(r.db.QueryRowContext(ctx, query, id))

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Note{}, fmt.Errorf("note %d: %w", id, ErrNotFound)
	case err != nil:
		return Note{}, fmt.Errorf("select note %d: %w", id, err)
	}

	return note, nil
}

// ListNotes is paginated from the start: an unbounded list query is a denial
// of service waiting for the table to grow.
func (r *Repository) ListNotes(ctx context.Context, userID int64, includeArchived bool, limit, offset int) ([]Note, error) {
	const query = `
		SELECT id, user_id, title, body, archived, created_at, updated_at
		FROM notes
		WHERE user_id = ? AND (archived = 0 OR ?)
		ORDER BY created_at DESC, id DESC
		LIMIT ? OFFSET ?;`

	rows, err := r.db.QueryContext(ctx, query, userID, includeArchived, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list notes for user %d: %w", userID, err)
	}

	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("list notes: close rows: %v", err)
		}
	}()

	notes := make([]Note, 0, limit)

	for rows.Next() {
		note, err := scanNote(rows)
		if err != nil {
			return nil, fmt.Errorf("list notes for user %d: %w", userID, err)
		}

		notes = append(notes, note)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list notes for user %d: %w", userID, err)
	}

	return notes, nil
}

func (r *Repository) UpdateNote(ctx context.Context, id int64, title, body string) (Note, error) {
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)

	if title == "" {
		return Note{}, fmt.Errorf("%w: title is required", ErrValidation)
	}

	result, err := r.db.ExecContext(
		ctx,
		`UPDATE notes SET title = ?, body = ?, updated_at = datetime('now') WHERE id = ?;`,
		title, body, id,
	)
	if err != nil {
		return Note{}, fmt.Errorf("update note %d: %w", id, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return Note{}, fmt.Errorf("update note %d: rows affected: %w", id, err)
	}

	if affected == 0 {
		return Note{}, fmt.Errorf("note %d: %w", id, ErrNotFound)
	}

	return r.NoteByID(ctx, id)
}

func (r *Repository) ArchiveNote(ctx context.Context, id int64) (Note, error) {
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE notes SET archived = 1, updated_at = datetime('now') WHERE id = ? AND archived = 0;`,
		id,
	)
	if err != nil {
		return Note{}, fmt.Errorf("archive note %d: %w", id, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return Note{}, fmt.Errorf("archive note %d: rows affected: %w", id, err)
	}

	if affected == 0 {
		// Either it does not exist or it is already archived. NoteByID tells
		// the two apart, and archiving twice stays idempotent.
		return r.NoteByID(ctx, id)
	}

	return r.NoteByID(ctx, id)
}

// DeleteNote removes the note and decrements the counter atomically, the
// mirror image of CreateNote.
func (r *Repository) DeleteNote(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete note %d: begin: %w", id, err)
	}

	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			log.Printf("delete note %d: rollback: %v", id, err)
		}
	}()

	var userID int64

	err = tx.QueryRowContext(ctx, `SELECT user_id FROM notes WHERE id = ?;`, id).Scan(&userID)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("note %d: %w", id, ErrNotFound)
	case err != nil:
		return fmt.Errorf("delete note %d: lookup owner: %w", id, err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM notes WHERE id = ?;`, id); err != nil {
		return fmt.Errorf("delete note %d: %w", id, err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE users SET note_count = note_count - 1 WHERE id = ? AND note_count > 0;`,
		userID,
	); err != nil {
		return fmt.Errorf("delete note %d: decrement counter: %w", id, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete note %d: commit: %w", id, err)
	}

	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanNote(row rowScanner) (Note, error) {
	var (
		note                 Note
		createdAt, updatedAt string
	)

	if err := row.Scan(
		&note.ID,
		&note.UserID,
		&note.Title,
		&note.Body,
		&note.Archived,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Note{}, err
	}

	var err error

	if note.CreatedAt, err = parseStamp(createdAt); err != nil {
		return Note{}, err
	}

	if note.UpdatedAt, err = parseStamp(updatedAt); err != nil {
		return Note{}, err
	}

	return note, nil
}

func parseStamp(value string) (time.Time, error) {
	stamp, err := time.Parse(time.DateTime, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse timestamp %q: %w", value, err)
	}

	return stamp.UTC(), nil
}

//
// MIGRATIONS
//

type migration struct {
	version int64
	name    string
	up      string
	down    string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	byVersion := make(map[int64]*migration)

	for _, entry := range entries {
		name := entry.Name()

		direction := "up"
		if strings.HasSuffix(name, ".down.sql") {
			direction = "down"
		}

		base := strings.TrimSuffix(name, "."+direction+".sql")

		versionText, label, found := strings.Cut(base, "_")
		if !found {
			return nil, fmt.Errorf("migration %q must be <version>_<name>.<up|down>.sql", name)
		}

		version, err := strconv.ParseInt(versionText, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("migration %q: %w", name, err)
		}

		body, err := fs.ReadFile(migrationFS, "migrations/"+name)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", name, err)
		}

		entryMigration, ok := byVersion[version]
		if !ok {
			entryMigration = &migration{version: version, name: label}
			byVersion[version] = entryMigration
		}

		if direction == "up" {
			entryMigration.up = string(body)
		} else {
			entryMigration.down = string(body)
		}
	}

	migrations := make([]migration, 0, len(byVersion))

	for _, value := range byVersion {
		if strings.TrimSpace(value.up) == "" || strings.TrimSpace(value.down) == "" {
			return nil, fmt.Errorf("migration %04d_%s is missing an up or down half", value.version, value.name)
		}

		migrations = append(migrations, *value)
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	return migrations, nil
}

func migrateUp(ctx context.Context, db *sql.DB) error {
	migrations, applied, err := migrationState(ctx, db)
	if err != nil {
		return err
	}

	for _, item := range migrations {
		if applied[item.version] {
			continue
		}

		log.Printf("migrate up %04d_%s", item.version, item.name)

		if err := runMigration(ctx, db, item, true); err != nil {
			return err
		}
	}

	return nil
}

func migrateDown(ctx context.Context, db *sql.DB) error {
	migrations, applied, err := migrationState(ctx, db)
	if err != nil {
		return err
	}

	for i := len(migrations) - 1; i >= 0; i-- {
		if !applied[migrations[i].version] {
			continue
		}

		log.Printf("migrate down %04d_%s", migrations[i].version, migrations[i].name)

		return runMigration(ctx, db, migrations[i], false)
	}

	log.Printf("nothing to revert")

	return nil
}

func migrateStatus(ctx context.Context, db *sql.DB) error {
	migrations, applied, err := migrationState(ctx, db)
	if err != nil {
		return err
	}

	fmt.Printf("\n%-9s %-16s %s\n", "VERSION", "NAME", "STATE")
	fmt.Println(strings.Repeat("-", 40))

	for _, item := range migrations {
		state := "pending"
		if applied[item.version] {
			state = "applied"
		}

		fmt.Printf("%-9d %-16s %s\n", item.version, item.name, state)
	}

	fmt.Println()

	return nil
}

func migrationState(ctx context.Context, db *sql.DB) ([]migration, map[int64]bool, error) {
	migrations, err := loadMigrations()
	if err != nil {
		return nil, nil, err
	}

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		);`); err != nil {
		return nil, nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations;`)
	if err != nil {
		return nil, nil, fmt.Errorf("read schema_migrations: %w", err)
	}

	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("close rows: %v", err)
		}
	}()

	applied := make(map[int64]bool)

	for rows.Next() {
		var version int64

		if err := rows.Scan(&version); err != nil {
			return nil, nil, fmt.Errorf("scan applied version: %w", err)
		}

		applied[version] = true
	}

	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate applied versions: %w", err)
	}

	return migrations, applied, nil
}

func runMigration(ctx context.Context, db *sql.DB, item migration, up bool) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migration %04d: begin: %w", item.version, err)
	}

	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			log.Printf("migration %04d: rollback: %v", item.version, err)
		}
	}()

	body := item.up
	if !up {
		body = item.down
	}

	for _, statement := range splitStatements(body) {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migration %04d: %w", item.version, err)
		}
	}

	if up {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, name) VALUES (?, ?);`, item.version, item.name)
	} else {
		_, err = tx.ExecContext(ctx,
			`DELETE FROM schema_migrations WHERE version = ?;`, item.version)
	}

	if err != nil {
		return fmt.Errorf("migration %04d: record version: %w", item.version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration %04d: commit: %w", item.version, err)
	}

	return nil
}

// splitStatements breaks a file into individual statements, because
// database/sql drivers execute one statement per call.
//
// Whole-line "--" comments are stripped first: a semicolon inside a comment
// (";" in a sentence, for example) would otherwise split a statement in half.
func splitStatements(body string) []string {
	var stripped strings.Builder

	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}

		stripped.WriteString(line)
		stripped.WriteString("\n")
	}

	parts := strings.Split(stripped.String(), ";")
	statements := make([]string, 0, len(parts))

	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}

		statements = append(statements, strings.TrimSpace(part)+";")
	}

	return statements
}

//
// HTTP API
//

type API struct {
	repo *Repository
}

func (a *API) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("POST /users", a.createUser)
	mux.HandleFunc("GET /users/{id}", a.getUser)
	mux.HandleFunc("GET /notes", a.listNotes)
	mux.HandleFunc("POST /notes", a.createNote)
	mux.HandleFunc("GET /notes/{id}", a.getNote)
	mux.HandleFunc("PUT /notes/{id}", a.updateNote)
	mux.HandleFunc("POST /notes/{id}/archive", a.archiveNote)
	mux.HandleFunc("DELETE /notes/{id}", a.deleteNote)

	return loggingMiddleware(recoveryMiddleware(mux))
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	// A health check that does not touch the database is a health check that
	// lies during an outage.
	if err := a.repo.db.PingContext(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unreachable")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email string `json:"email"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	user, err := a.repo.CreateUser(r.Context(), input.Email)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, user)
}

func (a *API) getUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	user, err := a.repo.UserByID(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, user)
}

func (a *API) createNote(w http.ResponseWriter, r *http.Request) {
	var input struct {
		UserID int64  `json:"user_id"`
		Title  string `json:"title"`
		Body   string `json:"body"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	note, err := a.repo.CreateNote(r.Context(), Note{
		UserID: input.UserID,
		Title:  input.Title,
		Body:   input.Body,
	})
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, note)
}

func (a *API) listNotes(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		writeError(w, http.StatusBadRequest, "user_id query parameter is required")
		return
	}

	limit := queryInt(r, "limit", 20)
	if limit < 1 || limit > 100 {
		limit = 20
	}

	offset := queryInt(r, "offset", 0)
	if offset < 0 {
		offset = 0
	}

	includeArchived := r.URL.Query().Get("archived") == "true"

	notes, err := a.repo.ListNotes(r.Context(), userID, includeArchived, limit, offset)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"notes":  notes,
		"count":  len(notes),
		"limit":  limit,
		"offset": offset,
	})
}

func (a *API) getNote(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	note, err := a.repo.NoteByID(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, note)
}

func (a *API) updateNote(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var input struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	note, err := a.repo.UpdateNote(r.Context(), id, input.Title, input.Body)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, note)
}

func (a *API) archiveNote(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	note, err := a.repo.ArchiveNote(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, note)
}

func (a *API) deleteNote(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if err := a.repo.DeleteNote(r.Context(), id); err != nil {
		respondError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

//
// HTTP HELPERS
//

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}

	return id, true
}

func queryInt(r *http.Request, key string, fallback int) int {
	value := r.URL.Query().Get(key)

	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return parsed
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	defer func() {
		if err := r.Body.Close(); err != nil {
			log.Printf("close request body: %v", err)
		}
	}()

	// Cap the body: an unbounded decode is free memory for an attacker.
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}

	return true
}

// respondError maps domain errors to status codes in one place, and never
// echoes a raw database error to the client.
func respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")

	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "validation failed: "))

	case errors.Is(err, ErrEmailExists):
		writeError(w, http.StatusConflict, "email already registered")

	default:
		log.Printf("internal error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("encode response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(recorder, r)

		log.Printf("method=%s path=%s status=%d duration=%s",
			r.Method, r.URL.Path, recorder.status, time.Since(start).Round(time.Microsecond))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("panic: %v", recovered)
				writeError(w, http.StatusInternalServerError, "internal server error")
			}
		}()

		next.ServeHTTP(w, r)
	})
}

//
// WIRING
//

func openDB(ctx context.Context, path string) (*sql.DB, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	dsn := path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close database: %v", closeErr)
		}

		return nil, fmt.Errorf("ping database: %w", err)
	}

	return db, nil
}

func serve(ctx context.Context, config Config, db *sql.DB) error {
	api := &API{repo: NewRepository(db)}

	server := &http.Server{
		Addr:              ":" + config.Port,
		Handler:           api.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)

	go func() {
		log.Printf("listening on :%s db=%s", config.Port, config.DBPath)

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		return fmt.Errorf("serve: %w", err)

	case received := <-shutdown:
		log.Printf("shutdown signal: %s", received)
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, config.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		if closeErr := server.Close(); closeErr != nil {
			log.Printf("force close: %v", closeErr)
		}

		return fmt.Errorf("graceful shutdown: %w", err)
	}

	log.Printf("stopped cleanly")

	return nil
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: go run . <serve|migrate up|migrate down|migrate status>")
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day45: ")

	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	config := loadConfig()
	ctx := context.Background()

	db, err := openDB(ctx, config.DBPath)
	if err != nil {
		log.Fatalf("database unavailable: %v", err)
	}

	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	}()

	switch command {
	case "migrate":
		sub := "up"
		if len(os.Args) > 2 {
			sub = os.Args[2]
		}

		switch sub {
		case "up":
			err = migrateUp(ctx, db)
		case "down":
			err = migrateDown(ctx, db)
		case "status":
			err = migrateStatus(ctx, db)
		default:
			usage()
			os.Exit(2)
		}

	case "serve":
		if config.AutoMigrate {
			if err := migrateUp(ctx, db); err != nil {
				log.Fatalf("migrate: %v", err)
			}
		}

		err = serve(ctx, config, db)

	default:
		usage()
		os.Exit(2)
	}

	if err != nil {
		log.Fatalf("%s: %v", command, err)
	}
}
