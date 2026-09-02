// Package inventory is the domain and service layer.
//
// It knows nothing about gRPC or HTTP. Both transports call into it, which is
// the point of today's lesson: two protocols, one implementation of the rules.
package inventory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound      = errors.New("item not found")
	ErrAlreadyExists = errors.New("item already exists")
	ErrValidation    = errors.New("invalid request")
	ErrInsufficient  = errors.New("insufficient stock")
)

type Item struct {
	SKU       string
	Name      string
	Quantity  int32
	Location  string
	Barcode   string
	UpdatedAt time.Time
}

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type Service struct {
	mu    sync.RWMutex
	items map[string]Item
	clock Clock

	// idempotency remembers the result of a request_id so a retried call does
	// not adjust stock twice. In production this is a table with a TTL, not a
	// map - but the semantics are the same.
	idempotency map[string]Item
}

func NewService(clock Clock) *Service {
	if clock == nil {
		clock = SystemClock{}
	}

	return &Service{
		items:       make(map[string]Item),
		idempotency: make(map[string]Item),
		clock:       clock,
	}
}

func normalizeSKU(sku string) string {
	return strings.ToUpper(strings.TrimSpace(sku))
}

func (s *Service) Get(ctx context.Context, sku string) (Item, error) {
	sku = normalizeSKU(sku)

	if sku == "" {
		return Item{}, fmt.Errorf("%w: sku is required", ErrValidation)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	item, found := s.items[sku]
	if !found {
		return Item{}, fmt.Errorf("%s: %w", sku, ErrNotFound)
	}

	return item, nil
}

func (s *Service) Create(ctx context.Context, item Item, requestID string) (Item, error) {
	item.SKU = normalizeSKU(item.SKU)
	item.Name = strings.TrimSpace(item.Name)

	switch {
	case item.SKU == "":
		return Item{}, fmt.Errorf("%w: sku is required", ErrValidation)
	case len(item.SKU) > 32:
		return Item{}, fmt.Errorf("%w: sku must be at most 32 characters", ErrValidation)
	case item.Name == "":
		return Item{}, fmt.Errorf("%w: name is required", ErrValidation)
	case item.Quantity < 0:
		return Item{}, fmt.Errorf("%w: quantity cannot be negative", ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// A retried create with the same id returns the original item instead of
	// failing with AlreadyExists - that is what makes the RPC safe to retry.
	if requestID != "" {
		if previous, replayed := s.idempotency[requestID]; replayed {
			return previous, nil
		}
	}

	if _, exists := s.items[item.SKU]; exists {
		return Item{}, fmt.Errorf("%s: %w", item.SKU, ErrAlreadyExists)
	}

	item.UpdatedAt = s.clock.Now()
	s.items[item.SKU] = item

	if requestID != "" {
		s.idempotency[requestID] = item
	}

	return item, nil
}

func (s *Service) List(ctx context.Context, location string, pageSize int32, pageToken string) ([]Item, string, int32, error) {
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	matched := make([]Item, 0, len(s.items))

	for _, item := range s.items {
		if location != "" && !strings.EqualFold(item.Location, location) {
			continue
		}

		matched = append(matched, item)
	}

	sort.Slice(matched, func(i, j int) bool { return matched[i].SKU < matched[j].SKU })

	total := int32(len(matched))

	// The page token is the sku to start after: stable under insertions, and
	// meaningless to the client, which is exactly what a token should be.
	if pageToken != "" {
		start := 0

		for i, item := range matched {
			if item.SKU > pageToken {
				start = i
				break
			}

			start = i + 1
		}

		matched = matched[start:]
	}

	next := ""

	if int32(len(matched)) > pageSize {
		matched = matched[:pageSize]
		next = matched[len(matched)-1].SKU
	}

	return matched, next, total, nil
}

// Adjust changes stock by a delta. It is the operation that must not run
// twice, which is why requestID exists.
func (s *Service) Adjust(ctx context.Context, sku string, delta int32, reason, requestID string) (item Item, previous int32, err error) {
	sku = normalizeSKU(sku)

	switch {
	case sku == "":
		return Item{}, 0, fmt.Errorf("%w: sku is required", ErrValidation)
	case delta == 0:
		return Item{}, 0, fmt.Errorf("%w: delta must not be zero", ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if requestID != "" {
		if stored, replayed := s.idempotency[requestID]; replayed {
			return stored, stored.Quantity - delta, nil
		}
	}

	current, found := s.items[sku]
	if !found {
		return Item{}, 0, fmt.Errorf("%s: %w", sku, ErrNotFound)
	}

	if current.Quantity+delta < 0 {
		return Item{}, 0, fmt.Errorf("%w: %s has %d on hand, cannot apply %d",
			ErrInsufficient, sku, current.Quantity, delta)
	}

	previous = current.Quantity

	current.Quantity += delta
	current.UpdatedAt = s.clock.Now()

	s.items[sku] = current

	if requestID != "" {
		s.idempotency[requestID] = current
	}

	return current, previous, nil
}

// Delete is idempotent: removing something already gone is success, and the
// caller is told whether a row actually disappeared.
func (s *Service) Delete(ctx context.Context, sku string) (bool, error) {
	sku = normalizeSKU(sku)

	if sku == "" {
		return false, fmt.Errorf("%w: sku is required", ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, found := s.items[sku]; !found {
		return false, nil
	}

	delete(s.items, sku)

	return true, nil
}

func (s *Service) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.items)
}
