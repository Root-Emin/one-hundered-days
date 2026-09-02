// Package catalog stores products and their stock levels.
//
// It is the example this day documents: small enough to read in one sitting,
// real enough that the documentation has something to be wrong about.
//
// The package owns two invariants, and they are the reason it exists as a
// package rather than a map in a handler:
//
//   - a product's stock is never negative; Reserve fails rather than
//     overselling
//   - a SKU is unique, and the store rejects a duplicate rather than silently
//     overwriting
//
// Everything is in memory and guarded by a mutex. Persistence is the caller's
// problem: a store that also owns a database is a store that cannot be tested
// without one.
//
// # Concurrency
//
// All methods are safe for concurrent use.
//
// # Errors
//
// Callers distinguish failures with errors.Is against the sentinel errors
// declared here. The error strings are for humans and may change; the
// sentinels are the contract and will not.
package catalog

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Sentinel errors returned by this package.
//
// These are part of the API. A caller switching on error strings will break;
// a caller using errors.Is will not.
var (
	// ErrNotFound means no product has the requested SKU.
	ErrNotFound = errors.New("product not found")

	// ErrDuplicateSKU means a product with this SKU already exists.
	ErrDuplicateSKU = errors.New("duplicate sku")

	// ErrInsufficientStock means the reservation asked for more than is
	// available. The stock is left unchanged.
	ErrInsufficientStock = errors.New("insufficient stock")

	// ErrInvalidProduct means a field failed validation. The message names
	// the field.
	ErrInvalidProduct = errors.New("invalid product")
)

// Product is an item that can be sold.
//
// PriceCent is in the smallest currency unit, always an integer: floating
// point money is how a cart totalling 19.99 three times becomes 59.969999.
type Product struct {
	SKU       string    `json:"sku"`
	Name      string    `json:"name"`
	PriceCent int64     `json:"price_cent"`
	Stock     int       `json:"stock"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store holds the catalog in memory.
//
// The zero value is not usable; call New.
type Store struct {
	mu       sync.RWMutex
	products map[string]Product
	now      func() time.Time
}

// New returns an empty Store.
func New() *Store {
	return &Store{products: make(map[string]Product), now: time.Now}
}

// SetClock replaces the time source. It exists for tests, which need
// UpdatedAt to be predictable.
func (s *Store) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.now = now
}

// Add stores a new product.
//
// It returns ErrDuplicateSKU if the SKU is taken, and ErrInvalidProduct if the
// SKU is empty, the name is empty, the price is not positive, or the stock is
// negative.
func (s *Store) Add(product Product) (Product, error) {
	if err := validate(product); err != nil {
		return Product{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.products[product.SKU]; exists {
		return Product{}, fmt.Errorf("%s: %w", product.SKU, ErrDuplicateSKU)
	}

	product.UpdatedAt = s.now().UTC()

	s.products[product.SKU] = product

	return product, nil
}

// Get returns one product, or ErrNotFound.
func (s *Store) Get(sku string) (Product, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	product, found := s.products[sku]
	if !found {
		return Product{}, fmt.Errorf("%s: %w", sku, ErrNotFound)
	}

	return product, nil
}

// List returns every product, ordered by SKU.
//
// The order is defined so the response is stable: an endpoint that returns the
// same data in a different order every call breaks caching, pagination and
// every test that touches it.
func (s *Store) List() []Product {
	s.mu.RLock()
	defer s.mu.RUnlock()

	products := make([]Product, 0, len(s.products))

	for _, product := range s.products {
		products = append(products, product)
	}

	sort.Slice(products, func(i, j int) bool { return products[i].SKU < products[j].SKU })

	return products
}

// Reserve decrements the stock of a product.
//
// It returns ErrInsufficientStock, and changes nothing, if quantity exceeds
// the available stock. Reserving zero or fewer is ErrInvalidProduct.
func (s *Store) Reserve(sku string, quantity int) (Product, error) {
	if quantity <= 0 {
		return Product{}, fmt.Errorf("%w: quantity must be positive", ErrInvalidProduct)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	product, found := s.products[sku]
	if !found {
		return Product{}, fmt.Errorf("%s: %w", sku, ErrNotFound)
	}

	if product.Stock < quantity {
		return Product{}, fmt.Errorf("%s: %w: have %d, want %d",
			sku, ErrInsufficientStock, product.Stock, quantity)
	}

	product.Stock -= quantity
	product.UpdatedAt = s.now().UTC()

	s.products[sku] = product

	return product, nil
}

// Delete removes a product, returning ErrNotFound if it was not there.
func (s *Store) Delete(sku string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, found := s.products[sku]; !found {
		return fmt.Errorf("%s: %w", sku, ErrNotFound)
	}

	delete(s.products, sku)

	return nil
}

// Len returns the number of products.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.products)
}

func validate(product Product) error {
	switch {
	case strings.TrimSpace(product.SKU) == "":
		return fmt.Errorf("%w: sku is required", ErrInvalidProduct)
	case strings.TrimSpace(product.Name) == "":
		return fmt.Errorf("%w: name is required", ErrInvalidProduct)
	case product.PriceCent <= 0:
		return fmt.Errorf("%w: price_cent must be positive", ErrInvalidProduct)
	case product.Stock < 0:
		return fmt.Errorf("%w: stock cannot be negative", ErrInvalidProduct)
	default:
		return nil
	}
}
