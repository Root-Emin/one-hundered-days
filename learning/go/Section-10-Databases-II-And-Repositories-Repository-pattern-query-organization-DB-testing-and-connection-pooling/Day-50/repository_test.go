package main

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

/*
Integration tests for the data layer.

Every test gets its own database file, created in t.TempDir() and migrated
with the same migrations the binary ships. Nothing here is mocked: these tests
exist to catch the failures a fake cannot - constraint violations, SQL typos,
and transaction boundaries that do not hold.

	go test ./...
	go test -race -run TestPlaceOrder -v ./...
*/

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := OpenDB(t.Context(), DBConfig{
		Path:         filepath.Join(t.TempDir(), "test.db"),
		MaxOpenConns: 4,
		MaxIdleConns: 4,
	})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})

	if err := MigrateUp(t.Context(), db); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	return db
}

func newTestService(t *testing.T) (*ShopService, *sql.DB) {
	t.Helper()

	db := newTestDB(t)

	return NewShopService(RepositoriesFor(db), NewSQLTxManager(db)), db
}

func seedCustomer(t *testing.T, repos Repositories, email string) Customer {
	t.Helper()

	customer, err := repos.Customers.Create(context.Background(), Customer{
		Email: email,
		Name:  "Test Customer",
	})
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	return customer
}

func seedProduct(t *testing.T, repos Repositories, sku string, stock int) Product {
	t.Helper()

	product, err := repos.Products.Create(context.Background(), Product{
		SKU:        sku,
		Name:       "Test Product",
		PriceCents: 1000,
		Stock:      stock,
	})
	if err != nil {
		t.Fatalf("seed product: %v", err)
	}

	return product
}

//
// CUSTOMERS
//

func TestCustomerRepository(t *testing.T) {
	t.Parallel()

	repos := RepositoriesFor(newTestDB(t))
	ctx := context.Background()

	created := seedCustomer(t, repos, "ada@example.com")

	t.Run("by id", func(t *testing.T) {
		found, err := repos.Customers.ByID(ctx, created.ID)
		if err != nil {
			t.Fatalf("by id: %v", err)
		}

		if found.Email != "ada@example.com" {
			t.Fatalf("email = %q", found.Email)
		}
	})

	t.Run("by id not found", func(t *testing.T) {
		if _, err := repos.Customers.ByID(ctx, 999); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("by email", func(t *testing.T) {
		// Lookup normalises case the same way Create does.
		found, err := repos.Customers.ByEmail(ctx, "ADA@example.com")
		if err != nil {
			t.Fatalf("by email: %v", err)
		}

		if found.ID != created.ID {
			t.Fatalf("id = %d, want %d", found.ID, created.ID)
		}
	})

	t.Run("by email not found", func(t *testing.T) {
		if _, err := repos.Customers.ByEmail(ctx, "nobody@example.com"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("duplicate email", func(t *testing.T) {
		_, err := repos.Customers.Create(ctx, Customer{Email: "ada@example.com", Name: "Copy"})

		if !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})

	t.Run("validation", func(t *testing.T) {
		_, err := repos.Customers.Create(ctx, Customer{Email: "not-an-email", Name: "X"})

		if !errors.Is(err, ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("list", func(t *testing.T) {
		customers, err := repos.Customers.List(ctx, 10, 0)
		if err != nil {
			t.Fatalf("list: %v", err)
		}

		if len(customers) != 1 {
			t.Fatalf("list returned %d customers, want 1", len(customers))
		}
	})

	t.Run("delete", func(t *testing.T) {
		if err := repos.Customers.Delete(ctx, created.ID); err != nil {
			t.Fatalf("delete: %v", err)
		}

		if err := repos.Customers.Delete(ctx, created.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("second delete err = %v, want ErrNotFound", err)
		}
	})
}

//
// PRODUCTS
//

func TestProductRepository(t *testing.T) {
	t.Parallel()

	repos := RepositoriesFor(newTestDB(t))
	ctx := context.Background()

	created := seedProduct(t, repos, "kb-01", 5)

	if created.SKU != "KB-01" {
		t.Fatalf("sku = %q, want normalised upper case", created.SKU)
	}

	t.Run("by id and sku", func(t *testing.T) {
		if _, err := repos.Products.ByID(ctx, created.ID); err != nil {
			t.Fatalf("by id: %v", err)
		}

		if _, err := repos.Products.BySKU(ctx, "kb-01"); err != nil {
			t.Fatalf("by sku: %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		if _, err := repos.Products.ByID(ctx, 999); !errors.Is(err, ErrNotFound) {
			t.Fatalf("by id err = %v, want ErrNotFound", err)
		}

		if _, err := repos.Products.BySKU(ctx, "NOPE"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("by sku err = %v, want ErrNotFound", err)
		}
	})

	t.Run("reserve and release", func(t *testing.T) {
		if err := repos.Products.ReserveStock(ctx, created.ID, 3); err != nil {
			t.Fatalf("reserve: %v", err)
		}

		product, err := repos.Products.ByID(ctx, created.ID)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}

		if product.Stock != 2 {
			t.Fatalf("stock = %d, want 2", product.Stock)
		}

		if err := repos.Products.ReleaseStock(ctx, created.ID, 3); err != nil {
			t.Fatalf("release: %v", err)
		}

		if product, err = repos.Products.ByID(ctx, created.ID); err != nil || product.Stock != 5 {
			t.Fatalf("stock after release = %d (err=%v), want 5", product.Stock, err)
		}
	})

	t.Run("reserve more than available", func(t *testing.T) {
		if err := repos.Products.ReserveStock(ctx, created.ID, 99); !errors.Is(err, ErrOutOfStock) {
			t.Fatalf("err = %v, want ErrOutOfStock", err)
		}
	})

	t.Run("reserve missing product", func(t *testing.T) {
		if err := repos.Products.ReserveStock(ctx, 999, 1); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("duplicate sku", func(t *testing.T) {
		_, err := repos.Products.Create(ctx, Product{SKU: "KB-01", Name: "Copy", PriceCents: 100})

		if !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})
}

//
// ORDERS
//

func TestOrderRepository(t *testing.T) {
	t.Parallel()

	repos := RepositoriesFor(newTestDB(t))
	ctx := context.Background()

	customer := seedCustomer(t, repos, "orders@example.com")
	product := seedProduct(t, repos, "sku-1", 10)

	created, err := repos.Orders.Create(ctx, Order{
		CustomerID: customer.ID,
		TotalCents: 2000,
		Items: []OrderItem{
			{ProductID: product.ID, Quantity: 2, UnitCents: 1000},
		},
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	t.Run("items are loaded with the order", func(t *testing.T) {
		found, err := repos.Orders.ByID(ctx, created.ID)
		if err != nil {
			t.Fatalf("by id: %v", err)
		}

		if len(found.Items) != 1 || found.Items[0].Quantity != 2 {
			t.Fatalf("items = %+v", found.Items)
		}

		if found.Status != StatusPlaced {
			t.Fatalf("status = %q, want placed", found.Status)
		}
	})

	t.Run("not found", func(t *testing.T) {
		if _, err := repos.Orders.ByID(ctx, 999); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("list by customer", func(t *testing.T) {
		orders, err := repos.Orders.ListByCustomer(ctx, customer.ID, 10, 0)
		if err != nil {
			t.Fatalf("list: %v", err)
		}

		if len(orders) != 1 || len(orders[0].Items) != 1 {
			t.Fatalf("orders = %+v", orders)
		}
	})

	t.Run("empty order is rejected", func(t *testing.T) {
		_, err := repos.Orders.Create(ctx, Order{CustomerID: customer.ID})

		if !errors.Is(err, ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("unknown customer", func(t *testing.T) {
		_, err := repos.Orders.Create(ctx, Order{
			CustomerID: 999,
			Items:      []OrderItem{{ProductID: product.ID, Quantity: 1, UnitCents: 100}},
		})

		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("status transitions", func(t *testing.T) {
		if err := repos.Orders.UpdateStatus(ctx, created.ID, StatusShipped); err != nil {
			t.Fatalf("update status: %v", err)
		}

		if err := repos.Orders.UpdateStatus(ctx, 999, StatusShipped); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing order err = %v, want ErrNotFound", err)
		}

		if err := repos.Orders.UpdateStatus(ctx, created.ID, "teleported"); !errors.Is(err, ErrValidation) {
			t.Fatalf("bad status err = %v, want ErrValidation", err)
		}
	})
}

//
// SERVICE / TRANSACTIONS
//

func TestPlaceOrderIsAtomic(t *testing.T) {
	t.Parallel()

	shop, db := newTestService(t)
	repos := RepositoriesFor(db)
	ctx := context.Background()

	customer := seedCustomer(t, repos, "atomic@example.com")
	cheap := seedProduct(t, repos, "cheap", 10)
	scarce := seedProduct(t, repos, "scarce", 1)

	// The first line succeeds, the second cannot: nothing may survive.
	_, err := shop.PlaceOrder(ctx, customer.ID, []OrderLine{
		{ProductID: cheap.ID, Quantity: 2},
		{ProductID: scarce.ID, Quantity: 5},
	})

	if !errors.Is(err, ErrOutOfStock) {
		t.Fatalf("err = %v, want ErrOutOfStock", err)
	}

	product, err := repos.Products.ByID(ctx, cheap.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if product.Stock != 10 {
		t.Fatalf("stock = %d, want 10 - the successful reservation was not rolled back", product.Stock)
	}

	orders, err := repos.Orders.ListByCustomer(ctx, customer.ID, 10, 0)
	if err != nil {
		t.Fatalf("list orders: %v", err)
	}

	if len(orders) != 0 {
		t.Fatalf("orders = %d, want 0 - a failed order was persisted", len(orders))
	}
}

func TestPlaceOrderMergesDuplicateLines(t *testing.T) {
	t.Parallel()

	shop, db := newTestService(t)
	repos := RepositoriesFor(db)
	ctx := context.Background()

	customer := seedCustomer(t, repos, "merge@example.com")
	product := seedProduct(t, repos, "limited", 5)

	// Two lines of 3 = 6 > 5. Checking each line separately would let this
	// through and oversell by one.
	_, err := shop.PlaceOrder(ctx, customer.ID, []OrderLine{
		{ProductID: product.ID, Quantity: 3},
		{ProductID: product.ID, Quantity: 3},
	})

	if !errors.Is(err, ErrOutOfStock) {
		t.Fatalf("err = %v, want ErrOutOfStock", err)
	}
}

func TestPlaceOrderUsesCatalogPrices(t *testing.T) {
	t.Parallel()

	shop, db := newTestService(t)
	repos := RepositoriesFor(db)
	ctx := context.Background()

	customer := seedCustomer(t, repos, "price@example.com")
	product := seedProduct(t, repos, "priced", 5) // 1000 cents

	order, err := shop.PlaceOrder(ctx, customer.ID, []OrderLine{{ProductID: product.ID, Quantity: 2}})
	if err != nil {
		t.Fatalf("place order: %v", err)
	}

	if order.TotalCents != 2000 {
		t.Fatalf("total = %d, want 2000", order.TotalCents)
	}

	if order.Items[0].UnitCents != 1000 {
		t.Fatalf("unit price = %d, want the catalog price", order.Items[0].UnitCents)
	}
}

func TestCancelOrderReturnsStock(t *testing.T) {
	t.Parallel()

	shop, db := newTestService(t)
	repos := RepositoriesFor(db)
	ctx := context.Background()

	customer := seedCustomer(t, repos, "cancel@example.com")
	product := seedProduct(t, repos, "returnable", 5)

	order, err := shop.PlaceOrder(ctx, customer.ID, []OrderLine{{ProductID: product.ID, Quantity: 3}})
	if err != nil {
		t.Fatalf("place order: %v", err)
	}

	if _, err := shop.CancelOrder(ctx, order.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	product, err = repos.Products.ByID(ctx, product.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if product.Stock != 5 {
		t.Fatalf("stock = %d, want 5", product.Stock)
	}

	if _, err := shop.CancelOrder(ctx, order.ID); !errors.Is(err, ErrValidation) {
		t.Fatalf("double cancel err = %v, want ErrValidation", err)
	}
}

// TestConcurrentOrdersCannotOversell is the test that only a real database can
// pass: the stock rule lives in an UPDATE ... WHERE stock >= ?, and this
// proves it holds under concurrency.
func TestConcurrentOrdersCannotOversell(t *testing.T) {
	t.Parallel()

	shop, db := newTestService(t)
	repos := RepositoriesFor(db)
	ctx := context.Background()

	customer := seedCustomer(t, repos, "race@example.com")
	product := seedProduct(t, repos, "hot-item", 10)

	var (
		waitGroup sync.WaitGroup
		mu        sync.Mutex
		succeeded int
	)

	// 20 buyers, 10 units, 1 unit each: exactly 10 may win.
	for range 20 {
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()

			_, err := shop.PlaceOrder(ctx, customer.ID, []OrderLine{{ProductID: product.ID, Quantity: 1}})

			mu.Lock()
			defer mu.Unlock()

			if err == nil {
				succeeded++
			}
		}()
	}

	waitGroup.Wait()

	if succeeded != 10 {
		t.Fatalf("%d orders succeeded, want exactly 10", succeeded)
	}

	product, err := repos.Products.ByID(ctx, product.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if product.Stock != 0 {
		t.Fatalf("stock = %d, want 0", product.Stock)
	}
}

func TestMigrationsAreReversible(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `SELECT 1 FROM orders LIMIT 1;`); err != nil {
		t.Fatalf("orders table missing: %v", err)
	}

	if err := MigrateDown(ctx, db); err != nil {
		t.Fatalf("down: %v", err)
	}

	if _, err := db.ExecContext(ctx, `SELECT 1 FROM orders LIMIT 1;`); err == nil {
		t.Fatal("orders table still exists after down")
	}

	if err := MigrateUp(ctx, db); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
