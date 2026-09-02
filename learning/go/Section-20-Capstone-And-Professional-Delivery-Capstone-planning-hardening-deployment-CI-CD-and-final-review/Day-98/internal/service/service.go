// Package service holds Linkr's use cases: create a link, follow a link, list
// and deactivate.
//
// It sits between transport and storage, and it exists for one reason: these
// operations are not CRUD. "Follow a link" is a cache read, a fallback, a
// domain rule and an event - and none of that belongs in a handler, where it
// could only be tested through HTTP.
//
// The dependencies are interfaces DEFINED HERE, not imported from the store.
// The consumer declares what it needs; the provider satisfies it. That is what
// lets the tests below run against a map instead of a database.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-98/internal/domain"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-98/internal/store"
)

// LinkRepository is the persistence this service needs, and no more.
type LinkRepository interface {
	DailyStats(ctx context.Context, code domain.Code, days int) ([]store.DailyStat, error)
	CreateLink(ctx context.Context, link domain.Link) error
	Link(ctx context.Context, code domain.Code) (domain.Link, error)
	LinksByOwner(ctx context.Context, owner string, limit int) ([]domain.Link, error)
	DeactivateLink(ctx context.Context, owner string, code domain.Code) error
	RecordClick(ctx context.Context, click domain.Click) error
	ClickCount(ctx context.Context, code domain.Code) (int64, error)
}

// Cache is the redirect's read cache. The service takes an interface so it can
// run without one - which is how the tests for the domain rules stay free of
// cache behaviour.
type Cache interface {
	Get(code domain.Code) (link domain.Link, found bool, hit bool)
	Put(link domain.Link)
	PutMissing(code domain.Code)
	Invalidate(code domain.Code)
}

// CacheObserver counts cache results for metrics.
type CacheObserver interface {
	RecordCacheLookup(result string)
}

// Clock is injectable so expiry can be tested without sleeping.
type Clock func() time.Time

// Service implements the use cases.
type Service struct {
	links    LinkRepository
	cache    Cache
	observer CacheObserver
	logger   *slog.Logger
	now      Clock
}

// New builds a Service.
func New(links LinkRepository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}

	return &Service{links: links, logger: logger, now: time.Now}
}

// SetCache attaches a redirect cache. Without one the service still works, one
// database read at a time.
func (s *Service) SetCache(cache Cache, observer CacheObserver) {
	s.cache = cache
	s.observer = observer
}

// SetClock replaces the time source, for tests.
func (s *Service) SetClock(now Clock) {
	s.now = now
}

// CreateRequest is the input to CreateLink.
type CreateRequest struct {
	Owner  string
	Target string
	// Code is optional; empty means "generate one".
	Code string
	// ExpiresAt is optional; zero means no expiry.
	ExpiresAt time.Time
}

// maxCodeAttempts bounds the retry when a generated code collides.
//
// With 62^7 codes a collision is vanishingly rare, so a loop that never gives
// up would be an infinite loop hiding a real bug - a broken generator, or a
// database returning "taken" for another reason.
const maxCodeAttempts = 5

// CreateLink validates the request and stores a link.
func (s *Service) CreateLink(ctx context.Context, request CreateRequest) (domain.Link, error) {
	now := s.now().UTC()

	if request.Code != "" {
		code, err := domain.ParseCode(request.Code)
		if err != nil {
			return domain.Link{}, err
		}

		link, err := domain.NewLink(code, request.Owner, request.Target, request.ExpiresAt, now)
		if err != nil {
			return domain.Link{}, err
		}

		if err := s.links.CreateLink(ctx, link); err != nil {
			return domain.Link{}, err
		}

		return link, nil
	}

	for attempt := 0; attempt < maxCodeAttempts; attempt++ {
		code, err := domain.NewCode()
		if err != nil {
			return domain.Link{}, err
		}

		link, err := domain.NewLink(code, request.Owner, request.Target, request.ExpiresAt, now)
		if err != nil {
			return domain.Link{}, err
		}

		err = s.links.CreateLink(ctx, link)

		if errors.Is(err, domain.ErrCodeTaken) {
			s.logger.Warn("code collision, retrying", slog.String("code", code.String()))

			continue
		}

		if err != nil {
			return domain.Link{}, err
		}

		return link, nil
	}

	return domain.Link{}, fmt.Errorf("could not find a free code in %d attempts", maxCodeAttempts)
}

// Follow resolves a code to its target and records the click.
//
// The ordering is the point of the whole service:
//
//  1. look the link up
//  2. apply the domain rule - is it followable RIGHT NOW
//  3. return the target so the caller can redirect immediately
//  4. record the click afterwards, without blocking the redirect
//
// Step 4 is separate (RecordClick) rather than part of this call, so the
// handler can write the 302 first. A click that is not counted is a metrics
// problem; a redirect that waits on a write is a broken link.
func (s *Service) Follow(ctx context.Context, rawCode string) (domain.Link, error) {
	code, err := domain.ParseCode(rawCode)
	if err != nil {
		// An unparseable code cannot exist, so it is a 404 rather than a 400:
		// the client did not make a malformed request, it followed a link that
		// is not ours.
		return domain.Link{}, fmt.Errorf("%s: %w", rawCode, domain.ErrNotFound)
	}

	link, err := s.resolve(ctx, code)
	if err != nil {
		return domain.Link{}, err
	}

	if err := link.Followable(s.now()); err != nil {
		return link, err
	}

	return link, nil
}

// resolve reads the cache, then the database.
//
// The negative result is cached too: a crawler hammering a code that does not
// exist would otherwise reach the database on every request, which is the
// shape of a cheap denial of service.
func (s *Service) resolve(ctx context.Context, code domain.Code) (domain.Link, error) {
	if s.cache != nil {
		link, found, hit := s.cache.Get(code)

		if hit {
			if !found {
				s.observe("negative_hit")

				return domain.Link{}, fmt.Errorf("%s: %w", code, domain.ErrNotFound)
			}

			s.observe("hit")

			return link, nil
		}

		s.observe("miss")
	}

	link, err := s.links.Link(ctx, code)

	if errors.Is(err, domain.ErrNotFound) {
		if s.cache != nil {
			s.cache.PutMissing(code)
		}

		return domain.Link{}, err
	}

	if err != nil {
		return domain.Link{}, err
	}

	if s.cache != nil {
		s.cache.Put(link)
	}

	return link, nil
}

func (s *Service) observe(result string) {
	if s.observer != nil {
		s.observer.RecordCacheLookup(result)
	}
}

// RecordClick stores a click event.
//
// Called after the response is written. Its error is logged by the caller, not
// returned to the client: the redirect already happened.
func (s *Service) RecordClick(ctx context.Context, click domain.Click) error {
	return s.links.RecordClick(ctx, click)
}

// ListLinks returns an owner's links with their click totals.
func (s *Service) ListLinks(ctx context.Context, owner string, limit int) ([]domain.Link, error) {
	links, err := s.links.LinksByOwner(ctx, owner, limit)
	if err != nil {
		return nil, err
	}

	for i := range links {
		count, err := s.links.ClickCount(ctx, links[i].Code)
		if err != nil {
			return nil, err
		}

		links[i].Clicks = count
	}

	return links, nil
}

// GetLink returns one of the owner's links.
func (s *Service) GetLink(ctx context.Context, owner string, rawCode string) (domain.Link, error) {
	code, err := domain.ParseCode(rawCode)
	if err != nil {
		return domain.Link{}, fmt.Errorf("%s: %w", rawCode, domain.ErrNotFound)
	}

	link, err := s.links.Link(ctx, code)
	if err != nil {
		return domain.Link{}, err
	}

	if link.Owner != owner {
		// Not found, not forbidden: "this exists but is not yours" is an
		// enumeration oracle, and the caller can do nothing with the
		// distinction anyway.
		return domain.Link{}, fmt.Errorf("%s: %w", rawCode, domain.ErrNotFound)
	}

	if link.Clicks, err = s.links.ClickCount(ctx, code); err != nil {
		return domain.Link{}, err
	}

	return link, nil
}

// DeactivateLink turns one of the owner's links off.
func (s *Service) DeactivateLink(ctx context.Context, owner, rawCode string) error {
	code, err := domain.ParseCode(rawCode)
	if err != nil {
		return fmt.Errorf("%s: %w", rawCode, domain.ErrNotFound)
	}

	if err := s.links.DeactivateLink(ctx, owner, code); err != nil {
		return err
	}

	// Invalidate AFTER the database commits. Deleting first leaves a window
	// where a concurrent redirect repopulates the cache from the old row, and
	// the deactivated link keeps working for a full TTL.
	if s.cache != nil {
		s.cache.Invalidate(code)
	}

	return nil
}

// Stats returns clicks per day for one of the owner's links.
//
// It reads the daily aggregate, never the raw clicks table: that is what makes
// this a bounded indexed read however many millions of clicks exist.
func (s *Service) Stats(ctx context.Context, owner, rawCode string, days int) ([]store.DailyStat, error) {
	link, err := s.GetLink(ctx, owner, rawCode)
	if err != nil {
		return nil, err
	}

	return s.links.DailyStats(ctx, link.Code, days)
}
