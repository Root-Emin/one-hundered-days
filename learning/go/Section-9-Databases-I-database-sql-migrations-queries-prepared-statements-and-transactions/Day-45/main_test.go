package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

/*
Integration smoke test for the Day 45 MVP.

It runs against a real SQLite database created in t.TempDir(), applies the
same migrations the binary ships with, and drives every CRUD path through the
HTTP layer - no mocks, no in-memory shortcut store. If the SQL is wrong, this
test fails; that is the point of an integration test.

	go test ./...
	go test -run TestNoteCRUD -v ./...
*/

// newTestServer boots the whole stack (database + migrations + routes) and
// hands back a live HTTP server plus the repository for direct assertions.
func newTestServer(t *testing.T) (*httptest.Server, *Repository) {
	t.Helper()

	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "test.db")

	db, err := openDB(ctx, dbPath)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})

	if err := migrateUp(ctx, db); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	repo := NewRepository(db)
	server := httptest.NewServer((&API{repo: repo}).routes())

	t.Cleanup(server.Close)

	return server, repo
}

func request(t *testing.T, server *httptest.Server, method, path string, body any) (int, []byte) {
	t.Helper()

	var payload *bytes.Reader

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode request body: %v", err)
		}

		payload = bytes.NewReader(encoded)
	} else {
		payload = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, payload)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close response body: %v", err)
		}
	}()

	var buffer bytes.Buffer

	if _, err := buffer.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read response body: %v", err)
	}

	return resp.StatusCode, buffer.Bytes()
}

func decode[T any](t *testing.T, body []byte) T {
	t.Helper()

	var value T

	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("decode response %s: %v", body, err)
	}

	return value
}

func TestHealthz(t *testing.T) {
	server, _ := newTestServer(t)

	status, body := request(t, server, http.MethodGet, "/healthz", nil)

	if status != http.StatusOK {
		t.Fatalf("healthz status = %d, want %d (%s)", status, http.StatusOK, body)
	}
}

func TestNoteCRUD(t *testing.T) {
	server, repo := newTestServer(t)

	// Create the owner.
	status, body := request(t, server, http.MethodPost, "/users",
		map[string]string{"email": "ada@example.com"})

	if status != http.StatusCreated {
		t.Fatalf("create user status = %d, want %d (%s)", status, http.StatusCreated, body)
	}

	user := decode[User](t, body)

	if user.ID == 0 || user.Email != "ada@example.com" {
		t.Fatalf("unexpected user: %+v", user)
	}

	// Create a note.
	status, body = request(t, server, http.MethodPost, "/notes", map[string]any{
		"user_id": user.ID,
		"title":   "first note",
		"body":    "persisted in SQL",
	})

	if status != http.StatusCreated {
		t.Fatalf("create note status = %d, want %d (%s)", status, http.StatusCreated, body)
	}

	note := decode[Note](t, body)

	if note.ID == 0 || note.Title != "first note" || note.Archived {
		t.Fatalf("unexpected note: %+v", note)
	}

	// The counter must have moved inside the same transaction as the insert.
	refreshed, err := repo.UserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}

	if refreshed.NoteCount != 1 {
		t.Fatalf("note_count = %d, want 1", refreshed.NoteCount)
	}

	// Read it back.
	status, body = request(t, server, http.MethodGet, "/notes/"+itoa(note.ID), nil)

	if status != http.StatusOK {
		t.Fatalf("get note status = %d, want %d (%s)", status, http.StatusOK, body)
	}

	if got := decode[Note](t, body); got.ID != note.ID {
		t.Fatalf("get note returned id %d, want %d", got.ID, note.ID)
	}

	// Update it.
	status, body = request(t, server, http.MethodPut, "/notes/"+itoa(note.ID),
		map[string]string{"title": "edited", "body": "changed"})

	if status != http.StatusOK {
		t.Fatalf("update note status = %d, want %d (%s)", status, http.StatusOK, body)
	}

	updated := decode[Note](t, body)

	if updated.Title != "edited" || updated.Body != "changed" {
		t.Fatalf("update did not persist: %+v", updated)
	}

	// List it.
	status, body = request(t, server, http.MethodGet, "/notes?user_id="+itoa(user.ID), nil)

	if status != http.StatusOK {
		t.Fatalf("list notes status = %d, want %d (%s)", status, http.StatusOK, body)
	}

	list := decode[struct {
		Notes []Note `json:"notes"`
		Count int    `json:"count"`
	}](t, body)

	if list.Count != 1 || len(list.Notes) != 1 {
		t.Fatalf("list returned %d notes, want 1", list.Count)
	}

	// Archive it: it disappears from the default list but is still fetchable.
	status, body = request(t, server, http.MethodPost, "/notes/"+itoa(note.ID)+"/archive", nil)

	if status != http.StatusOK {
		t.Fatalf("archive status = %d, want %d (%s)", status, http.StatusOK, body)
	}

	if archived := decode[Note](t, body); !archived.Archived {
		t.Fatalf("note was not archived: %+v", archived)
	}

	_, body = request(t, server, http.MethodGet, "/notes?user_id="+itoa(user.ID), nil)

	if got := decode[struct {
		Count int `json:"count"`
	}](t, body); got.Count != 0 {
		t.Fatalf("archived note still listed: count = %d", got.Count)
	}

	_, body = request(t, server, http.MethodGet, "/notes?user_id="+itoa(user.ID)+"&archived=true", nil)

	if got := decode[struct {
		Count int `json:"count"`
	}](t, body); got.Count != 1 {
		t.Fatalf("archived=true count = %d, want 1", got.Count)
	}

	// Delete it.
	status, body = request(t, server, http.MethodDelete, "/notes/"+itoa(note.ID), nil)

	if status != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d (%s)", status, http.StatusNoContent, body)
	}

	status, _ = request(t, server, http.MethodGet, "/notes/"+itoa(note.ID), nil)

	if status != http.StatusNotFound {
		t.Fatalf("deleted note status = %d, want %d", status, http.StatusNotFound)
	}

	// And the counter came back down with it.
	refreshed, err = repo.UserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}

	if refreshed.NoteCount != 0 {
		t.Fatalf("note_count after delete = %d, want 0", refreshed.NoteCount)
	}
}

func TestValidationAndNotFound(t *testing.T) {
	server, _ := newTestServer(t)

	tests := []struct {
		name   string
		method string
		path   string
		body   any
		want   int
	}{
		{"empty title", http.MethodPost, "/notes", map[string]any{"user_id": 1, "title": "  "}, http.StatusBadRequest},
		{"unknown user", http.MethodPost, "/notes", map[string]any{"user_id": 999, "title": "x"}, http.StatusNotFound},
		{"bad email", http.MethodPost, "/users", map[string]string{"email": "nope"}, http.StatusBadRequest},
		{"missing note", http.MethodGet, "/notes/424242", nil, http.StatusNotFound},
		{"bad id", http.MethodGet, "/notes/abc", nil, http.StatusBadRequest},
		{"missing user_id", http.MethodGet, "/notes", nil, http.StatusBadRequest},
		{"unknown field", http.MethodPost, "/users", map[string]string{"e-mail": "a@b.c"}, http.StatusBadRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, body := request(t, server, test.method, test.path, test.body)

			if status != test.want {
				t.Fatalf("status = %d, want %d (%s)", status, test.want, body)
			}
		})
	}
}

func TestDuplicateEmailConflicts(t *testing.T) {
	server, _ := newTestServer(t)

	payload := map[string]string{"email": "grace@example.com"}

	if status, body := request(t, server, http.MethodPost, "/users", payload); status != http.StatusCreated {
		t.Fatalf("first create status = %d (%s)", status, body)
	}

	status, body := request(t, server, http.MethodPost, "/users", payload)

	if status != http.StatusConflict {
		t.Fatalf("duplicate email status = %d, want %d (%s)", status, http.StatusConflict, body)
	}
}

// TestDataSurvivesRestart is the promise of today's lesson: the process can
// die and the data is still there.
func TestDataSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "restart.db")

	// First "process".
	db, err := openDB(ctx, dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if err := migrateUp(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repo := NewRepository(db)

	user, err := repo.CreateUser(ctx, "restart@example.com")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if _, err := repo.CreateNote(ctx, Note{UserID: user.ID, Title: "survives"}); err != nil {
		t.Fatalf("create note: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Second "process", same file.
	reopened, err := openDB(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}

	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close reopened database: %v", err)
		}
	})

	notes, err := NewRepository(reopened).ListNotes(ctx, user.ID, false, 10, 0)
	if err != nil {
		t.Fatalf("list after restart: %v", err)
	}

	if len(notes) != 1 || notes[0].Title != "survives" {
		t.Fatalf("data did not survive restart: %+v", notes)
	}
}

func TestMigrationsAreReversible(t *testing.T) {
	ctx := context.Background()

	db, err := openDB(ctx, filepath.Join(t.TempDir(), "migrate.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	if err := migrateUp(ctx, db); err != nil {
		t.Fatalf("up: %v", err)
	}

	if _, err := db.ExecContext(ctx, `SELECT 1 FROM notes LIMIT 1;`); err != nil {
		t.Fatalf("notes table missing after up: %v", err)
	}

	if err := migrateDown(ctx, db); err != nil {
		t.Fatalf("down: %v", err)
	}

	if _, err := db.ExecContext(ctx, `SELECT 1 FROM notes LIMIT 1;`); err == nil {
		t.Fatal("notes table still exists after down")
	}

	// Re-applying must work: migrations are not one-shot.
	if err := migrateUp(ctx, db); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

func itoa(id int64) string {
	if id == 0 {
		return "0"
	}

	digits := ""

	for id > 0 {
		digits = string(rune('0'+id%10)) + digits
		id /= 10
	}

	return digits
}
