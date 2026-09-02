package main

import (
	"context"
	"errors"
	"testing"
)

/*
These tests protect two things that reviews miss:

  - the three loading strategies must return identical data, otherwise
    "optimising" a handler silently changes its response
  - the query count of each strategy is part of its contract; a refactor that
    reintroduces N+1 should fail a test, not a production dashboard
*/

func newTestStore(t *testing.T) *CatalogStore {
	t.Helper()

	db, err := openCatalogDB(t.Context(), ":memory:")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})

	return NewCatalogStore(db)
}

func seedTestData(t *testing.T, store *CatalogStore, authors, booksPerAuthor int) {
	t.Helper()

	ctx := context.Background()

	for i := range authors {
		id, err := store.CreateAuthor(ctx, "Author "+string(rune('A'+i)), "TR")
		if err != nil {
			t.Fatalf("create author: %v", err)
		}

		for j := range booksPerAuthor {
			if _, err := store.CreateBook(ctx, Book{
				AuthorID:   id,
				Title:      "Book",
				Year:       2000 + j,
				PriceCents: int64(1000 * (j + 1)),
			}); err != nil {
				t.Fatalf("create book: %v", err)
			}
		}
	}
}

func TestLoadingStrategiesAgree(t *testing.T) {
	store := newTestStore(t)
	seedTestData(t, store, 5, 3)

	ctx := context.Background()

	nPlusOne, err := store.ListAuthorsWithBooksNPlusOne(ctx, 10)
	if err != nil {
		t.Fatalf("n+1: %v", err)
	}

	batched, err := store.ListAuthorsWithBooksBatched(ctx, 10)
	if err != nil {
		t.Fatalf("batched: %v", err)
	}

	joined, err := store.ListAuthorsWithBooksJoined(ctx)
	if err != nil {
		t.Fatalf("joined: %v", err)
	}

	for _, result := range []struct {
		name    string
		authors []Author
	}{
		{"batched", batched},
		{"joined", joined},
	} {
		if len(result.authors) != len(nPlusOne) {
			t.Fatalf("%s returned %d authors, want %d", result.name, len(result.authors), len(nPlusOne))
		}

		for i := range nPlusOne {
			if result.authors[i].ID != nPlusOne[i].ID {
				t.Fatalf("%s author %d = %d, want %d", result.name, i, result.authors[i].ID, nPlusOne[i].ID)
			}

			if len(result.authors[i].Books) != len(nPlusOne[i].Books) {
				t.Fatalf("%s author %d has %d books, want %d",
					result.name, i, len(result.authors[i].Books), len(nPlusOne[i].Books))
			}

			for j := range nPlusOne[i].Books {
				if result.authors[i].Books[j].ID != nPlusOne[i].Books[j].ID {
					t.Fatalf("%s author %d book %d differs", result.name, i, j)
				}
			}
		}
	}
}

func TestQueryCounts(t *testing.T) {
	const authors = 8

	store := newTestStore(t)
	seedTestData(t, store, authors, 2)

	ctx := context.Background()

	tests := []struct {
		name string
		load func() error
		want int64
	}{
		{
			"n+1 grows with the page size",
			func() error {
				_, err := store.ListAuthorsWithBooksNPlusOne(ctx, authors)
				return err
			},
			authors + 1,
		},
		{
			"batched stays at two",
			func() error {
				_, err := store.ListAuthorsWithBooksBatched(ctx, authors)
				return err
			},
			2,
		},
		{
			"join stays at one",
			func() error {
				_, err := store.ListAuthorsWithBooksJoined(ctx)
				return err
			},
			1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store.ResetQueryCount()

			if err := test.load(); err != nil {
				t.Fatalf("load: %v", err)
			}

			if got := store.QueryCount(); got != test.want {
				t.Fatalf("queries = %d, want %d", got, test.want)
			}
		})
	}
}

func TestBuildInClause(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "NULL"},
		{1, "?"},
		{3, "?, ?, ?"},
	}

	for _, test := range tests {
		if got := buildInClause(test.n); got != test.want {
			t.Fatalf("buildInClause(%d) = %q, want %q", test.n, got, test.want)
		}
	}
}

// A hostile author name must be stored and returned verbatim: it is data, and
// the batched IN query binds it as a parameter like everything else.
func TestBatchedQueryIsInjectionSafe(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	hostile := "Robert'); DROP TABLE books;--"

	id, err := store.CreateAuthor(ctx, hostile, "TR")
	if err != nil {
		t.Fatalf("create author: %v", err)
	}

	if _, err := store.CreateBook(ctx, Book{AuthorID: id, Title: "Still here", Year: 2024}); err != nil {
		t.Fatalf("create book: %v", err)
	}

	authors, err := store.ListAuthorsWithBooksBatched(ctx, 10)
	if err != nil {
		t.Fatalf("batched: %v", err)
	}

	if len(authors) != 1 || authors[0].Name != hostile {
		t.Fatalf("author name round trip failed: %+v", authors)
	}

	if count, err := store.CountBooks(ctx); err != nil || count != 1 {
		t.Fatalf("books table damaged: count=%d err=%v", count, err)
	}
}

func TestAuthorByIDNotFound(t *testing.T) {
	store := newTestStore(t)

	if _, err := store.AuthorByID(context.Background(), 4242); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestAuthorStatsAggregatesInSQL(t *testing.T) {
	store := newTestStore(t)
	seedTestData(t, store, 3, 2) // each author: books priced 10.00 and 20.00

	store.ResetQueryCount()

	stats, err := store.AuthorStats(context.Background(), 2)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}

	if len(stats) != 3 {
		t.Fatalf("stats returned %d rows, want 3", len(stats))
	}

	if store.QueryCount() != 1 {
		t.Fatalf("stats used %d queries, want 1", store.QueryCount())
	}

	for _, stat := range stats {
		if stat.BookCount != 2 {
			t.Fatalf("%s book count = %d, want 2", stat.Name, stat.BookCount)
		}

		if stat.CatalogValueCents != 3000 {
			t.Fatalf("%s catalog value = %d, want 3000", stat.Name, stat.CatalogValueCents)
		}
	}
}
