package domain

import (
	"context"
	"time"
)

/*
Ports. Declared here, implemented outside, which is what makes the dependency
arrow point inward.
*/

type BookRepository interface {
	Create(ctx context.Context, book Book) (Book, error)
	Update(ctx context.Context, book Book) (Book, error)
	ByID(ctx context.Context, id int64) (Book, error)
	ByISBN(ctx context.Context, isbn ISBN) (Book, error)
	List(ctx context.Context, status Status, limit, offset int) ([]Book, error)
	Delete(ctx context.Context, id int64) error
}

type Clock interface {
	Now() time.Time
}

// Stats is a read model: what the library looks like as a whole, computed by
// storage because a database counts faster than a Go loop over every row.
type Stats struct {
	Total      int
	Reading    int
	Finished   int
	PagesRead  int
	PagesTotal int
}

type StatsReader interface {
	Stats(ctx context.Context) (Stats, error)
}
