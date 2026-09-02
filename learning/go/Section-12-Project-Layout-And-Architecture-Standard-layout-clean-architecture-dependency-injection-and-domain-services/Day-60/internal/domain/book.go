// Package domain is the centre of the service: business types, their
// invariants, the errors they raise and the ports the outer layers implement.
//
// It imports the standard library only. internal/arch enforces that.
package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrValidation = errors.New("validation failed")
	ErrNotFound   = errors.New("not found")
	ErrConflict   = errors.New("conflict")
)

type Status string

const (
	StatusWishlist  Status = "wishlist"
	StatusReading   Status = "reading"
	StatusFinished  Status = "finished"
	StatusAbandoned Status = "abandoned"
)

func (s Status) Valid() bool {
	switch s {
	case StatusWishlist, StatusReading, StatusFinished, StatusAbandoned:
		return true
	default:
		return false
	}
}

// ISBN is a value object: once constructed it is known to be well formed, so
// nothing downstream re-checks it.
type ISBN struct {
	value string
}

func NewISBN(raw string) (ISBN, error) {
	value := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(raw), "-", ""))

	if len(value) != 13 {
		return ISBN{}, fmt.Errorf("%w: isbn must have 13 digits", ErrValidation)
	}

	for _, char := range value {
		if char < '0' || char > '9' {
			return ISBN{}, fmt.Errorf("%w: isbn may contain digits and dashes only", ErrValidation)
		}
	}

	return ISBN{value: value}, nil
}

func (i ISBN) String() string { return i.value }

func (i ISBN) IsZero() bool { return i.value == "" }

// Book is the aggregate. Fields are exported because the storage layer maps
// them, but every state change goes through a method that enforces the rules.
type Book struct {
	ID       int64
	ISBN     ISBN
	Title    string
	Author   string
	Pages    int
	Status   Status
	Progress int // pages read

	AddedAt    time.Time
	FinishedAt time.Time
}

func NewBook(isbn ISBN, title, author string, pages int, now time.Time) (Book, error) {
	title = strings.TrimSpace(title)
	author = strings.TrimSpace(author)

	switch {
	case isbn.IsZero():
		return Book{}, fmt.Errorf("%w: isbn is required", ErrValidation)
	case title == "":
		return Book{}, fmt.Errorf("%w: title is required", ErrValidation)
	case len(title) > 300:
		return Book{}, fmt.Errorf("%w: title must be at most 300 characters", ErrValidation)
	case author == "":
		return Book{}, fmt.Errorf("%w: author is required", ErrValidation)
	case pages <= 0 || pages > 20000:
		return Book{}, fmt.Errorf("%w: pages must be between 1 and 20000", ErrValidation)
	}

	return Book{
		ISBN:    isbn,
		Title:   title,
		Author:  author,
		Pages:   pages,
		Status:  StatusWishlist,
		AddedAt: now,
	}, nil
}

// Start moves a book from the wishlist into progress.
func (b *Book) Start() error {
	if b.Status != StatusWishlist && b.Status != StatusAbandoned {
		return fmt.Errorf("%w: a %s book cannot be started", ErrConflict, b.Status)
	}

	b.Status = StatusReading

	return nil
}

// RecordProgress carries the rules that a page count cannot go backwards, can
// never exceed the book, and that reaching the last page finishes the book.
func (b *Book) RecordProgress(page int, now time.Time) error {
	if b.Status != StatusReading {
		return fmt.Errorf("%w: progress can only be recorded while reading, not while %s",
			ErrConflict, b.Status)
	}

	switch {
	case page <= 0:
		return fmt.Errorf("%w: page must be positive", ErrValidation)
	case page > b.Pages:
		return fmt.Errorf("%w: page %d is past the end of a %d page book", ErrValidation, page, b.Pages)
	case page < b.Progress:
		return fmt.Errorf("%w: progress cannot go backwards (currently at page %d)", ErrValidation, b.Progress)
	}

	b.Progress = page

	if page == b.Pages {
		b.Status = StatusFinished
		b.FinishedAt = now
	}

	return nil
}

func (b *Book) Abandon() error {
	if b.Status == StatusFinished {
		return fmt.Errorf("%w: a finished book cannot be abandoned", ErrConflict)
	}

	b.Status = StatusAbandoned

	return nil
}

// PercentRead is a derived value, never a stored column that can drift.
func (b Book) PercentRead() int {
	if b.Pages == 0 {
		return 0
	}

	return b.Progress * 100 / b.Pages
}
