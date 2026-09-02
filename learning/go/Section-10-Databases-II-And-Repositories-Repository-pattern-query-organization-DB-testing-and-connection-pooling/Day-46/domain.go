package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

/*
The domain layer. It knows nothing about SQL, HTTP or JSON.

Everything below could be compiled without importing database/sql at all -
that is the test of whether persistence has really been isolated.
*/

var (
	ErrNotFound   = errors.New("product not found")
	ErrValidation = errors.New("invalid product")
	ErrConflict   = errors.New("sku already exists")
)

// Product is a business concept, not a table row. CostCents is internal
// pricing data: it exists in the domain but never reaches the API (see dto.go).
type Product struct {
	ID        int64
	SKU       string
	Name      string
	PriceCent int64
	CostCents int64
	Stock     int
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (p Product) Validate() error {
	switch {
	case strings.TrimSpace(p.SKU) == "":
		return fmt.Errorf("%w: sku is required", ErrValidation)

	case len(p.SKU) > 32:
		return fmt.Errorf("%w: sku must be at most 32 characters", ErrValidation)

	case strings.TrimSpace(p.Name) == "":
		return fmt.Errorf("%w: name is required", ErrValidation)

	case p.PriceCent <= 0:
		return fmt.Errorf("%w: price must be positive", ErrValidation)

	case p.CostCents < 0:
		return fmt.Errorf("%w: cost cannot be negative", ErrValidation)

	case p.Stock < 0:
		return fmt.Errorf("%w: stock cannot be negative", ErrValidation)
	}

	return nil
}

// MarginPercent is business logic, so it lives with the domain type rather
// than in a handler or a SQL expression.
func (p Product) MarginPercent() float64 {
	if p.PriceCent == 0 {
		return 0
	}

	return float64(p.PriceCent-p.CostCents) / float64(p.PriceCent) * 100
}

// ProductFilter keeps list options in one value, so adding a filter later does
// not change every implementation's signature.
type ProductFilter struct {
	InStockOnly bool
	Limit       int
	Offset      int
}

func (f ProductFilter) normalized() ProductFilter {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}

	if f.Offset < 0 {
		f.Offset = 0
	}

	return f
}

//
// THE INTERFACE
//
// It is written in terms of the domain: "reserve stock", not
// "UPDATE products SET stock = stock - ?". Every caller above this line can
// be tested without a database, and the storage engine can be replaced
// without touching them.
//
// The interface is declared here, next to its consumers, not next to the
// SQLite implementation - Go interfaces belong to the side that uses them.
//

type ProductRepository interface {
	Create(ctx context.Context, product Product) (Product, error)
	ByID(ctx context.Context, id int64) (Product, error)
	BySKU(ctx context.Context, sku string) (Product, error)
	List(ctx context.Context, filter ProductFilter) ([]Product, error)
	ReserveStock(ctx context.Context, id int64, quantity int) (Product, error)
	Delete(ctx context.Context, id int64) error
}

//
// SERVICE
//
// Business rules live here, above the repository and below HTTP. It takes the
// interface, so it never learns which implementation it received.
//

type CatalogService struct {
	products ProductRepository
}

// NewCatalogService is constructor injection: the dependency is explicit,
// visible in the signature, and impossible to forget. No package-level
// database handle anywhere.
func NewCatalogService(products ProductRepository) *CatalogService {
	return &CatalogService{products: products}
}

func (s *CatalogService) AddProduct(ctx context.Context, product Product) (Product, error) {
	product.SKU = strings.ToUpper(strings.TrimSpace(product.SKU))
	product.Name = strings.TrimSpace(product.Name)

	if err := product.Validate(); err != nil {
		return Product{}, err
	}

	// A business rule, enforced above storage: the database unique index is
	// the backstop, this is the friendly path.
	existing, err := s.products.BySKU(ctx, product.SKU)

	switch {
	case err == nil:
		return Product{}, fmt.Errorf("add product %s: %w (id %d)", product.SKU, ErrConflict, existing.ID)

	case !errors.Is(err, ErrNotFound):
		return Product{}, fmt.Errorf("add product %s: %w", product.SKU, err)
	}

	return s.products.Create(ctx, product)
}

func (s *CatalogService) Get(ctx context.Context, id int64) (Product, error) {
	return s.products.ByID(ctx, id)
}

func (s *CatalogService) Catalog(ctx context.Context, filter ProductFilter) ([]Product, error) {
	return s.products.List(ctx, filter.normalized())
}

// Sell is the reason the repository exposes ReserveStock instead of a generic
// Update: the "do not oversell" rule must be atomic in storage, whatever the
// storage is.
func (s *CatalogService) Sell(ctx context.Context, id int64, quantity int) (Product, error) {
	if quantity <= 0 {
		return Product{}, fmt.Errorf("%w: quantity must be positive", ErrValidation)
	}

	return s.products.ReserveStock(ctx, id, quantity)
}

func (s *CatalogService) Remove(ctx context.Context, id int64) error {
	return s.products.Delete(ctx, id)
}
