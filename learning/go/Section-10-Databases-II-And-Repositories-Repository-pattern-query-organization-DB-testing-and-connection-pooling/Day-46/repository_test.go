package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

/*
One contract test suite, executed against every ProductRepository
implementation.

This is what interfaces buy you: the fake is not "the version that is allowed
to be wrong". If the in-memory repository and the SQL repository disagree
about not-found, uniqueness or overselling, this file fails - which means the
fake stays a trustworthy stand-in for the real thing in other tests.
*/

func newSQLRepositoryForTest(t *testing.T) *SQLProductRepository {
	t.Helper()

	db, err := openProductDB(t.Context(), ":memory:")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})

	return NewSQLProductRepository(db)
}

func repositoryImplementations(t *testing.T) map[string]ProductRepository {
	t.Helper()

	return map[string]ProductRepository{
		"memory": NewMemoryProductRepository(),
		"sqlite": newSQLRepositoryForTest(t),
	}
}

func sampleProduct() Product {
	return Product{
		SKU:       "KB-01",
		Name:      "Mechanical Keyboard",
		PriceCent: 129_00,
		CostCents: 71_00,
		Stock:     10,
	}
}

func TestRepositoryContract(t *testing.T) {
	for name, repository := range repositoryImplementations(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()

			created, err := repository.Create(ctx, sampleProduct())
			if err != nil {
				t.Fatalf("create: %v", err)
			}

			if created.ID == 0 {
				t.Fatal("create did not assign an id")
			}

			if created.CreatedAt.IsZero() {
				t.Fatal("create did not set created_at")
			}

			t.Run("read back by id", func(t *testing.T) {
				found, err := repository.ByID(ctx, created.ID)
				if err != nil {
					t.Fatalf("by id: %v", err)
				}

				if found.SKU != created.SKU || found.Stock != created.Stock {
					t.Fatalf("round trip mismatch: %+v vs %+v", found, created)
				}
			})

			t.Run("read back by sku", func(t *testing.T) {
				found, err := repository.BySKU(ctx, "KB-01")
				if err != nil {
					t.Fatalf("by sku: %v", err)
				}

				if found.ID != created.ID {
					t.Fatalf("by sku returned id %d, want %d", found.ID, created.ID)
				}
			})

			t.Run("missing id is ErrNotFound", func(t *testing.T) {
				if _, err := repository.ByID(ctx, 999_999); !errors.Is(err, ErrNotFound) {
					t.Fatalf("err = %v, want ErrNotFound", err)
				}
			})

			t.Run("missing sku is ErrNotFound", func(t *testing.T) {
				if _, err := repository.BySKU(ctx, "NOPE"); !errors.Is(err, ErrNotFound) {
					t.Fatalf("err = %v, want ErrNotFound", err)
				}
			})

			t.Run("duplicate sku is ErrConflict", func(t *testing.T) {
				if _, err := repository.Create(ctx, sampleProduct()); !errors.Is(err, ErrConflict) {
					t.Fatalf("err = %v, want ErrConflict", err)
				}
			})

			t.Run("reserve stock", func(t *testing.T) {
				updated, err := repository.ReserveStock(ctx, created.ID, 3)
				if err != nil {
					t.Fatalf("reserve: %v", err)
				}

				if updated.Stock != 7 {
					t.Fatalf("stock = %d, want 7", updated.Stock)
				}
			})

			t.Run("cannot oversell", func(t *testing.T) {
				if _, err := repository.ReserveStock(ctx, created.ID, 100); !errors.Is(err, ErrValidation) {
					t.Fatalf("err = %v, want ErrValidation", err)
				}
			})

			t.Run("list respects the in-stock filter", func(t *testing.T) {
				soldOut := Product{SKU: "HP-03", Name: "Headphones", PriceCent: 199_00, Stock: 0}

				if _, err := repository.Create(ctx, soldOut); err != nil {
					t.Fatalf("create sold-out product: %v", err)
				}

				all, err := repository.List(ctx, ProductFilter{Limit: 20})
				if err != nil {
					t.Fatalf("list all: %v", err)
				}

				if len(all) != 2 {
					t.Fatalf("list all returned %d products, want 2", len(all))
				}

				inStock, err := repository.List(ctx, ProductFilter{InStockOnly: true, Limit: 20})
				if err != nil {
					t.Fatalf("list in stock: %v", err)
				}

				if len(inStock) != 1 || inStock[0].SKU != "KB-01" {
					t.Fatalf("in-stock list = %+v, want only KB-01", inStock)
				}
			})

			t.Run("delete", func(t *testing.T) {
				if err := repository.Delete(ctx, created.ID); err != nil {
					t.Fatalf("delete: %v", err)
				}

				if _, err := repository.ByID(ctx, created.ID); !errors.Is(err, ErrNotFound) {
					t.Fatalf("after delete err = %v, want ErrNotFound", err)
				}

				if err := repository.Delete(ctx, created.ID); !errors.Is(err, ErrNotFound) {
					t.Fatalf("second delete err = %v, want ErrNotFound", err)
				}
			})
		})
	}
}

// TestServiceRulesWithFake shows the payoff: service rules are tested at
// memory speed, with no database in the picture at all.
func TestServiceRulesWithFake(t *testing.T) {
	catalog := NewCatalogService(NewMemoryProductRepository())
	ctx := context.Background()

	if _, err := catalog.AddProduct(ctx, sampleProduct()); err != nil {
		t.Fatalf("add: %v", err)
	}

	t.Run("sku is normalised to upper case", func(t *testing.T) {
		if _, err := catalog.AddProduct(ctx, Product{
			SKU: "  kb-01 ", Name: "Same product", PriceCent: 100,
		}); !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})

	t.Run("quantity must be positive", func(t *testing.T) {
		if _, err := catalog.Sell(ctx, 1, 0); !errors.Is(err, ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})
}

// TestHandlerUsesInjectedRepository drives the HTTP layer with the fake, and
// checks the mapping rule that matters most: internal cost never ships.
func TestHandlerUsesInjectedRepository(t *testing.T) {
	handler := NewProductHandler(NewCatalogService(NewMemoryProductRepository()))
	server := httptest.NewServer(handler.Routes())

	t.Cleanup(server.Close)

	body := strings.NewReader(`{"sku":"kb-01","name":"Keyboard","price_cents":12900,"cost_cents":7100,"stock":4}`)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/products", body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("post product: %v", err)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	var payload map[string]any

	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if _, leaked := payload["cost_cents"]; leaked {
		t.Fatal("cost_cents leaked into the API response")
	}

	if payload["price"] != "129.00" {
		t.Fatalf("price = %v, want \"129.00\"", payload["price"])
	}

	if payload["id"] != "1" {
		t.Fatalf("id = %v, want string \"1\"", payload["id"])
	}
}
