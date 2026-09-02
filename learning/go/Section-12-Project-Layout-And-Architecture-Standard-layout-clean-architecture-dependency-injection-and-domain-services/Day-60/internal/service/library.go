// Package service implements the use cases. It depends on the domain and on
// the ports the domain declares - never on storage or transport.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-60/internal/domain"
)

type Library struct {
	books domain.BookRepository
	stats domain.StatsReader
	clock domain.Clock
}

// NewLibrary states every dependency explicitly. stats may be nil when the
// storage engine cannot compute it; the use case degrades instead of failing.
func NewLibrary(books domain.BookRepository, stats domain.StatsReader, clock domain.Clock) *Library {
	return &Library{books: books, stats: stats, clock: clock}
}

// AddBook is a use case: parse the input into domain types, apply the
// uniqueness rule, persist.
func (l *Library) AddBook(ctx context.Context, rawISBN, title, author string, pages int) (domain.Book, error) {
	isbn, err := domain.NewISBN(rawISBN)
	if err != nil {
		return domain.Book{}, err
	}

	existing, err := l.books.ByISBN(ctx, isbn)

	switch {
	case err == nil:
		return domain.Book{}, fmt.Errorf("%w: %s is already in the library as book %d",
			domain.ErrConflict, isbn, existing.ID)

	case !errors.Is(err, domain.ErrNotFound):
		return domain.Book{}, fmt.Errorf("add book %s: %w", isbn, err)
	}

	book, err := domain.NewBook(isbn, title, author, pages, l.clock.Now())
	if err != nil {
		return domain.Book{}, err
	}

	return l.books.Create(ctx, book)
}

func (l *Library) Book(ctx context.Context, id int64) (domain.Book, error) {
	return l.books.ByID(ctx, id)
}

func (l *Library) List(ctx context.Context, status domain.Status, limit, offset int) ([]domain.Book, error) {
	if status != "" && !status.Valid() {
		return nil, fmt.Errorf("%w: %q is not a valid status", domain.ErrValidation, status)
	}

	if limit <= 0 || limit > 100 {
		limit = 20
	}

	if offset < 0 {
		offset = 0
	}

	return l.books.List(ctx, status, limit, offset)
}

func (l *Library) Start(ctx context.Context, id int64) (domain.Book, error) {
	return l.mutate(ctx, id, func(book *domain.Book) error { return book.Start() })
}

func (l *Library) RecordProgress(ctx context.Context, id, page int64) (domain.Book, error) {
	return l.mutate(ctx, int64(id), func(book *domain.Book) error {
		return book.RecordProgress(int(page), l.clock.Now())
	})
}

func (l *Library) Abandon(ctx context.Context, id int64) (domain.Book, error) {
	return l.mutate(ctx, id, func(book *domain.Book) error { return book.Abandon() })
}

func (l *Library) Remove(ctx context.Context, id int64) error {
	return l.books.Delete(ctx, id)
}

// mutate is the load-apply-save shape every state transition shares. The rule
// itself always lives on the entity.
func (l *Library) mutate(ctx context.Context, id int64, apply func(*domain.Book) error) (domain.Book, error) {
	book, err := l.books.ByID(ctx, id)
	if err != nil {
		return domain.Book{}, err
	}

	if err := apply(&book); err != nil {
		return domain.Book{}, err
	}

	return l.books.Update(ctx, book)
}

// Progress is a read use case combining a storage read model with a domain
// calculation.
func (l *Library) Progress(ctx context.Context) (domain.Stats, int, error) {
	if l.stats == nil {
		return domain.Stats{}, 0, fmt.Errorf("progress: %w: statistics are not available from this storage",
			domain.ErrNotFound)
	}

	stats, err := l.stats.Stats(ctx)
	if err != nil {
		return domain.Stats{}, 0, fmt.Errorf("progress: %w", err)
	}

	percent := 0

	if stats.PagesTotal > 0 {
		percent = stats.PagesRead * 100 / stats.PagesTotal
	}

	return stats, percent, nil
}

// SystemClock is the production clock. It lives here rather than in the domain
// so the domain keeps zero implementations.
type SystemClock struct{}

var _ domain.Clock = SystemClock{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }
