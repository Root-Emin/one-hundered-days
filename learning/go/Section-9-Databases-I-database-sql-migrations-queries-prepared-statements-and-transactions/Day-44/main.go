package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

/*
Day 44 - Databases (I): Transactions

Tasks covered:

 1. Begin transactions with BeginTx and commit or rollback explicitly
 2. Defer Rollback so a failed path can never leave a transaction open
 3. Multi-step updates: move money between accounts atomically
 4. Isolation awareness: what concurrent transactions can and cannot see

Run:

	go run main.go

Environment variables:

	DB_PATH  SQLite file path. Default: ./data/day44.db
	         A file (not :memory:) is used so several pooled connections talk
	         to the same database during the concurrency demo.

The program:
  - transfers money inside a transaction and commits
  - fails a transfer mid-way and proves nothing was written
  - runs 50 concurrent transfers and checks the money invariant
  - shows a lost update caused by read-modify-write outside SQL
*/

const (
	defaultDBPath = "data/day44.db"
	txTimeout     = 10 * time.Second
)

//
// DOMAIN ERRORS
//

var (
	ErrAccountNotFound  = errors.New("account not found")
	ErrInsufficientFund = errors.New("insufficient funds")
	ErrInvalidAmount    = errors.New("amount must be positive")
	ErrSameAccount      = errors.New("source and destination are the same account")
)

//
// SCHEMA
//

const schema = `
CREATE TABLE IF NOT EXISTS accounts (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	owner   TEXT    NOT NULL UNIQUE,
	-- Money in minor units (kurus/cents). Never float: 0.1 + 0.2 != 0.3.
	balance INTEGER NOT NULL CHECK (balance >= 0)
);

CREATE TABLE IF NOT EXISTS ledger (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	from_account INTEGER NOT NULL REFERENCES accounts (id),
	to_account   INTEGER NOT NULL REFERENCES accounts (id),
	amount       INTEGER NOT NULL CHECK (amount > 0),
	created_at   TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_ledger_from ON ledger (from_account, created_at DESC);`

//
// BANK
//

type Bank struct {
	db *sql.DB
}

// Transfer moves money between two accounts as one atomic unit.
//
// Four statements must all land or none of them: debit, credit, and two
// ledger-visible effects. A partial success here means money that exists in
// no account, which is the kind of bug nobody can reconcile afterwards.
func (b *Bank) Transfer(ctx context.Context, fromID, toID int64, amount int64) (err error) {
	if amount <= 0 {
		return fmt.Errorf("transfer %d -> %d: %w", fromID, toID, ErrInvalidAmount)
	}

	if fromID == toID {
		return fmt.Errorf("transfer %d -> %d: %w", fromID, toID, ErrSameAccount)
	}

	ctx, cancel := context.WithTimeout(ctx, txTimeout)
	defer cancel()

	// BeginTx takes the context: if the caller's request is cancelled, the
	// transaction is rolled back by database/sql instead of holding locks.
	tx, err := b.db.BeginTx(ctx, &sql.TxOptions{
		// Serializable is SQLite's real behaviour for write transactions.
		// On Postgres this is where you would pick LevelReadCommitted (the
		// default) or LevelSerializable and add retry-on-serialization-error.
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return fmt.Errorf("transfer %d -> %d: begin: %w", fromID, toID, err)
	}

	// The safety net. After a successful Commit this is a no-op returning
	// sql.ErrTxDone, so it costs nothing on the happy path - and on every
	// early return below it is what prevents a leaked open transaction.
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			// Do not overwrite a real error with a rollback error, but never
			// lose it either.
			err = errors.Join(err, fmt.Errorf("rollback: %w", rollbackErr))
		}
	}()

	// Step 1: debit. The WHERE clause carries the business rule, so the check
	// and the write are one statement and cannot race with another writer.
	result, err := tx.ExecContext(
		ctx,
		`UPDATE accounts SET balance = balance - ? WHERE id = ? AND balance >= ?;`,
		amount, fromID, amount,
	)
	if err != nil {
		return fmt.Errorf("transfer %d -> %d: debit: %w", fromID, toID, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("transfer %d -> %d: debit rows: %w", fromID, toID, err)
	}

	if affected == 0 {
		// Either the account does not exist or it cannot cover the amount.
		// Tell those two apart for the caller.
		exists, existsErr := b.accountExists(ctx, tx, fromID)
		if existsErr != nil {
			return fmt.Errorf("transfer %d -> %d: %w", fromID, toID, existsErr)
		}

		if !exists {
			return fmt.Errorf("transfer %d -> %d: source: %w", fromID, toID, ErrAccountNotFound)
		}

		return fmt.Errorf("transfer %d -> %d: %w", fromID, toID, ErrInsufficientFund)
	}

	// Step 2: credit.
	result, err = tx.ExecContext(
		ctx,
		`UPDATE accounts SET balance = balance + ? WHERE id = ?;`,
		amount, toID,
	)
	if err != nil {
		return fmt.Errorf("transfer %d -> %d: credit: %w", fromID, toID, err)
	}

	affected, err = result.RowsAffected()
	if err != nil {
		return fmt.Errorf("transfer %d -> %d: credit rows: %w", fromID, toID, err)
	}

	if affected == 0 {
		// The debit above already happened inside this transaction. Returning
		// here triggers the deferred Rollback, so it is undone.
		return fmt.Errorf("transfer %d -> %d: destination: %w", fromID, toID, ErrAccountNotFound)
	}

	// Step 3: audit trail, in the same unit of work as the money movement.
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO ledger (from_account, to_account, amount) VALUES (?, ?, ?);`,
		fromID, toID, amount,
	); err != nil {
		return fmt.Errorf("transfer %d -> %d: ledger: %w", fromID, toID, err)
	}

	// Nothing is durable until this returns nil.
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("transfer %d -> %d: commit: %w", fromID, toID, err)
	}

	return nil
}

// accountExists runs inside the caller's transaction, so it sees that
// transaction's own uncommitted writes.
func (b *Bank) accountExists(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	var exists int

	err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM accounts WHERE id = ?;`, id).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check account %d: %w", id, err)
	}

	return exists > 0, nil
}

// TransferUnsafe is the anti-pattern kept for contrast: read the balance into
// Go, compute the new value there, write it back. Two goroutines can read the
// same balance before either writes, and one update silently disappears.
func (b *Bank) TransferUnsafe(ctx context.Context, fromID, toID, amount int64) error {
	var fromBalance int64

	if err := b.db.QueryRowContext(ctx, `SELECT balance FROM accounts WHERE id = ?;`, fromID).
		Scan(&fromBalance); err != nil {
		return fmt.Errorf("unsafe transfer: read source: %w", err)
	}

	if fromBalance < amount {
		return ErrInsufficientFund
	}

	var toBalance int64

	if err := b.db.QueryRowContext(ctx, `SELECT balance FROM accounts WHERE id = ?;`, toID).
		Scan(&toBalance); err != nil {
		return fmt.Errorf("unsafe transfer: read destination: %w", err)
	}

	// The gap where another goroutine reads the same stale numbers.
	time.Sleep(time.Millisecond)

	if _, err := b.db.ExecContext(ctx, `UPDATE accounts SET balance = ? WHERE id = ?;`,
		fromBalance-amount, fromID); err != nil {
		return fmt.Errorf("unsafe transfer: write source: %w", err)
	}

	if _, err := b.db.ExecContext(ctx, `UPDATE accounts SET balance = ? WHERE id = ?;`,
		toBalance+amount, toID); err != nil {
		return fmt.Errorf("unsafe transfer: write destination: %w", err)
	}

	return nil
}

func (b *Bank) Balance(ctx context.Context, id int64) (int64, error) {
	var balance int64

	err := b.db.QueryRowContext(ctx, `SELECT balance FROM accounts WHERE id = ?;`, id).Scan(&balance)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, fmt.Errorf("balance of %d: %w: %w", id, ErrAccountNotFound, err)
	case err != nil:
		return 0, fmt.Errorf("balance of %d: %w", id, err)
	}

	return balance, nil
}

func (b *Bank) TotalMoney(ctx context.Context) (int64, error) {
	var total int64

	if err := b.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(balance), 0) FROM accounts;`).
		Scan(&total); err != nil {
		return 0, fmt.Errorf("sum balances: %w", err)
	}

	return total, nil
}

func (b *Bank) LedgerCount(ctx context.Context) (int, error) {
	var count int

	if err := b.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ledger;`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count ledger: %w", err)
	}

	return count, nil
}

//
// SETUP
//

func openDB(ctx context.Context) (*sql.DB, error) {
	path := os.Getenv("DB_PATH")

	if path == "" {
		path = defaultDBPath
	}

	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}

		// Start from a clean database so the demo output is reproducible.
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("reset database file: %w", err)
			}
		}
	}

	// journal_mode=WAL lets readers work while a writer holds the write lock.
	// busy_timeout makes a blocked writer wait instead of failing instantly -
	// SQLite's equivalent of lock_timeout in Postgres.
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close database: %v", closeErr)
		}

		return nil, fmt.Errorf("ping database: %w", err)
	}

	if _, err := db.ExecContext(ctx, schema); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close database: %v", closeErr)
		}

		return nil, fmt.Errorf("create schema: %w", err)
	}

	return db, nil
}

func seedAccounts(ctx context.Context, db *sql.DB) error {
	accounts := []struct {
		owner   string
		balance int64
	}{
		{"ada", 100_00},
		{"alan", 50_00},
		{"grace", 25_00},
	}

	for _, account := range accounts {
		if _, err := db.ExecContext(
			ctx,
			`INSERT INTO accounts (owner, balance) VALUES (?, ?)
			 ON CONFLICT (owner) DO UPDATE SET balance = excluded.balance;`,
			account.owner, account.balance,
		); err != nil {
			return fmt.Errorf("seed account %q: %w", account.owner, err)
		}
	}

	return nil
}

func money(minor int64) string {
	return fmt.Sprintf("%d.%02d", minor/100, minor%100)
}

func printBalances(ctx context.Context, bank *Bank, label string) {
	total, err := bank.TotalMoney(ctx)
	if err != nil {
		log.Fatalf("total: %v", err)
	}

	parts := make([]string, 0, 3)

	for id, name := range map[int64]string{1: "ada", 2: "alan", 3: "grace"} {
		balance, err := bank.Balance(ctx, id)
		if err != nil {
			log.Fatalf("balance: %v", err)
		}

		parts = append(parts, fmt.Sprintf("%s=%s", name, money(balance)))
	}

	// Map iteration order is random; sort so the demo output is stable.
	sortStrings(parts)

	fmt.Printf("  %-22s %s  total=%s\n", label, strings.Join(parts, " "), money(total))
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

//
// MAIN
//

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day44: ")

	ctx := context.Background()

	db, err := openDB(ctx)
	if err != nil {
		log.Fatalf("database unavailable: %v", err)
	}

	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	}()

	if err := seedAccounts(ctx, db); err != nil {
		log.Fatalf("seed: %v", err)
	}

	bank := &Bank{db: db}

	//
	// 1. Committed transfer
	//

	fmt.Println("\n1) Successful transfer (commit)")
	fmt.Println(strings.Repeat("-", 66))

	printBalances(ctx, bank, "before")

	if err := bank.Transfer(ctx, 1, 2, 30_00); err != nil {
		log.Fatalf("transfer: %v", err)
	}

	printBalances(ctx, bank, "after ada -> alan 30.00")

	//
	// 2. Rolled back transfer
	//

	fmt.Println("\n2) Failed transfer (rollback)")
	fmt.Println(strings.Repeat("-", 66))

	err = bank.Transfer(ctx, 3, 1, 999_00)

	if !errors.Is(err, ErrInsufficientFund) {
		log.Fatalf("expected ErrInsufficientFund, got: %v", err)
	}

	fmt.Printf("  rejected: %v\n", err)
	printBalances(ctx, bank, "after failed transfer")

	// A debit that succeeded but whose credit failed must also vanish.
	err = bank.Transfer(ctx, 1, 4242, 10_00)

	if !errors.Is(err, ErrAccountNotFound) {
		log.Fatalf("expected ErrAccountNotFound, got: %v", err)
	}

	fmt.Printf("  rejected: %v\n", err)
	printBalances(ctx, bank, "debit rolled back")

	ledgerCount, err := bank.LedgerCount(ctx)
	if err != nil {
		log.Fatalf("ledger: %v", err)
	}

	fmt.Printf("  ledger rows: %d (only the committed transfer)\n", ledgerCount)

	//
	// 3. Concurrency: the invariant must hold
	//

	fmt.Println("\n3) 50 concurrent transfers")
	fmt.Println(strings.Repeat("-", 66))

	totalBefore, err := bank.TotalMoney(ctx)
	if err != nil {
		log.Fatalf("total: %v", err)
	}

	var (
		waitGroup sync.WaitGroup
		mu        sync.Mutex
		succeeded int
		rejected  int
		failed    []error
	)

	start := time.Now()

	for i := range 50 {
		waitGroup.Add(1)

		go func(i int) {
			defer waitGroup.Done()

			// Alternate direction so both accounts are debited and credited.
			from, to := int64(1), int64(2)
			if i%2 == 1 {
				from, to = 2, 1
			}

			err := bank.Transfer(ctx, from, to, 1_00)

			mu.Lock()
			defer mu.Unlock()

			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrInsufficientFund):
				rejected++
			default:
				failed = append(failed, err)
			}
		}(i)
	}

	waitGroup.Wait()

	if len(failed) > 0 {
		log.Fatalf("unexpected transfer failures (%d), first: %v", len(failed), failed[0])
	}

	totalAfter, err := bank.TotalMoney(ctx)
	if err != nil {
		log.Fatalf("total: %v", err)
	}

	fmt.Printf("  committed=%d rejected=%d in %s\n", succeeded, rejected, time.Since(start).Round(time.Millisecond))
	fmt.Printf("  total before=%s after=%s\n", money(totalBefore), money(totalAfter))

	if totalBefore != totalAfter {
		log.Fatalf("money invariant broken: %d != %d", totalBefore, totalAfter)
	}

	fmt.Println("  invariant holds: no money created or destroyed")

	//
	// 4. Lost update without a transaction
	//

	fmt.Println("\n4) Read-modify-write in Go instead of SQL")
	fmt.Println(strings.Repeat("-", 66))

	if err := seedAccounts(ctx, db); err != nil {
		log.Fatalf("reseed: %v", err)
	}

	adaBefore, err := bank.Balance(ctx, 1)
	if err != nil {
		log.Fatalf("balance: %v", err)
	}

	waitGroup = sync.WaitGroup{}

	for range 10 {
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()

			if err := bank.TransferUnsafe(ctx, 1, 2, 1_00); err != nil {
				log.Printf("unsafe transfer: %v", err)
			}
		}()
	}

	waitGroup.Wait()

	adaAfter, err := bank.Balance(ctx, 1)
	if err != nil {
		log.Fatalf("balance: %v", err)
	}

	const transfers = 10

	expected := adaBefore - transfers*1_00

	fmt.Printf("  %d concurrent unsafe transfers of 1.00 out of ada\n", transfers)
	fmt.Printf("  ada before=%s expected=%s actual=%s\n",
		money(adaBefore), money(expected), money(adaAfter))

	if adaAfter == expected {
		fmt.Println("  no lost update this run - the race is real but timing dependent")
	} else {
		fmt.Printf("  %s never left the account: updates overwrote each other\n",
			money(adaAfter-expected))
	}

	fmt.Println("  the transactional Transfer above cannot do this: the debit is")
	fmt.Println("  a single UPDATE ... SET balance = balance - ? statement")

	//
	// 5. Isolation levels, briefly
	//

	fmt.Println("\n5) Isolation levels (what each one stops)")
	fmt.Println(strings.Repeat("-", 66))

	levels := []struct {
		name    string
		stops   string
		allows  string
		example string
	}{
		{
			"READ UNCOMMITTED", "nothing", "dirty reads",
			"you read a balance another transaction has not committed and may roll back",
		},
		{
			"READ COMMITTED", "dirty reads", "non-repeatable reads, phantoms",
			"the same SELECT inside one transaction returns two different balances (Postgres default)",
		},
		{
			"REPEATABLE READ", "non-repeatable reads", "phantoms (in the standard)",
			"rows you already read stay stable, but new matching rows can appear (MySQL InnoDB default)",
		},
		{
			"SERIALIZABLE", "everything above", "nothing, at the cost of retries",
			"transactions behave as if run one after another; conflicts surface as errors you must retry (SQLite's write behaviour)",
		},
	}

	for _, level := range levels {
		fmt.Printf("  %-17s stops: %-22s allows: %s\n", level.name, level.stops, level.allows)
		fmt.Printf("  %-17s %s\n\n", "", level.example)
	}

	fmt.Println("  Rule of thumb: keep the default level, keep transactions short,")
	fmt.Println("  and put the business rule in the WHERE clause instead of in Go.")
}
