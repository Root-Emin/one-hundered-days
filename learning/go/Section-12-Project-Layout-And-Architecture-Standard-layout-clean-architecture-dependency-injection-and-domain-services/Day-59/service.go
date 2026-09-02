package main

import (
	"context"
	"sort"
	"sync"
	"time"
)

/*
The service layer.

It orchestrates: load the entity, ask it to do the thing, persist the result.
The rules themselves are in domain.go - a service method that starts making
decisions about what an order may do is a sign the entity is anemic again.
*/

type OrderRepository interface {
	Save(ctx context.Context, order *Order) (*Order, error)
	ByID(ctx context.Context, id int64) (*Order, error)
	List(ctx context.Context, state OrderState) ([]*Order, error)
}

type Catalog interface {
	Price(ctx context.Context, sku SKU) (Money, error)
}

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type OrderService struct {
	orders  OrderRepository
	catalog Catalog
	clock   Clock
}

func NewOrderService(orders OrderRepository, catalog Catalog, clock Clock) *OrderService {
	if clock == nil {
		clock = SystemClock{}
	}

	return &OrderService{orders: orders, catalog: catalog, clock: clock}
}

type LineRequest struct {
	SKU      string
	Quantity int
}

// CreateOrder parses every input into a domain type first, collecting all the
// failures rather than stopping at the first. Once past this block, the rest
// of the method works with values that cannot be invalid.
func (s *OrderService) CreateOrder(ctx context.Context, customerEmail string, lines []LineRequest) (*Order, error) {
	validation := &ValidationError{}

	email, err := NewEmail(customerEmail)
	validation.Add(err)

	type parsedLine struct {
		sku      SKU
		quantity Quantity
	}

	parsed := make([]parsedLine, 0, len(lines))

	for _, line := range lines {
		sku, skuErr := NewSKU(line.SKU)
		validation.Add(skuErr)

		quantity, quantityErr := NewQuantity(line.Quantity)
		validation.Add(quantityErr)

		if skuErr == nil && quantityErr == nil {
			parsed = append(parsed, parsedLine{sku: sku, quantity: quantity})
		}
	}

	if len(lines) == 0 {
		validation.Add(invalid("lines", "required", "an order needs at least one line"))
	}

	if err := validation.OrNil(); err != nil {
		return nil, err
	}

	order, err := NewOrder(email, s.clock.Now())
	if err != nil {
		return nil, err
	}

	for _, line := range parsed {
		// Prices come from the catalog, never from the request: a client that
		// can name its own price is a shop that loses money.
		price, err := s.catalog.Price(ctx, line.sku)
		if err != nil {
			return nil, err
		}

		if err := order.AddLine(line.sku, line.quantity, price); err != nil {
			return nil, err
		}
	}

	return s.orders.Save(ctx, order)
}

func (s *OrderService) AddLine(ctx context.Context, orderID int64, request LineRequest) (*Order, error) {
	order, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return nil, err
	}

	validation := &ValidationError{}

	sku, skuErr := NewSKU(request.SKU)
	validation.Add(skuErr)

	quantity, quantityErr := NewQuantity(request.Quantity)
	validation.Add(quantityErr)

	if err := validation.OrNil(); err != nil {
		return nil, err
	}

	price, err := s.catalog.Price(ctx, sku)
	if err != nil {
		return nil, err
	}

	// The state rule ("only a draft can change") is enforced by the entity,
	// not re-implemented here.
	if err := order.AddLine(sku, quantity, price); err != nil {
		return nil, err
	}

	return s.orders.Save(ctx, order)
}

func (s *OrderService) Submit(ctx context.Context, orderID int64) (*Order, error) {
	return s.transition(ctx, orderID, func(order *Order) error { return order.Submit() })
}

func (s *OrderService) Pay(ctx context.Context, orderID int64) (*Order, error) {
	return s.transition(ctx, orderID, func(order *Order) error { return order.MarkPaid(s.clock.Now()) })
}

func (s *OrderService) Cancel(ctx context.Context, orderID int64) (*Order, error) {
	return s.transition(ctx, orderID, func(order *Order) error { return order.Cancel() })
}

// transition is the shared shape of every workflow step: load, apply, save.
// Keeping it in one place means no step can forget to persist.
func (s *OrderService) transition(ctx context.Context, orderID int64, apply func(*Order) error) (*Order, error) {
	order, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return nil, err
	}

	if err := apply(order); err != nil {
		return nil, err
	}

	return s.orders.Save(ctx, order)
}

func (s *OrderService) Order(ctx context.Context, orderID int64) (*Order, error) {
	return s.orders.ByID(ctx, orderID)
}

func (s *OrderService) List(ctx context.Context, state OrderState) ([]*Order, error) {
	if state != "" {
		switch state {
		case OrderDraft, OrderSubmitted, OrderPaid, OrderCancelled:
		default:
			return nil, invalid("state", "enum", "state %q is not a valid order state", state)
		}
	}

	return s.orders.List(ctx, state)
}

//
// ADAPTERS
//

type MemoryOrderRepository struct {
	mu     sync.RWMutex
	orders map[int64]*Order
	nextID int64
}

func NewMemoryOrderRepository() *MemoryOrderRepository {
	return &MemoryOrderRepository{orders: make(map[int64]*Order), nextID: 1}
}

func (r *MemoryOrderRepository) Save(ctx context.Context, order *Order) (*Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if order.id == 0 {
		order.id = r.nextID
		r.nextID++
	}

	// Store a copy so a caller cannot mutate what is "persisted" by holding
	// on to the pointer - the closest an in-memory store gets to a database.
	stored := *order
	stored.lines = order.Lines()

	r.orders[order.id] = &stored

	return order, nil
}

func (r *MemoryOrderRepository) ByID(ctx context.Context, id int64) (*Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	order, found := r.orders[id]
	if !found {
		return nil, NotFoundError{Resource: "order", ID: itoa(id)}
	}

	copied := *order
	copied.lines = order.Lines()

	return &copied, nil
}

func (r *MemoryOrderRepository) List(ctx context.Context, state OrderState) ([]*Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	matched := make([]*Order, 0, len(r.orders))

	for _, order := range r.orders {
		if state != "" && order.state != state {
			continue
		}

		copied := *order
		copied.lines = order.Lines()

		matched = append(matched, &copied)
	}

	sort.Slice(matched, func(i, j int) bool { return matched[i].id < matched[j].id })

	return matched, nil
}

// StaticCatalog is the price source. In production it is a table or another
// service; the interface is what keeps that decision out of the domain.
type StaticCatalog struct {
	prices map[string]Money
}

func NewStaticCatalog() *StaticCatalog {
	catalog := &StaticCatalog{prices: map[string]Money{}}

	for sku, cents := range map[string]int64{
		"KB-01": 12900,
		"MS-02": 4900,
		"HP-03": 19900,
	} {
		money, err := NewMoney(cents, "EUR")
		if err != nil {
			panic("catalog seed is invalid: " + err.Error())
		}

		catalog.prices[sku] = money
	}

	return catalog
}

func (c *StaticCatalog) Price(ctx context.Context, sku SKU) (Money, error) {
	price, found := c.prices[sku.String()]
	if !found {
		return Money{}, NotFoundError{Resource: "product", ID: sku.String()}
	}

	return price, nil
}

func itoa(value int64) string {
	if value == 0 {
		return "0"
	}

	digits := ""

	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}

	return digits
}
