package store_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-98/internal/domain"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-98/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()

	dataStore, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() {
		if err := dataStore.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	if _, err := store.Migrate(t.Context(), dataStore.DB()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	return dataStore
}

func newLink(t *testing.T, code, owner string) domain.Link {
	t.Helper()

	parsed, err := domain.ParseCode(code)
	if err != nil {
		t.Fatalf("ParseCode: %v", err)
	}

	link, err := domain.NewLink(parsed, owner, "https://example.com", time.Time{},
		time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}

	return link
}

func TestMigrationsAreIdempotent(t *testing.T) {
	dataStore := newStore(t)

	// The first Migrate ran in newStore; a second must do nothing, which is
	// what makes "migrate on startup" safe.
	applied, err := store.Migrate(t.Context(), dataStore.DB())
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if len(applied) != 0 {
		t.Errorf("the second Migrate applied %d migrations, want 0", len(applied))
	}
}

func TestMigrationsHaveDownScripts(t *testing.T) {
	migrations, err := store.LoadMigrations()
	if err != nil {
		t.Fatalf("LoadMigrations: %v", err)
	}

	if len(migrations) == 0 {
		t.Fatal("no migrations found - the embed is not working")
	}

	for _, migration := range migrations {
		if migration.Down == "" {
			t.Errorf("migration %d (%s) has no down script: a deploy with no way back",
				migration.Version, migration.Name)
		}
	}
}

func TestCreateAndReadLink(t *testing.T) {
	dataStore := newStore(t)

	link := newLink(t, "abc1234", "ada")

	if err := dataStore.CreateLink(t.Context(), link); err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	stored, err := dataStore.Link(t.Context(), link.Code)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}

	if stored.Target != link.Target || stored.Owner != "ada" || !stored.Active {
		t.Errorf("stored = %+v, want %+v", stored, link)
	}

	// Times survive the round trip through TEXT columns.
	if !stored.CreatedAt.Equal(link.CreatedAt) {
		t.Errorf("created at = %s, want %s", stored.CreatedAt, link.CreatedAt)
	}
}

func TestExpiryRoundTrips(t *testing.T) {
	dataStore := newStore(t)

	expires := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	link := newLink(t, "abc1234", "ada")
	link.ExpiresAt = expires

	if err := dataStore.CreateLink(t.Context(), link); err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	stored, err := dataStore.Link(t.Context(), link.Code)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}

	if !stored.ExpiresAt.Equal(expires) {
		t.Errorf("expires at = %s, want %s", stored.ExpiresAt, expires)
	}

	// And a link with no expiry comes back with the zero time, not an error.
	plain := newLink(t, "xyz9876", "ada")

	if err := dataStore.CreateLink(t.Context(), plain); err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	storedPlain, err := dataStore.Link(t.Context(), plain.Code)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}

	if !storedPlain.ExpiresAt.IsZero() {
		t.Errorf("expires at = %s, want the zero time", storedPlain.ExpiresAt)
	}
}

func TestDuplicateCodeIsTaken(t *testing.T) {
	dataStore := newStore(t)

	link := newLink(t, "abc1234", "ada")

	if err := dataStore.CreateLink(t.Context(), link); err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	other := newLink(t, "abc1234", "grace")

	if err := dataStore.CreateLink(t.Context(), other); !errors.Is(err, domain.ErrCodeTaken) {
		t.Errorf("duplicate CreateLink = %v, want ErrCodeTaken", err)
	}
}

func TestMissingLinkIsNotFound(t *testing.T) {
	if _, err := newStore(t).Link(t.Context(), domain.Code("nothing")); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Link(missing) = %v, want ErrNotFound", err)
	}
}

func TestLinksByOwnerIsScopedAndOrdered(t *testing.T) {
	dataStore := newStore(t)

	base := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	for i, code := range []string{"aaa1111", "bbb2222", "ccc3333"} {
		link := newLink(t, code, "ada")
		link.CreatedAt = base.Add(time.Duration(i) * time.Hour)

		if err := dataStore.CreateLink(t.Context(), link); err != nil {
			t.Fatalf("CreateLink: %v", err)
		}
	}

	if err := dataStore.CreateLink(t.Context(), newLink(t, "ddd4444", "grace")); err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	links, err := dataStore.LinksByOwner(t.Context(), "ada", 10)
	if err != nil {
		t.Fatalf("LinksByOwner: %v", err)
	}

	if len(links) != 3 {
		t.Fatalf("links = %d, want 3 (grace's link leaked in)", len(links))
	}

	// Newest first.
	if links[0].Code != "ccc3333" {
		t.Errorf("first = %s, want ccc3333", links[0].Code)
	}
}

// Not found and not-yours are the same answer: telling a caller that a code
// exists but belongs to someone else is an enumeration oracle.
func TestDeactivateRefusesAnotherOwnersLink(t *testing.T) {
	dataStore := newStore(t)

	if err := dataStore.CreateLink(t.Context(), newLink(t, "abc1234", "ada")); err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	if err := dataStore.DeactivateLink(t.Context(), "grace", domain.Code("abc1234")); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("DeactivateLink as the wrong owner = %v, want ErrNotFound", err)
	}

	// And the link is untouched.
	link, err := dataStore.Link(t.Context(), domain.Code("abc1234"))
	if err != nil {
		t.Fatalf("Link: %v", err)
	}

	if !link.Active {
		t.Error("another owner's deactivate went through")
	}
}

// The row stays and the code is never reused: a deleted row would let a new
// link inherit an old one's traffic.
func TestDeactivateKeepsTheRow(t *testing.T) {
	dataStore := newStore(t)

	if err := dataStore.CreateLink(t.Context(), newLink(t, "abc1234", "ada")); err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	if err := dataStore.DeactivateLink(t.Context(), "ada", domain.Code("abc1234")); err != nil {
		t.Fatalf("DeactivateLink: %v", err)
	}

	link, err := dataStore.Link(t.Context(), domain.Code("abc1234"))
	if err != nil {
		t.Fatalf("the row was deleted rather than deactivated: %v", err)
	}

	if link.Active {
		t.Error("the link is still active")
	}

	// And the code cannot be taken by someone else.
	if err := dataStore.CreateLink(t.Context(), newLink(t, "abc1234", "grace")); !errors.Is(err, domain.ErrCodeTaken) {
		t.Errorf("a deactivated code was reusable: %v", err)
	}
}

//
// API KEYS
//

func TestAPIKeyLookup(t *testing.T) {
	dataStore := newStore(t)

	key := store.APIKey{ID: "id-1", Owner: "ada", Hash: "hash-1", CreatedAt: time.Now().UTC()}

	if err := dataStore.CreateAPIKey(t.Context(), key); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	owner, err := dataStore.OwnerForHash(t.Context(), "hash-1")
	if err != nil {
		t.Fatalf("OwnerForHash: %v", err)
	}

	if owner != "ada" {
		t.Errorf("owner = %q, want ada", owner)
	}

	if _, err := dataStore.OwnerForHash(t.Context(), "unknown"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("OwnerForHash(unknown) = %v, want ErrUnauthorized", err)
	}
}

//
// CLICKS AND THE OUTBOX
//

// The click and its outbox event are one transaction: an event with no click
// is a phantom in the aggregate, and a click with no event never reaches
// click_daily.
func TestRecordClickWritesBothRows(t *testing.T) {
	dataStore := newStore(t)

	if err := dataStore.CreateLink(t.Context(), newLink(t, "abc1234", "ada")); err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	click := domain.Click{
		Code:       domain.Code("abc1234"),
		OccurredAt: time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC),
		Referrer:   "https://news.example.com",
		UserAgent:  "curl/8",
	}

	if err := dataStore.RecordClick(t.Context(), click); err != nil {
		t.Fatalf("RecordClick: %v", err)
	}

	count, err := dataStore.ClickCount(t.Context(), click.Code)
	if err != nil {
		t.Fatalf("ClickCount: %v", err)
	}

	if count != 1 {
		t.Errorf("clicks = %d, want 1", count)
	}

	pending, err := dataStore.PendingEvents(t.Context())
	if err != nil {
		t.Fatalf("PendingEvents: %v", err)
	}

	if pending != 1 {
		t.Errorf("pending events = %d, want 1", pending)
	}
}

func TestCheckReportsAClosedDatabase(t *testing.T) {
	dataStore := newStore(t)

	if err := dataStore.Check(t.Context()); err != nil {
		t.Errorf("Check on an open store = %v, want nil", err)
	}

	if err := dataStore.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := dataStore.Check(t.Context()); err == nil {
		t.Error("Check on a closed store returned nil")
	}

	// Close is called again by the cleanup; a second close must not panic.
}
