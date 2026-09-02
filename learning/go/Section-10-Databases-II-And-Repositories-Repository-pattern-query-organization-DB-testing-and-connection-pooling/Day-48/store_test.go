package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

//
// STRATEGY 1: a disposable database per test, safe to run in parallel
//

func TestAccountLifecycle(t *testing.T) {
	t.Parallel() // safe: this test owns its database

	store := NewStore(newIsolatedDB(t))
	ctx := context.Background()

	created, err := store.CreateAccount(ctx, "Ada@Example.com ", "pro")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if created.Email != "ada@example.com" {
		t.Fatalf("email = %q, want normalised lower case", created.Email)
	}

	t.Run("read back", func(t *testing.T) {
		found, err := store.AccountByID(ctx, created.ID)
		if err != nil {
			t.Fatalf("by id: %v", err)
		}

		if found.Plan != "pro" {
			t.Fatalf("plan = %q, want pro", found.Plan)
		}
	})

	t.Run("not found", func(t *testing.T) {
		if _, err := store.AccountByID(ctx, 999); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("duplicate email", func(t *testing.T) {
		// This case exists because a fake repository would never catch it:
		// the uniqueness rule lives in the database, so only a real database
		// can prove it is enforced.
		if _, err := store.CreateAccount(ctx, "ada@example.com", "free"); !errors.Is(err, ErrDuplicate) {
			t.Fatalf("err = %v, want ErrDuplicate", err)
		}
	})
}

func TestInvoiceFlow(t *testing.T) {
	t.Parallel()

	store := NewStore(newIsolatedDB(t))
	ctx := context.Background()

	account, err := store.CreateAccount(ctx, "grace@example.com", "free")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	first, err := store.CreateInvoice(ctx, account.ID, 2500)
	if err != nil {
		t.Fatalf("create invoice: %v", err)
	}

	if _, err := store.CreateInvoice(ctx, account.ID, 1000); err != nil {
		t.Fatalf("create second invoice: %v", err)
	}

	unpaid, err := store.UnpaidTotal(ctx, account.ID)
	if err != nil {
		t.Fatalf("unpaid total: %v", err)
	}

	if unpaid != 3500 {
		t.Fatalf("unpaid = %d, want 3500", unpaid)
	}

	if err := store.MarkInvoicePaid(ctx, first.ID); err != nil {
		t.Fatalf("mark paid: %v", err)
	}

	if unpaid, err = store.UnpaidTotal(ctx, account.ID); err != nil || unpaid != 1000 {
		t.Fatalf("unpaid after payment = %d (err=%v), want 1000", unpaid, err)
	}

	t.Run("foreign key is enforced", func(t *testing.T) {
		// Another database-only rule: no invoice without an account.
		if _, err := store.CreateInvoice(ctx, 4242, 100); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("missing invoice", func(t *testing.T) {
		if err := store.MarkInvoicePaid(ctx, 4242); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

// TestValidationTable is the table-driven shape for the boring cases: cheap,
// exhaustive, and readable as a specification.
func TestValidationTable(t *testing.T) {
	t.Parallel()

	store := NewStore(newIsolatedDB(t))
	ctx := context.Background()

	account, err := store.CreateAccount(ctx, "table@example.com", "free")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	tests := []struct {
		name    string
		email   string
		amount  int64
		wantErr bool
	}{
		{"valid amount", "", 100, false},
		{"zero amount", "", 0, true},
		{"negative amount", "", -50, true},
		{"empty email", "", 0, true},
		{"malformed email", "not-an-email", 0, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var err error

			if test.email != "" || test.amount == 0 {
				_, err = store.CreateAccount(ctx, test.email, "free")
			} else {
				_, err = store.CreateInvoice(ctx, account.ID, test.amount)
			}

			if gotErr := err != nil; gotErr != test.wantErr {
				t.Fatalf("err = %v, wantErr = %t", err, test.wantErr)
			}
		})
	}
}

//
// STRATEGY 2: one database, a transaction per test, always rolled back
//

func TestWithRollbackIsolation(t *testing.T) {
	db := newIsolatedDB(t)

	// Each subtest writes the same email. Without the rollback, the second
	// one would fail on the unique index - which is exactly the assertion.
	for _, name := range []string{"first", "second", "third"} {
		t.Run(name, func(t *testing.T) {
			withRollback(t, db, func(store *Store) {
				ctx := context.Background()

				if _, err := store.CreateAccount(ctx, "same@example.com", "pro"); err != nil {
					t.Fatalf("create: %v", err)
				}

				count, err := store.CountAccounts(ctx)
				if err != nil {
					t.Fatalf("count: %v", err)
				}

				// Always 1: the previous subtest's row was rolled back.
				if count != 1 {
					t.Fatalf("accounts = %d, want 1 - state leaked between tests", count)
				}
			})
		})
	}

	// After all of it, the database is untouched.
	count, err := NewStore(db).CountAccounts(context.Background())
	if err != nil {
		t.Fatalf("count: %v", err)
	}

	if count != 0 {
		t.Fatalf("accounts left behind = %d, want 0", count)
	}
}

//
// STRATEGY 3: a shared database, truncated between tests
//
// These deliberately do NOT call t.Parallel. Truncating a database another
// running test is reading from is the classic source of order-dependent,
// "only fails in CI" flakes.
//

func TestSharedDatabaseTruncate(t *testing.T) {
	db := openSharedDB(t)

	for i := range 3 {
		t.Run(fmt.Sprintf("run-%d", i), func(t *testing.T) {
			resetShared(t, db)

			store := NewStore(db)
			ctx := context.Background()

			account, err := store.CreateAccount(ctx, "shared@example.com", "free")
			if err != nil {
				t.Fatalf("create: %v", err)
			}

			// Ids restart at 1 because Truncate resets the sequence, so this
			// assertion does not depend on how many tests ran before.
			if account.ID != 1 {
				t.Fatalf("id = %d, want 1 - sequence not reset", account.ID)
			}
		})
	}
}

func TestSharedDatabaseSeesOnlyItsOwnRows(t *testing.T) {
	db := openSharedDB(t)
	resetShared(t, db)

	store := NewStore(db)
	ctx := context.Background()

	if _, err := store.CreateAccount(ctx, "only@example.com", "free"); err != nil {
		t.Fatalf("create: %v", err)
	}

	count, err := store.CountAccounts(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}

	if count != 1 {
		t.Fatalf("accounts = %d, want 1 - another test left rows behind", count)
	}
}
