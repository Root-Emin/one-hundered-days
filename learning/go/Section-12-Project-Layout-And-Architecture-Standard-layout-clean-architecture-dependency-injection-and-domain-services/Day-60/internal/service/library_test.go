package service

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-60/internal/domain"
)

/*
Use case tests with fakes.

No database, no HTTP, no waiting: the ports are satisfied by the fake below
and a clock the test controls. Running these on every save is realistic
because they finish in milliseconds.
*/

type fakeBooks struct {
	mu     sync.Mutex
	books  map[int64]domain.Book
	nextID int64
	failOn string
}

func newFakeBooks() *fakeBooks {
	return &fakeBooks{books: map[int64]domain.Book{}, nextID: 1}
}

var _ domain.BookRepository = (*fakeBooks)(nil)

func (f *fakeBooks) Create(ctx context.Context, book domain.Book) (domain.Book, error) {
	if f.failOn == "create" {
		return domain.Book{}, errors.New("storage unavailable")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	book.ID = f.nextID
	f.nextID++
	f.books[book.ID] = book

	return book, nil
}

func (f *fakeBooks) Update(ctx context.Context, book domain.Book) (domain.Book, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, found := f.books[book.ID]; !found {
		return domain.Book{}, domain.ErrNotFound
	}

	f.books[book.ID] = book

	return book, nil
}

func (f *fakeBooks) ByID(ctx context.Context, id int64) (domain.Book, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	book, found := f.books[id]
	if !found {
		return domain.Book{}, domain.ErrNotFound
	}

	return book, nil
}

func (f *fakeBooks) ByISBN(ctx context.Context, isbn domain.ISBN) (domain.Book, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, book := range f.books {
		if book.ISBN == isbn {
			return book, nil
		}
	}

	return domain.Book{}, domain.ErrNotFound
}

func (f *fakeBooks) List(ctx context.Context, status domain.Status, limit, offset int) ([]domain.Book, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	matched := make([]domain.Book, 0, len(f.books))

	for _, book := range f.books {
		if status == "" || book.Status == status {
			matched = append(matched, book)
		}
	}

	sort.Slice(matched, func(i, j int) bool { return matched[i].ID < matched[j].ID })

	if offset >= len(matched) {
		return []domain.Book{}, nil
	}

	matched = matched[offset:]

	if len(matched) > limit {
		matched = matched[:limit]
	}

	return matched, nil
}

func (f *fakeBooks) Delete(ctx context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, found := f.books[id]; !found {
		return domain.ErrNotFound
	}

	delete(f.books, id)

	return nil
}

type fixedClock struct {
	now time.Time
}

var _ domain.Clock = (*fixedClock)(nil)

func (c *fixedClock) Now() time.Time { return c.now }

func newLibrary(t *testing.T) (*Library, *fakeBooks, *fixedClock) {
	t.Helper()

	books := newFakeBooks()
	clock := &fixedClock{now: time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)}

	return NewLibrary(books, nil, clock), books, clock
}

const validISBN = "978-0-13-419044-0"

func TestAddBook(t *testing.T) {
	t.Parallel()

	library, _, clock := newLibrary(t)

	book, err := library.AddBook(context.Background(), validISBN, " The Go Programming Language ", "Donovan", 380)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	if book.ID == 0 || book.Status != domain.StatusWishlist {
		t.Fatalf("book = %+v", book)
	}

	if book.Title != "The Go Programming Language" {
		t.Fatalf("title = %q, want it trimmed", book.Title)
	}

	if book.ISBN.String() != "9780134190440" {
		t.Fatalf("isbn = %q, want the dashes stripped", book.ISBN)
	}

	if !book.AddedAt.Equal(clock.now) {
		t.Fatalf("added_at = %s, want the injected clock", book.AddedAt)
	}
}

func TestAddBookValidation(t *testing.T) {
	t.Parallel()

	library, _, _ := newLibrary(t)
	ctx := context.Background()

	tests := []struct {
		name   string
		isbn   string
		title  string
		author string
		pages  int
	}{
		{"short isbn", "123", "Title", "Author", 100},
		{"isbn with letters", "97801341904AB", "Title", "Author", 100},
		{"empty title", validISBN, "   ", "Author", 100},
		{"empty author", validISBN, "Title", "", 100},
		{"zero pages", validISBN, "Title", "Author", 0},
		{"absurd pages", validISBN, "Title", "Author", 100000},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := library.AddBook(ctx, test.isbn, test.title, test.author, test.pages)

			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation", err)
			}
		})
	}
}

func TestAddBookRejectsDuplicateISBN(t *testing.T) {
	t.Parallel()

	library, _, _ := newLibrary(t)
	ctx := context.Background()

	if _, err := library.AddBook(ctx, validISBN, "Title", "Author", 100); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Same book, formatted differently: the value object normalises it, so
	// the uniqueness rule still catches it.
	_, err := library.AddBook(ctx, "9780134190440", "Title again", "Author", 100)

	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestReadingWorkflow(t *testing.T) {
	t.Parallel()

	library, _, clock := newLibrary(t)
	ctx := context.Background()

	book, err := library.AddBook(ctx, validISBN, "Title", "Author", 100)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Progress before starting is a state error.
	if _, err := library.RecordProgress(ctx, book.ID, 10); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("progress on a wishlist book err = %v, want ErrConflict", err)
	}

	if _, err := library.Start(ctx, book.ID); err != nil {
		t.Fatalf("start: %v", err)
	}

	updated, err := library.RecordProgress(ctx, book.ID, 40)
	if err != nil {
		t.Fatalf("progress: %v", err)
	}

	if updated.PercentRead() != 40 {
		t.Fatalf("percent = %d, want 40", updated.PercentRead())
	}

	// Backwards and out of range are both rejected.
	if _, err := library.RecordProgress(ctx, book.ID, 20); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("backwards progress err = %v, want ErrValidation", err)
	}

	if _, err := library.RecordProgress(ctx, book.ID, 500); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("out of range progress err = %v, want ErrValidation", err)
	}

	// Reaching the last page finishes the book, and stamps it with the
	// injected clock.
	finished, err := library.RecordProgress(ctx, book.ID, 100)
	if err != nil {
		t.Fatalf("progress: %v", err)
	}

	if finished.Status != domain.StatusFinished {
		t.Fatalf("status = %q, want finished", finished.Status)
	}

	if !finished.FinishedAt.Equal(clock.now) {
		t.Fatalf("finished_at = %s, want the injected clock", finished.FinishedAt)
	}

	if _, err := library.Abandon(ctx, book.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("abandoning a finished book err = %v, want ErrConflict", err)
	}
}

func TestListFiltersAndPaginates(t *testing.T) {
	t.Parallel()

	library, _, _ := newLibrary(t)
	ctx := context.Background()

	isbns := []string{"9780134190440", "9781617291784", "9781491941195"}

	for i, isbn := range isbns {
		book, err := library.AddBook(ctx, isbn, "Title", "Author", 100)
		if err != nil {
			t.Fatalf("add: %v", err)
		}

		if i < 2 {
			if _, err := library.Start(ctx, book.ID); err != nil {
				t.Fatalf("start: %v", err)
			}
		}
	}

	reading, err := library.List(ctx, domain.StatusReading, 0, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(reading) != 2 {
		t.Fatalf("reading = %d, want 2", len(reading))
	}

	page, err := library.List(ctx, "", 2, 2)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(page) != 1 {
		t.Fatalf("page = %d, want 1", len(page))
	}

	if _, err := library.List(ctx, domain.Status("reading-ish"), 0, 0); !errors.Is(err, domain.ErrValidation) {
		t.Fatal("an invalid status filter was accepted")
	}
}

func TestStorageFailureSurfaces(t *testing.T) {
	t.Parallel()

	library, books, _ := newLibrary(t)
	books.failOn = "create"

	if _, err := library.AddBook(context.Background(), validISBN, "Title", "Author", 100); err == nil {
		t.Fatal("a storage failure was swallowed")
	}
}

func TestMissingBook(t *testing.T) {
	t.Parallel()

	library, _, _ := newLibrary(t)
	ctx := context.Background()

	if _, err := library.Book(ctx, 999); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}

	if err := library.Remove(ctx, 999); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestStatsUnavailableDegradesGracefully: the service was built without a
// stats reader, so it says so rather than panicking on a nil interface.
func TestStatsUnavailableDegradesGracefully(t *testing.T) {
	t.Parallel()

	library, _, _ := newLibrary(t)

	if _, _, err := library.Progress(context.Background()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
