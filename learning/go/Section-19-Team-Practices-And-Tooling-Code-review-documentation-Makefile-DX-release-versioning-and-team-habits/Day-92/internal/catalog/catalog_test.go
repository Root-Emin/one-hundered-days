package catalog_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-92/internal/catalog"
)

func newStore(t *testing.T) *catalog.Store {
	t.Helper()

	store := catalog.New()

	fixed := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return fixed })

	return store
}

func product(sku string, stock int) catalog.Product {
	return catalog.Product{SKU: sku, Name: "Keyboard", PriceCent: 12000, Stock: stock}
}

func TestAddAndGet(t *testing.T) {
	store := newStore(t)

	added, err := store.Add(product("KB-01", 5))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if added.UpdatedAt.IsZero() {
		t.Error("UpdatedAt was not set")
	}

	fetched, err := store.Get("KB-01")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if fetched != added {
		t.Errorf("fetched = %+v, want %+v", fetched, added)
	}
}

// The sentinels are the API; the message text is not. A caller switching on
// strings would break on the next release, which is why they use errors.Is.
func TestSentinelErrors(t *testing.T) {
	store := newStore(t)

	if _, err := store.Get("nope"); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("Get(missing) = %v, want ErrNotFound", err)
	}

	if _, err := store.Add(product("KB-01", 1)); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if _, err := store.Add(product("KB-01", 1)); !errors.Is(err, catalog.ErrDuplicateSKU) {
		t.Errorf("duplicate Add = %v, want ErrDuplicateSKU", err)
	}

	if _, err := store.Reserve("KB-01", 5); !errors.Is(err, catalog.ErrInsufficientStock) {
		t.Errorf("over-reserve = %v, want ErrInsufficientStock", err)
	}

	if err := store.Delete("nope"); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("Delete(missing) = %v, want ErrNotFound", err)
	}
}

func TestValidation(t *testing.T) {
	store := newStore(t)

	cases := map[string]catalog.Product{
		"empty sku":      {SKU: "", Name: "x", PriceCent: 1, Stock: 0},
		"empty name":     {SKU: "x", Name: " ", PriceCent: 1, Stock: 0},
		"zero price":     {SKU: "x", Name: "x", PriceCent: 0, Stock: 0},
		"negative price": {SKU: "x", Name: "x", PriceCent: -1, Stock: 0},
		"negative stock": {SKU: "x", Name: "x", PriceCent: 1, Stock: -1},
	}

	for name, invalid := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := store.Add(invalid); !errors.Is(err, catalog.ErrInvalidProduct) {
				t.Errorf("Add(%+v) = %v, want ErrInvalidProduct", invalid, err)
			}
		})
	}
}

// The invariant the package exists for: stock never goes negative, and a
// failed reservation changes nothing.
func TestFailedReservationChangesNothing(t *testing.T) {
	store := newStore(t)

	if _, err := store.Add(product("KB-01", 2)); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if _, err := store.Reserve("KB-01", 3); !errors.Is(err, catalog.ErrInsufficientStock) {
		t.Fatalf("Reserve = %v, want ErrInsufficientStock", err)
	}

	after, err := store.Get("KB-01")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if after.Stock != 2 {
		t.Errorf("stock = %d, want 2", after.Stock)
	}
}

func TestReserveRejectsNonPositiveQuantities(t *testing.T) {
	store := newStore(t)

	if _, err := store.Add(product("KB-01", 5)); err != nil {
		t.Fatalf("Add: %v", err)
	}

	for _, quantity := range []int{0, -1} {
		if _, err := store.Reserve("KB-01", quantity); !errors.Is(err, catalog.ErrInvalidProduct) {
			t.Errorf("Reserve(%d) = %v, want ErrInvalidProduct", quantity, err)
		}
	}
}

// Concurrent reservations must not oversell: with 100 units and 200 attempts
// of one unit each, exactly 100 succeed.
func TestConcurrentReservationsDoNotOversell(t *testing.T) {
	store := newStore(t)

	if _, err := store.Add(product("KB-01", 100)); err != nil {
		t.Fatalf("Add: %v", err)
	}

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
	)

	for i := 0; i < 200; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			if _, err := store.Reserve("KB-01", 1); err == nil {
				mu.Lock()
				succeeded++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if succeeded != 100 {
		t.Errorf("%d reservations succeeded, want exactly 100", succeeded)
	}

	final, err := store.Get("KB-01")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if final.Stock != 0 {
		t.Errorf("final stock = %d, want 0", final.Stock)
	}
}

func TestListIsOrderedAndComplete(t *testing.T) {
	store := newStore(t)

	for _, sku := range []string{"MS-02", "KB-01", "SC-03"} {
		if _, err := store.Add(product(sku, 1)); err != nil {
			t.Fatalf("Add(%s): %v", sku, err)
		}
	}

	products := store.List()

	if len(products) != 3 || store.Len() != 3 {
		t.Fatalf("List = %d, Len = %d, want 3", len(products), store.Len())
	}

	for i := 1; i < len(products); i++ {
		if products[i-1].SKU > products[i].SKU {
			t.Fatalf("out of order: %s before %s", products[i-1].SKU, products[i].SKU)
		}
	}
}

func TestDeleteRemoves(t *testing.T) {
	store := newStore(t)

	if _, err := store.Add(product("KB-01", 1)); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := store.Delete("KB-01"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if store.Len() != 0 {
		t.Errorf("Len = %d, want 0", store.Len())
	}
}
