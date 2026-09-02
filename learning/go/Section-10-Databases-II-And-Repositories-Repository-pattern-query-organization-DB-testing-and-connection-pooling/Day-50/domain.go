package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

/*
Domain layer: types, rules, errors and the repository interfaces.

Nothing in this file imports database/sql, net/http or encoding/json. That is
the review criterion for the whole section: if the domain compiles without
knowing how data is stored or transported, the layering is real.
*/

var (
	ErrNotFound   = errors.New("not found")
	ErrValidation = errors.New("validation failed")
	ErrConflict   = errors.New("already exists")
	ErrOutOfStock = errors.New("insufficient stock")
)

type Customer struct {
	ID        int64
	Email     string
	Name      string
	CreatedAt time.Time
}

func (c *Customer) Normalize() {
	c.Email = strings.ToLower(strings.TrimSpace(c.Email))
	c.Name = strings.TrimSpace(c.Name)
}

func (c Customer) Validate() error {
	switch {
	case c.Email == "" || !strings.Contains(c.Email, "@"):
		return fmt.Errorf("%w: email must look like an address", ErrValidation)
	case len(c.Email) > 254:
		return fmt.Errorf("%w: email is too long", ErrValidation)
	case c.Name == "":
		return fmt.Errorf("%w: name is required", ErrValidation)
	}

	return nil
}

type Product struct {
	ID         int64
	SKU        string
	Name       string
	PriceCents int64
	Stock      int
	CreatedAt  time.Time
}

func (p *Product) Normalize() {
	p.SKU = strings.ToUpper(strings.TrimSpace(p.SKU))
	p.Name = strings.TrimSpace(p.Name)
}

func (p Product) Validate() error {
	switch {
	case p.SKU == "":
		return fmt.Errorf("%w: sku is required", ErrValidation)
	case p.Name == "":
		return fmt.Errorf("%w: name is required", ErrValidation)
	case p.PriceCents <= 0:
		return fmt.Errorf("%w: price must be positive", ErrValidation)
	case p.Stock < 0:
		return fmt.Errorf("%w: stock cannot be negative", ErrValidation)
	}

	return nil
}

type OrderStatus string

const (
	StatusPlaced    OrderStatus = "placed"
	StatusShipped   OrderStatus = "shipped"
	StatusCancelled OrderStatus = "cancelled"
)

type Order struct {
	ID         int64
	CustomerID int64
	Status     OrderStatus
	TotalCents int64
	CreatedAt  time.Time
	Items      []OrderItem
}

type OrderItem struct {
	ID        int64
	OrderID   int64
	ProductID int64
	Quantity  int
	UnitCents int64
}

func (i OrderItem) LineTotal() int64 {
	return int64(i.Quantity) * i.UnitCents
}

// OrderLine is the request shape: what the caller asks for, before prices are
// resolved from the catalog. Trusting a client-supplied price is how a shop
// gets robbed.
type OrderLine struct {
	ProductID int64
	Quantity  int
}

//
// REPOSITORY INTERFACES
//
// One interface per aggregate, each expressed in domain language. Every
// method takes a context: cancellation must reach the database.
//

type CustomerRepository interface {
	Create(ctx context.Context, customer Customer) (Customer, error)
	ByID(ctx context.Context, id int64) (Customer, error)
	ByEmail(ctx context.Context, email string) (Customer, error)
	List(ctx context.Context, limit, offset int) ([]Customer, error)
	Delete(ctx context.Context, id int64) error
}

type ProductRepository interface {
	Create(ctx context.Context, product Product) (Product, error)
	ByID(ctx context.Context, id int64) (Product, error)
	BySKU(ctx context.Context, sku string) (Product, error)
	List(ctx context.Context, limit, offset int) ([]Product, error)
	ReserveStock(ctx context.Context, productID int64, quantity int) error
	ReleaseStock(ctx context.Context, productID int64, quantity int) error
}

type OrderRepository interface {
	Create(ctx context.Context, order Order) (Order, error)
	ByID(ctx context.Context, id int64) (Order, error)
	ListByCustomer(ctx context.Context, customerID int64, limit, offset int) ([]Order, error)
	UpdateStatus(ctx context.Context, id int64, status OrderStatus) error
}

// Repositories groups the three so a transaction can hand out a consistent
// set that all share one *sql.Tx.
type Repositories struct {
	Customers CustomerRepository
	Products  ProductRepository
	Orders    OrderRepository
}

// TxManager runs a function inside a transaction, giving it repositories
// bound to that transaction.
//
// This is the piece that keeps "place an order" atomic without leaking
// *sql.Tx into the service: the service asks for a unit of work, not for a
// database handle.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(repos Repositories) error) error
}
