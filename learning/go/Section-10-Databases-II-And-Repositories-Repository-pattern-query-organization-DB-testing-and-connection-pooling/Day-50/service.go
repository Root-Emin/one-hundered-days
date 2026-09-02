package main

import (
	"context"
	"fmt"
)

/*
Business logic. It depends on the repository interfaces and the transaction
manager, and on nothing else - no SQL, no HTTP, no JSON.
*/

type ShopService struct {
	repos Repositories
	tx    TxManager
}

// NewShopService takes both the non-transactional repositories (for reads)
// and the transaction manager (for units of work), all through interfaces.
func NewShopService(repos Repositories, tx TxManager) *ShopService {
	return &ShopService{repos: repos, tx: tx}
}

func (s *ShopService) RegisterCustomer(ctx context.Context, email, name string) (Customer, error) {
	return s.repos.Customers.Create(ctx, Customer{Email: email, Name: name})
}

func (s *ShopService) Customer(ctx context.Context, id int64) (Customer, error) {
	return s.repos.Customers.ByID(ctx, id)
}

func (s *ShopService) AddProduct(ctx context.Context, product Product) (Product, error) {
	return s.repos.Products.Create(ctx, product)
}

func (s *ShopService) Products(ctx context.Context, limit, offset int) ([]Product, error) {
	return s.repos.Products.List(ctx, clampLimit(limit), clampOffset(offset))
}

func (s *ShopService) Orders(ctx context.Context, customerID int64, limit, offset int) ([]Order, error) {
	// Reads still validate their inputs: an unbounded LIMIT is an outage
	// waiting for a customer with a long history.
	if _, err := s.repos.Customers.ByID(ctx, customerID); err != nil {
		return nil, err
	}

	return s.repos.Orders.ListByCustomer(ctx, customerID, clampLimit(limit), clampOffset(offset))
}

func (s *ShopService) Order(ctx context.Context, id int64) (Order, error) {
	return s.repos.Orders.ByID(ctx, id)
}

// PlaceOrder is the unit of work of this service: stock reservation, order
// header and order items either all happen or none of them do.
//
// Prices come from the catalog inside the transaction, never from the request
// body - otherwise a client could name its own price.
func (s *ShopService) PlaceOrder(ctx context.Context, customerID int64, lines []OrderLine) (Order, error) {
	if len(lines) == 0 {
		return Order{}, fmt.Errorf("place order: %w: at least one line is required", ErrValidation)
	}

	if len(lines) > 50 {
		return Order{}, fmt.Errorf("place order: %w: at most 50 lines per order", ErrValidation)
	}

	// Merge duplicate lines so the stock check sees the true quantity: two
	// lines of 3 must be rejected when only 5 are left.
	merged := make(map[int64]int, len(lines))
	order := make([]int64, 0, len(lines))

	for _, line := range lines {
		if line.Quantity <= 0 {
			return Order{}, fmt.Errorf("place order: %w: quantity must be positive", ErrValidation)
		}

		if _, seen := merged[line.ProductID]; !seen {
			order = append(order, line.ProductID)
		}

		merged[line.ProductID] += line.Quantity
	}

	var placed Order

	err := s.tx.WithinTx(ctx, func(repos Repositories) error {
		if _, err := repos.Customers.ByID(ctx, customerID); err != nil {
			return err
		}

		draft := Order{
			CustomerID: customerID,
			Status:     StatusPlaced,
			Items:      make([]OrderItem, 0, len(order)),
		}

		for _, productID := range order {
			quantity := merged[productID]

			product, err := repos.Products.ByID(ctx, productID)
			if err != nil {
				return err
			}

			// Reserve first: the UPDATE carries the stock check, so this is
			// where an oversell is caught, not in an if statement above.
			if err := repos.Products.ReserveStock(ctx, productID, quantity); err != nil {
				return err
			}

			draft.Items = append(draft.Items, OrderItem{
				ProductID: productID,
				Quantity:  quantity,
				UnitCents: product.PriceCents,
			})

			draft.TotalCents += int64(quantity) * product.PriceCents
		}

		created, err := repos.Orders.Create(ctx, draft)
		if err != nil {
			return err
		}

		placed = created

		return nil
	})
	if err != nil {
		return Order{}, err
	}

	return placed, nil
}

// CancelOrder is the reverse unit of work: the status change and the stock
// return must land together, or a cancelled order leaves stock unsellable.
func (s *ShopService) CancelOrder(ctx context.Context, orderID int64) (Order, error) {
	var cancelled Order

	err := s.tx.WithinTx(ctx, func(repos Repositories) error {
		order, err := repos.Orders.ByID(ctx, orderID)
		if err != nil {
			return err
		}

		if order.Status == StatusCancelled {
			return fmt.Errorf("cancel order %d: %w: already cancelled", orderID, ErrValidation)
		}

		if order.Status == StatusShipped {
			return fmt.Errorf("cancel order %d: %w: already shipped", orderID, ErrValidation)
		}

		for _, item := range order.Items {
			if err := repos.Products.ReleaseStock(ctx, item.ProductID, item.Quantity); err != nil {
				return err
			}
		}

		if err := repos.Orders.UpdateStatus(ctx, orderID, StatusCancelled); err != nil {
			return err
		}

		order.Status = StatusCancelled
		cancelled = order

		return nil
	})
	if err != nil {
		return Order{}, err
	}

	return cancelled, nil
}

func (s *ShopService) ShipOrder(ctx context.Context, orderID int64) (Order, error) {
	order, err := s.repos.Orders.ByID(ctx, orderID)
	if err != nil {
		return Order{}, err
	}

	if order.Status != StatusPlaced {
		return Order{}, fmt.Errorf("ship order %d: %w: status is %s", orderID, ErrValidation, order.Status)
	}

	if err := s.repos.Orders.UpdateStatus(ctx, orderID, StatusShipped); err != nil {
		return Order{}, err
	}

	order.Status = StatusShipped

	return order, nil
}

func clampLimit(limit int) int {
	if limit <= 0 || limit > 100 {
		return 20
	}

	return limit
}

func clampOffset(offset int) int {
	if offset < 0 {
		return 0
	}

	return offset
}
