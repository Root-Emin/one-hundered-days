package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"
)

/*
The data access layer.

No SQL text appears here: every statement comes from queries.go by name. That
separation is what makes the "centralize SQL" task more than cosmetics - the
Go code reads as operations, and the SQL file reads as a data contract.
*/

var ErrNotFound = errors.New("not found")

//
// DOMAIN TYPES
//

type Author struct {
	ID      int64
	Name    string
	Country string
	Books   []Book
}

type Book struct {
	ID         int64
	AuthorID   int64
	Title      string
	Year       int
	PriceCents int64
}

type AuthorStats struct {
	AuthorID          int64
	Name              string
	BookCount         int
	CatalogValueCents int64
	LatestYear        int
}

//
// STORE
//
// queryCount is not decoration: "how many round trips did that endpoint make?"
// is the question that finds N+1 problems, and the cheapest way to answer it
// is to count.
//

type CatalogStore struct {
	db         *sql.DB
	queryCount atomic.Int64
}

func NewCatalogStore(db *sql.DB) *CatalogStore {
	return &CatalogStore{db: db}
}

func (s *CatalogStore) QueryCount() int64 {
	return s.queryCount.Load()
}

func (s *CatalogStore) ResetQueryCount() {
	s.queryCount.Store(0)
}

func (s *CatalogStore) query(ctx context.Context, statement string, args ...any) (*sql.Rows, error) {
	s.queryCount.Add(1)

	return s.db.QueryContext(ctx, statement, args...)
}

func (s *CatalogStore) queryRow(ctx context.Context, statement string, args ...any) *sql.Row {
	s.queryCount.Add(1)

	return s.db.QueryRowContext(ctx, statement, args...)
}

//
// WRITES
//

func (s *CatalogStore) CreateAuthor(ctx context.Context, name, country string) (int64, error) {
	result, err := s.db.ExecContext(ctx, InsertAuthorSQL, name, country)
	if err != nil {
		return 0, fmt.Errorf("insert author %q: %w", name, err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert author %q: read id: %w", name, err)
	}

	return id, nil
}

func (s *CatalogStore) CreateBook(ctx context.Context, book Book) (int64, error) {
	result, err := s.db.ExecContext(ctx, InsertBookSQL, book.AuthorID, book.Title, book.Year, book.PriceCents)
	if err != nil {
		return 0, fmt.Errorf("insert book %q: %w", book.Title, err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert book %q: read id: %w", book.Title, err)
	}

	return id, nil
}

//
// READS
//

func (s *CatalogStore) AuthorByID(ctx context.Context, id int64) (Author, error) {
	var author Author

	err := s.queryRow(ctx, SelectAuthorByIDSQL, id).Scan(&author.ID, &author.Name, &author.Country)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Author{}, fmt.Errorf("author %d: %w", id, ErrNotFound)
	case err != nil:
		return Author{}, fmt.Errorf("select author %d: %w", id, err)
	}

	return author, nil
}

func (s *CatalogStore) ListAuthors(ctx context.Context, limit, offset int) ([]Author, error) {
	rows, err := s.query(ctx, SelectAuthorsSQL, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("select authors: %w", err)
	}

	defer closeRows(rows)

	authors := make([]Author, 0, limit)

	for rows.Next() {
		var author Author

		if err := rows.Scan(&author.ID, &author.Name, &author.Country); err != nil {
			return nil, fmt.Errorf("scan author: %w", err)
		}

		authors = append(authors, author)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate authors: %w", err)
	}

	return authors, nil
}

func (s *CatalogStore) BooksByAuthorID(ctx context.Context, authorID int64) ([]Book, error) {
	rows, err := s.query(ctx, SelectBooksByAuthorIDSQL, authorID)
	if err != nil {
		return nil, fmt.Errorf("select books of author %d: %w", authorID, err)
	}

	defer closeRows(rows)

	return scanBooks(rows)
}

//
// THE THREE WAYS TO LOAD AUTHORS WITH THEIR BOOKS
//

// ListAuthorsWithBooksNPlusOne is the anti-pattern: one query for the list,
// then one more per row. It looks innocent because each individual query is
// fast and correct. With 100 authors it is 101 round trips, and the latency
// is the network cost multiplied by the page size.
func (s *CatalogStore) ListAuthorsWithBooksNPlusOne(ctx context.Context, limit int) ([]Author, error) {
	authors, err := s.ListAuthors(ctx, limit, 0) // 1 query
	if err != nil {
		return nil, err
	}

	for i := range authors {
		books, err := s.BooksByAuthorID(ctx, authors[i].ID) // + N queries
		if err != nil {
			return nil, err
		}

		authors[i].Books = books
	}

	return authors, nil
}

// ListAuthorsWithBooksBatched keeps two queries no matter how many authors:
// fetch the page, then fetch every child row for that page in one IN query
// and group them in Go. This is the shape most ORMs call "preloading".
func (s *CatalogStore) ListAuthorsWithBooksBatched(ctx context.Context, limit int) ([]Author, error) {
	authors, err := s.ListAuthors(ctx, limit, 0) // 1 query
	if err != nil {
		return nil, err
	}

	if len(authors) == 0 {
		return authors, nil
	}

	ids := make([]any, 0, len(authors))

	for _, author := range authors {
		ids = append(ids, author.ID)
	}

	// Placeholders are generated from the count; the values still travel as
	// parameters. Building "IN (" + strings.Join(values, ",") + ")" instead
	// would be an injection hole.
	statement := fmt.Sprintf(SelectBooksByAuthorIDsSQL, buildInClause(len(ids)))

	rows, err := s.query(ctx, statement, ids...) // + 1 query
	if err != nil {
		return nil, fmt.Errorf("select books for author page: %w", err)
	}

	defer closeRows(rows)

	books, err := scanBooks(rows)
	if err != nil {
		return nil, err
	}

	byAuthor := make(map[int64][]Book, len(authors))

	for _, book := range books {
		byAuthor[book.AuthorID] = append(byAuthor[book.AuthorID], book)
	}

	for i := range authors {
		authors[i].Books = byAuthor[authors[i].ID]
	}

	return authors, nil
}

// ListAuthorsWithBooksJoined does it in a single round trip. The trade-off is
// that author columns repeat on every book row, so it is the right choice for
// small child sets and the wrong one when the parent row is wide.
func (s *CatalogStore) ListAuthorsWithBooksJoined(ctx context.Context) ([]Author, error) {
	rows, err := s.query(ctx, SelectAuthorsWithBooksSQL) // exactly 1 query
	if err != nil {
		return nil, fmt.Errorf("select authors with books: %w", err)
	}

	defer closeRows(rows)

	var (
		authors []Author
		index   = make(map[int64]int)
	)

	for rows.Next() {
		var (
			author Author
			book   Book

			// LEFT JOIN: an author with no books produces NULL book columns,
			// which only scan into nullable types.
			bookID     sql.NullInt64
			title      sql.NullString
			year       sql.NullInt64
			priceCents sql.NullInt64
		)

		if err := rows.Scan(
			&author.ID, &author.Name, &author.Country,
			&bookID, &title, &year, &priceCents,
		); err != nil {
			return nil, fmt.Errorf("scan joined row: %w", err)
		}

		position, seen := index[author.ID]
		if !seen {
			position = len(authors)
			index[author.ID] = position
			authors = append(authors, author)
		}

		if !bookID.Valid {
			continue
		}

		book = Book{
			ID:         bookID.Int64,
			AuthorID:   author.ID,
			Title:      title.String,
			Year:       int(year.Int64),
			PriceCents: priceCents.Int64,
		}

		authors[position].Books = append(authors[position].Books, book)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate joined rows: %w", err)
	}

	return authors, nil
}

// AuthorStats pushes counting and summing into the database. Loading every
// book into Go to add up prices is the same N+1 mistake wearing a different
// hat: it moves work that the database does in one pass onto the network.
func (s *CatalogStore) AuthorStats(ctx context.Context, minBooks int) ([]AuthorStats, error) {
	rows, err := s.query(ctx, SelectAuthorStatsSQL, minBooks)
	if err != nil {
		return nil, fmt.Errorf("select author stats: %w", err)
	}

	defer closeRows(rows)

	var stats []AuthorStats

	for rows.Next() {
		var stat AuthorStats

		if err := rows.Scan(
			&stat.AuthorID,
			&stat.Name,
			&stat.BookCount,
			&stat.CatalogValueCents,
			&stat.LatestYear,
		); err != nil {
			return nil, fmt.Errorf("scan author stats: %w", err)
		}

		stats = append(stats, stat)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate author stats: %w", err)
	}

	return stats, nil
}

func (s *CatalogStore) CountBooks(ctx context.Context) (int, error) {
	var count int

	if err := s.queryRow(ctx, CountBooksSQL).Scan(&count); err != nil {
		return 0, fmt.Errorf("count books: %w", err)
	}

	return count, nil
}

//
// HELPERS
//

// buildInClause returns "?, ?, ?" for n values. Only the number of
// placeholders is derived from user input, never the values themselves.
func buildInClause(n int) string {
	if n <= 0 {
		return "NULL"
	}

	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func scanBooks(rows *sql.Rows) ([]Book, error) {
	var books []Book

	for rows.Next() {
		var book Book

		if err := rows.Scan(&book.ID, &book.AuthorID, &book.Title, &book.Year, &book.PriceCents); err != nil {
			return nil, fmt.Errorf("scan book: %w", err)
		}

		books = append(books, book)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate books: %w", err)
	}

	return books, nil
}

func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		log.Printf("close rows: %v", err)
	}
}

func openCatalogDB(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close database: %v", closeErr)
		}

		return nil, fmt.Errorf("ping database: %w", err)
	}

	if _, err := db.ExecContext(ctx, SchemaSQL); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close database: %v", closeErr)
		}

		return nil, fmt.Errorf("create schema: %w", err)
	}

	return db, nil
}
