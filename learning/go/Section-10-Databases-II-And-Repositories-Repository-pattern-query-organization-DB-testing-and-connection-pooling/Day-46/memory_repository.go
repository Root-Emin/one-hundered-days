package main

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

/*
An in-memory ProductRepository.

Two uses:
  - tests that exercise service rules without paying for a database
  - proof that the interface really is storage agnostic: main.go runs the same
    scenario against this and against SQLite, and the output is identical

It is a fake, not a mock: it behaves like the real thing (it enforces the same
uniqueness and stock rules) instead of asserting on calls.
*/

type MemoryProductRepository struct {
	mu       sync.RWMutex
	products map[int64]Product
	nextID   int64
}

func NewMemoryProductRepository() *MemoryProductRepository {
	return &MemoryProductRepository{
		products: make(map[int64]Product),
		nextID:   1,
	}
}

var _ ProductRepository = (*MemoryProductRepository)(nil)

func (r *MemoryProductRepository) Create(ctx context.Context, product Product) (Product, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, existing := range r.products {
		if existing.SKU == product.SKU {
			return Product{}, fmt.Errorf("create product %s: %w", product.SKU, ErrConflict)
		}
	}

	now := time.Now().UTC().Truncate(time.Second)

	product.ID = r.nextID
	product.CreatedAt = now
	product.UpdatedAt = now

	r.products[product.ID] = product
	r.nextID++

	return product, nil
}

func (r *MemoryProductRepository) ByID(ctx context.Context, id int64) (Product, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	product, ok := r.products[id]
	if !ok {
		return Product{}, fmt.Errorf("product %d: %w", id, ErrNotFound)
	}

	return product, nil
}

func (r *MemoryProductRepository) BySKU(ctx context.Context, sku string) (Product, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, product := range r.products {
		if product.SKU == sku {
			return product, nil
		}
	}

	return Product{}, fmt.Errorf("product %s: %w", sku, ErrNotFound)
}

func (r *MemoryProductRepository) List(ctx context.Context, filter ProductFilter) ([]Product, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	matched := make([]Product, 0, len(r.products))

	for _, product := range r.products {
		if filter.InStockOnly && product.Stock == 0 {
			continue
		}

		matched = append(matched, product)
	}

	// Same ORDER BY as the SQL implementation: callers must not be able to
	// tell the two apart.
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].SKU < matched[j].SKU
	})

	if filter.Offset >= len(matched) {
		return []Product{}, nil
	}

	matched = matched[filter.Offset:]

	if len(matched) > filter.Limit {
		matched = matched[:filter.Limit]
	}

	return matched, nil
}

func (r *MemoryProductRepository) ReserveStock(ctx context.Context, id int64, quantity int) (Product, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	product, ok := r.products[id]
	if !ok {
		return Product{}, fmt.Errorf("product %d: %w", id, ErrNotFound)
	}

	if product.Stock < quantity {
		return Product{}, fmt.Errorf(
			"reserve %d of product %d: %w: only %d in stock",
			quantity, id, ErrValidation, product.Stock,
		)
	}

	product.Stock -= quantity
	product.UpdatedAt = time.Now().UTC().Truncate(time.Second)

	r.products[id] = product

	return product, nil
}

func (r *MemoryProductRepository) Delete(ctx context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.products[id]; !ok {
		return fmt.Errorf("product %d: %w", id, ErrNotFound)
	}

	delete(r.products, id)

	return nil
}
