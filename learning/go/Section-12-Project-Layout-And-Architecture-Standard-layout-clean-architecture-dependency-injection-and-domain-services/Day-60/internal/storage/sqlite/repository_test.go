package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-60/internal/domain"
)

// Integration tests: a real database, created per test in t.TempDir(). These
// catch what the service fakes cannot - SQL typos, constraint violations, and
// values that do not survive a round trip through storage.

func newTestRepository(t *testing.T) *Repository {
	t.Helper()

	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	return New(db)
}

func testBook(t *testing.T, isbn string) domain.Book {
	t.Helper()

	parsed, err := domain.NewISBN(isbn)
	if err != nil {
		t.Fatalf("isbn: %v", err)
	}

	book, err := domain.NewBook(parsed, "The Go Programming Language", "Donovan", 380,
		time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("new book: %v", err)
	}

	return book
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()

	repository := newTestRepository(t)
	ctx := context.Background()

	created, err := repository.Create(ctx, testBook(t, "9780134190440"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if created.ID == 0 {
		t.Fatal("no id assigned")
	}

	found, err := repository.ByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("by id: %v", err)
	}

	// Every field must survive storage, including the value object and the
	// timestamp - this is the assertion a fake repository can never make.
	if found.ISBN != created.ISBN || found.Title != created.Title ||
		found.Pages != created.Pages || !found.AddedAt.Equal(created.AddedAt) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", found, created)
	}

	byISBN, err := repository.ByISBN(ctx, created.ISBN)
	if err != nil || byISBN.ID != created.ID {
		t.Fatalf("by isbn = %+v (err=%v)", byISBN, err)
	}
}

func TestUniqueISBNIsEnforcedByTheDatabase(t *testing.T) {
	t.Parallel()

	repository := newTestRepository(t)
	ctx := context.Background()

	if _, err := repository.Create(ctx, testBook(t, "9780134190440")); err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err := repository.Create(ctx, testBook(t, "9780134190440"))

	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestNotFound(t *testing.T) {
	t.Parallel()

	repository := newTestRepository(t)
	ctx := context.Background()

	if _, err := repository.ByID(ctx, 999); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("by id err = %v, want ErrNotFound", err)
	}

	isbn, err := domain.NewISBN("9781617291784")
	if err != nil {
		t.Fatalf("isbn: %v", err)
	}

	if _, err := repository.ByISBN(ctx, isbn); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("by isbn err = %v, want ErrNotFound", err)
	}

	if err := repository.Delete(ctx, 999); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete err = %v, want ErrNotFound", err)
	}

	if _, err := repository.Update(ctx, domain.Book{ID: 999}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update err = %v, want ErrNotFound", err)
	}
}

func TestUpdatePersistsStateAndProgress(t *testing.T) {
	t.Parallel()

	repository := newTestRepository(t)
	ctx := context.Background()

	book, err := repository.Create(ctx, testBook(t, "9780134190440"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := book.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := book.RecordProgress(380, time.Now().UTC()); err != nil {
		t.Fatalf("progress: %v", err)
	}

	updated, err := repository.Update(ctx, book)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if updated.Status != domain.StatusFinished || updated.Progress != 380 {
		t.Fatalf("updated = %+v", updated)
	}

	if updated.FinishedAt.IsZero() {
		t.Fatal("finished_at was not persisted")
	}
}

func TestListFilterAndPagination(t *testing.T) {
	t.Parallel()

	repository := newTestRepository(t)
	ctx := context.Background()

	for _, isbn := range []string{"9780134190440", "9781617291784", "9781491941195"} {
		book, err := repository.Create(ctx, testBook(t, isbn))
		if err != nil {
			t.Fatalf("create: %v", err)
		}

		if isbn != "9781491941195" {
			if err := book.Start(); err != nil {
				t.Fatalf("start: %v", err)
			}

			if _, err := repository.Update(ctx, book); err != nil {
				t.Fatalf("update: %v", err)
			}
		}
	}

	all, err := repository.List(ctx, "", 10, 0)
	if err != nil || len(all) != 3 {
		t.Fatalf("list all = %d (err=%v)", len(all), err)
	}

	reading, err := repository.List(ctx, domain.StatusReading, 10, 0)
	if err != nil || len(reading) != 2 {
		t.Fatalf("list reading = %d (err=%v)", len(reading), err)
	}

	page, err := repository.List(ctx, "", 2, 2)
	if err != nil || len(page) != 1 {
		t.Fatalf("paged list = %d (err=%v)", len(page), err)
	}
}

func TestStats(t *testing.T) {
	t.Parallel()

	repository := newTestRepository(t)
	ctx := context.Background()

	first, err := repository.Create(ctx, testBook(t, "9780134190440"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := first.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := first.RecordProgress(190, time.Now().UTC()); err != nil {
		t.Fatalf("progress: %v", err)
	}

	if _, err := repository.Update(ctx, first); err != nil {
		t.Fatalf("update: %v", err)
	}

	if _, err := repository.Create(ctx, testBook(t, "9781617291784")); err != nil {
		t.Fatalf("create: %v", err)
	}

	stats, err := repository.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}

	if stats.Total != 2 || stats.Reading != 1 || stats.PagesRead != 190 || stats.PagesTotal != 760 {
		t.Fatalf("stats = %+v", stats)
	}
}
