package main

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

/*
Day 47 - Databases (II) & Repositories: Query Organization

Tasks covered:

 1. All SQL centralized in queries.go as named constants
 2. sqlc explored as an option (see ./sqlc: query.sql, schema.sql, sqlc.yaml)
 3. Queries named after business operations, not "query1"
 4. N+1 avoided with a batched IN query and with a JOIN, both measured

Files:

	queries.go   every SQL statement in the program, grouped and named
	store.go     the data access layer; contains no SQL text
	sqlc/        the optional generated-code path, documented not required
	store_test.go the three loading strategies must agree, and 1 != N+1

Run:

	go run .
	AUTHORS=200 BOOKS_PER_AUTHOR=5 go run .

Environment variables:

	AUTHORS            number of seeded authors.      Default: 50
	BOOKS_PER_AUTHOR   books per author.              Default: 4
	DB_PATH            SQLite path.                   Default: :memory:

Test:

	go test ./...
*/

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day47: ")

	ctx := context.Background()

	authorCount := envInt("AUTHORS", 50)
	booksPerAuthor := envInt("BOOKS_PER_AUTHOR", 4)

	db, err := openCatalogDB(ctx, envOr("DB_PATH", ":memory:"))
	if err != nil {
		log.Fatalf("database unavailable: %v", err)
	}

	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	}()

	store := NewCatalogStore(db)

	if err := seed(ctx, store, authorCount, booksPerAuthor); err != nil {
		log.Fatalf("seed: %v", err)
	}

	books, err := store.CountBooks(ctx)
	if err != nil {
		log.Fatalf("count books: %v", err)
	}

	fmt.Printf("\nSeeded %d authors and %d books\n", authorCount, books)

	//
	// The same result, three ways
	//

	fmt.Println("\nLoading every author with their books")
	fmt.Println(strings.Repeat("-", 72))
	fmt.Printf("%-28s %-10s %-12s %s\n", "STRATEGY", "QUERIES", "DURATION", "RESULT")

	strategies := []struct {
		name string
		load func(context.Context) ([]Author, error)
	}{
		{
			"N+1 (one per author)",
			func(ctx context.Context) ([]Author, error) {
				return store.ListAuthorsWithBooksNPlusOne(ctx, authorCount)
			},
		},
		{
			"batched IN (always 2)",
			func(ctx context.Context) ([]Author, error) {
				return store.ListAuthorsWithBooksBatched(ctx, authorCount)
			},
		},
		{
			"LEFT JOIN (always 1)",
			func(ctx context.Context) ([]Author, error) {
				return store.ListAuthorsWithBooksJoined(ctx)
			},
		},
	}

	results := make([][]Author, 0, len(strategies))

	for _, strategy := range strategies {
		store.ResetQueryCount()

		start := time.Now()

		authors, err := strategy.load(ctx)
		if err != nil {
			log.Fatalf("%s: %v", strategy.name, err)
		}

		elapsed := time.Since(start)

		results = append(results, authors)

		fmt.Printf("%-28s %-10d %-12s %d authors, %d books\n",
			strategy.name,
			store.QueryCount(),
			elapsed.Round(time.Microsecond),
			len(authors),
			countBooks(authors),
		)
	}

	// Same data or the comparison is meaningless.
	for i := 1; i < len(results); i++ {
		if countBooks(results[i]) != countBooks(results[0]) {
			log.Fatalf("strategy %q returned different data", strategies[i].name)
		}
	}

	fmt.Println("\nSame rows, same order, different number of round trips.")
	fmt.Println("On a database one network hop away, each of those N queries costs")
	fmt.Println("its own latency - which is why N+1 hurts in production long before")
	fmt.Println("it shows up on a laptop with an in-process database.")

	//
	// Aggregates belong in SQL
	//

	fmt.Println("\nTop authors (aggregated by the database, one query)")
	fmt.Println(strings.Repeat("-", 72))

	store.ResetQueryCount()

	stats, err := store.AuthorStats(ctx, booksPerAuthor)
	if err != nil {
		log.Fatalf("author stats: %v", err)
	}

	fmt.Printf("%-28s %-8s %-16s %s\n", "AUTHOR", "BOOKS", "CATALOG VALUE", "LATEST")

	for _, stat := range stats[:min(5, len(stats))] {
		fmt.Printf("%-28s %-8d %-16s %d\n",
			stat.Name,
			stat.BookCount,
			formatMoney(stat.CatalogValueCents),
			stat.LatestYear,
		)
	}

	fmt.Printf("\n%d authors matched, computed in %d query\n", len(stats), store.QueryCount())
}

func seed(ctx context.Context, store *CatalogStore, authors, booksPerAuthor int) error {
	// Deterministic seed: the demo prints the same numbers on every run.
	random := rand.New(rand.NewPCG(42, 1024))

	countries := []string{"TR", "UK", "US", "DE", "JP"}

	for i := range authors {
		name := fmt.Sprintf("Author %02d", i+1)

		authorID, err := store.CreateAuthor(ctx, name, countries[i%len(countries)])
		if err != nil {
			return err
		}

		for j := range booksPerAuthor {
			if _, err := store.CreateBook(ctx, Book{
				AuthorID:   authorID,
				Title:      fmt.Sprintf("%s - Volume %d", name, j+1),
				Year:       2000 + random.IntN(25),
				PriceCents: int64(1500 + random.IntN(6000)),
			}); err != nil {
				return err
			}
		}
	}

	return nil
}

func countBooks(authors []Author) int {
	total := 0

	for _, author := range authors {
		total += len(author.Books)
	}

	return total
}

func formatMoney(cents int64) string {
	return strconv.FormatFloat(float64(cents)/100, 'f', 2, 64)
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))

	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		log.Printf("invalid %s=%q, using %d", key, value, fallback)
		return fallback
	}

	return parsed
}
