package service_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-97/internal/domain"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-97/internal/service"
)

// memoryRepo is the whole reason the service defines its own interfaces: these
// tests run against a map, in microseconds, with no database.
type memoryRepo struct {
	mu     sync.Mutex
	links  map[domain.Code]domain.Link
	clicks map[domain.Code]int64

	// failCodes makes CreateLink report ErrCodeTaken for these codes, to
	// exercise the collision retry.
	failCodes map[domain.Code]bool
	// createErr fails every create, to exercise the error path.
	createErr error
	creates   int
}

func newRepo() *memoryRepo {
	return &memoryRepo{
		links:     make(map[domain.Code]domain.Link),
		clicks:    make(map[domain.Code]int64),
		failCodes: make(map[domain.Code]bool),
	}
}

func (r *memoryRepo) CreateLink(_ context.Context, link domain.Link) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.creates++

	if r.createErr != nil {
		return r.createErr
	}

	if _, exists := r.links[link.Code]; exists || r.failCodes[link.Code] {
		return domain.ErrCodeTaken
	}

	r.links[link.Code] = link

	return nil
}

func (r *memoryRepo) Link(_ context.Context, code domain.Code) (domain.Link, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	link, found := r.links[code]
	if !found {
		return domain.Link{}, domain.ErrNotFound
	}

	return link, nil
}

func (r *memoryRepo) LinksByOwner(_ context.Context, owner string, _ int) ([]domain.Link, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var links []domain.Link

	for _, link := range r.links {
		if link.Owner == owner {
			links = append(links, link)
		}
	}

	return links, nil
}

func (r *memoryRepo) DeactivateLink(_ context.Context, owner string, code domain.Code) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	link, found := r.links[code]
	if !found || link.Owner != owner {
		return domain.ErrNotFound
	}

	link.Active = false
	r.links[code] = link

	return nil
}

func (r *memoryRepo) RecordClick(_ context.Context, click domain.Click) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.clicks[click.Code]++

	return nil
}

func (r *memoryRepo) ClickCount(_ context.Context, code domain.Code) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.clicks[code], nil
}

func newService(t *testing.T, repo *memoryRepo, now time.Time) *service.Service {
	t.Helper()

	svc := service.New(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetClock(func() time.Time { return now })

	return svc
}

var fixedNow = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

func TestCreateLinkGeneratesACode(t *testing.T) {
	repo := newRepo()
	svc := newService(t, repo, fixedNow)

	link, err := svc.CreateLink(t.Context(), service.CreateRequest{
		Owner: "ada", Target: "https://example.com",
	})
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	if len(link.Code) != domain.CodeLength {
		t.Errorf("code %q is %d characters, want %d", link.Code, len(link.Code), domain.CodeLength)
	}

	if !link.Active || link.Owner != "ada" {
		t.Errorf("link = %+v", link)
	}
}

func TestCreateLinkAcceptsACustomCode(t *testing.T) {
	svc := newService(t, newRepo(), fixedNow)

	link, err := svc.CreateLink(t.Context(), service.CreateRequest{
		Owner: "ada", Target: "https://example.com", Code: "golang",
	})
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	if link.Code != "golang" {
		t.Errorf("code = %q, want golang", link.Code)
	}
}

func TestCreateLinkValidates(t *testing.T) {
	svc := newService(t, newRepo(), fixedNow)

	cases := map[string]service.CreateRequest{
		"bad target":    {Owner: "ada", Target: "javascript:alert(1)"},
		"no owner":      {Target: "https://example.com"},
		"bad code":      {Owner: "ada", Target: "https://example.com", Code: "not a code"},
		"reserved code": {Owner: "ada", Target: "https://example.com", Code: "api"},
		"past expiry":   {Owner: "ada", Target: "https://example.com", ExpiresAt: fixedNow.Add(-time.Hour)},
	}

	for name, request := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.CreateLink(t.Context(), request); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

// A collision must be retried, not returned. With 62^7 codes it is rare, but
// "rare" is not "never" at scale.
func TestCreateLinkRetriesOnCollision(t *testing.T) {
	repo := newRepo()

	// Fail the first two generated codes, whatever they are.
	attempts := 0

	repo.createErr = nil

	failing := &collidingRepo{memoryRepo: repo, failFirst: 2, attempts: &attempts}

	svc := service.New(failing, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetClock(func() time.Time { return fixedNow })

	link, err := svc.CreateLink(t.Context(), service.CreateRequest{
		Owner: "ada", Target: "https://example.com",
	})
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	if attempts != 3 {
		t.Errorf("attempts = %d, want 3 (two collisions then success)", attempts)
	}

	if link.Code == "" {
		t.Error("no code was assigned")
	}
}

// An endless retry would hide a broken generator or a database returning
// "taken" for another reason.
func TestCreateLinkGivesUpAfterRepeatedCollisions(t *testing.T) {
	attempts := 0

	failing := &collidingRepo{memoryRepo: newRepo(), failFirst: 100, attempts: &attempts}

	svc := service.New(failing, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetClock(func() time.Time { return fixedNow })

	if _, err := svc.CreateLink(t.Context(), service.CreateRequest{
		Owner: "ada", Target: "https://example.com",
	}); err == nil {
		t.Fatal("expected an error after repeated collisions")
	}

	if attempts > 10 {
		t.Errorf("attempts = %d - the retry is not bounded", attempts)
	}
}

type collidingRepo struct {
	*memoryRepo

	failFirst int
	attempts  *int
}

func (r *collidingRepo) CreateLink(ctx context.Context, link domain.Link) error {
	*r.attempts++

	if *r.attempts <= r.failFirst {
		return domain.ErrCodeTaken
	}

	return r.memoryRepo.CreateLink(ctx, link)
}

func TestFollow(t *testing.T) {
	repo := newRepo()
	svc := newService(t, repo, fixedNow)

	created, err := svc.CreateLink(t.Context(), service.CreateRequest{
		Owner: "ada", Target: "https://example.com", Code: "golang",
	})
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	link, err := svc.Follow(t.Context(), "golang")
	if err != nil {
		t.Fatalf("Follow: %v", err)
	}

	if link.Target != created.Target {
		t.Errorf("target = %q, want %q", link.Target, created.Target)
	}
}

// An unparseable code cannot exist, so it is a 404 rather than a 400: the
// client followed a link that is not ours, it did not send a bad request.
func TestFollowTreatsAnInvalidCodeAsNotFound(t *testing.T) {
	svc := newService(t, newRepo(), fixedNow)

	if _, err := svc.Follow(t.Context(), "not a code!"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Follow(invalid) = %v, want ErrNotFound", err)
	}
}

func TestFollowRefusesDeactivatedAndExpiredLinks(t *testing.T) {
	repo := newRepo()
	svc := newService(t, repo, fixedNow)

	if _, err := svc.CreateLink(t.Context(), service.CreateRequest{
		Owner: "ada", Target: "https://example.com", Code: "gone01",
	}); err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	if err := svc.DeactivateLink(t.Context(), "ada", "gone01"); err != nil {
		t.Fatalf("DeactivateLink: %v", err)
	}

	if _, err := svc.Follow(t.Context(), "gone01"); !errors.Is(err, domain.ErrGone) {
		t.Errorf("Follow(deactivated) = %v, want ErrGone", err)
	}

	// And an expired one, without sleeping: the clock is injected.
	if _, err := svc.CreateLink(t.Context(), service.CreateRequest{
		Owner: "ada", Target: "https://example.com", Code: "exp001",
		ExpiresAt: fixedNow.Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	svc.SetClock(func() time.Time { return fixedNow.Add(2 * time.Hour) })

	if _, err := svc.Follow(t.Context(), "exp001"); !errors.Is(err, domain.ErrGone) {
		t.Errorf("Follow(expired) = %v, want ErrGone", err)
	}
}

// "This exists but is not yours" is an enumeration oracle, so it is a 404.
func TestGetLinkHidesOtherOwnersLinks(t *testing.T) {
	repo := newRepo()
	svc := newService(t, repo, fixedNow)

	if _, err := svc.CreateLink(t.Context(), service.CreateRequest{
		Owner: "ada", Target: "https://example.com", Code: "golang",
	}); err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	if _, err := svc.GetLink(t.Context(), "grace", "golang"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetLink as another owner = %v, want ErrNotFound", err)
	}
}

func TestListLinksIncludesClickCounts(t *testing.T) {
	repo := newRepo()
	svc := newService(t, repo, fixedNow)

	if _, err := svc.CreateLink(t.Context(), service.CreateRequest{
		Owner: "ada", Target: "https://example.com", Code: "golang",
	}); err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := svc.RecordClick(t.Context(), domain.Click{Code: "golang", OccurredAt: fixedNow}); err != nil {
			t.Fatalf("RecordClick: %v", err)
		}
	}

	links, err := svc.ListLinks(t.Context(), "ada", 10)
	if err != nil {
		t.Fatalf("ListLinks: %v", err)
	}

	if len(links) != 1 {
		t.Fatalf("links = %d, want 1", len(links))
	}

	if links[0].Clicks != 3 {
		t.Errorf("clicks = %d, want 3", links[0].Clicks)
	}
}
