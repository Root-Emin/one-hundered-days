package main

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"
)

/*
Tests with injected fakes.

Every one of these runs in microseconds, in parallel, with no database, no
network and no waiting for time to pass. That is not a property of the tests -
it is a property of the design, and the tests are just where it pays off.
*/

//
// FAKES
//

type fakeClock struct {
	now time.Time
}

var _ Clock = (*fakeClock)(nil)

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

// sequenceIDs makes ids predictable, so assertions can name them.
type sequenceIDs struct {
	mu   sync.Mutex
	next int
}

var _ IDGenerator = (*sequenceIDs)(nil)

func (g *sequenceIDs) NewID() string {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.next++

	return "usr_" + strconv.Itoa(g.next)
}

// recordingMailer captures what would have been sent, and can be told to fail.
type recordingMailer struct {
	mu   sync.Mutex
	sent []User
	err  error
}

var _ Mailer = (*recordingMailer)(nil)

func (m *recordingMailer) SendWelcome(ctx context.Context, user User) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return m.err
	}

	m.sent = append(m.sent, user)

	return nil
}

func (m *recordingMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.sent)
}

type recordingAudit struct {
	mu      sync.Mutex
	actions []string
}

var _ AuditLogger = (*recordingAudit)(nil)

func (a *recordingAudit) Record(ctx context.Context, action string, user User) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.actions = append(a.actions, action)
}

func (a *recordingAudit) has(action string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, candidate := range a.actions {
		if candidate == action {
			return true
		}
	}

	return false
}

// failingRepository injects a storage failure at a chosen point.
type failingRepository struct {
	UserRepository

	createErr error
}

func (r failingRepository) Create(ctx context.Context, user User) (User, error) {
	if r.createErr != nil {
		return User{}, r.createErr
	}

	return r.UserRepository.Create(ctx, user)
}

//
// HELPERS
//

type harness struct {
	service *SignupService
	users   *MemoryUserRepository
	clock   *fakeClock
	mailer  *recordingMailer
	audit   *recordingAudit
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	users := NewMemoryUserRepository()
	clock := &fakeClock{now: time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)}
	mailer := &recordingMailer{}
	audit := &recordingAudit{}

	service, err := NewSignupService(SignupConfig{
		Users:       users,
		Clock:       clock,
		IDs:         &sequenceIDs{},
		Mailer:      mailer,
		Audit:       audit,
		TrialLength: 14 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("build service: %v", err)
	}

	return &harness{service: service, users: users, clock: clock, mailer: mailer, audit: audit}
}

//
// TESTS
//

func TestSignupHappyPath(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	user, err := h.service.Signup(context.Background(), " ADA@Example.com ", "Ada", "pro")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}

	// Deterministic id and time, because both are injected.
	if user.ID != "usr_1" {
		t.Fatalf("id = %q, want usr_1", user.ID)
	}

	if user.Email != "ada@example.com" {
		t.Fatalf("email = %q, want the normalised address", user.Email)
	}

	if !user.CreatedAt.Equal(h.clock.now) {
		t.Fatalf("created_at = %s, want the injected clock's time", user.CreatedAt)
	}

	if !user.TrialEnds.Equal(h.clock.now.Add(14 * 24 * time.Hour)) {
		t.Fatalf("trial ends = %s", user.TrialEnds)
	}

	if h.mailer.count() != 1 {
		t.Fatalf("welcome mails = %d, want 1", h.mailer.count())
	}

	if !h.audit.has("signup.completed") {
		t.Fatal("the signup was not audited")
	}
}

func TestSignupValidation(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	tests := []struct {
		name  string
		email string
		user  string
		plan  string
	}{
		{"empty email", "", "Ada", "pro"},
		{"malformed email", "ada.example.com", "Ada", "pro"},
		{"empty name", "ada@example.com", "  ", "pro"},
		{"unknown plan", "ada@example.com", "Ada", "platinum"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := h.service.Signup(context.Background(), test.email, test.user, test.plan)

			if !errors.Is(err, ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation", err)
			}
		})
	}

	// Nothing reached storage, and nobody was emailed.
	if count, err := h.service.Users(context.Background()); err != nil || count != 0 {
		t.Fatalf("stored users = %d (err=%v), want 0", count, err)
	}

	if h.mailer.count() != 0 {
		t.Fatalf("mails sent for invalid signups: %d", h.mailer.count())
	}
}

func TestSignupRejectsDuplicates(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.service.Signup(ctx, "ada@example.com", "Ada", "free"); err != nil {
		t.Fatalf("first signup: %v", err)
	}

	// Case and whitespace must not create a second account.
	if _, err := h.service.Signup(ctx, " Ada@Example.com ", "Ada", "free"); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

// TestWelcomeMailFailureDoesNotFailSignup pins a deliberate product decision:
// the user exists even if the mail bounced. Injecting the failure is the only
// way to test it.
func TestWelcomeMailFailureDoesNotFailSignup(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.mailer.err = errors.New("smtp unavailable")

	user, err := h.service.Signup(context.Background(), "ada@example.com", "Ada", "free")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}

	if user.ID == "" {
		t.Fatal("no user was created")
	}

	if !h.audit.has("signup.welcome_mail_failed") {
		t.Fatal("the mail failure was not recorded")
	}
}

func TestStorageFailureIsReported(t *testing.T) {
	t.Parallel()

	mailer := &recordingMailer{}

	service, err := NewSignupService(SignupConfig{
		Users:  failingRepository{UserRepository: NewMemoryUserRepository(), createErr: errors.New("disk full")},
		Clock:  &fakeClock{now: time.Now()},
		IDs:    &sequenceIDs{},
		Mailer: mailer,
	})
	if err != nil {
		t.Fatalf("build service: %v", err)
	}

	if _, err := service.Signup(context.Background(), "ada@example.com", "Ada", "free"); err == nil {
		t.Fatal("a storage failure was swallowed")
	}

	if mailer.count() != 0 {
		t.Fatal("a welcome mail was sent for a user that was never stored")
	}
}

// TestTrialExpiryWithoutWaiting is the clearest argument for injecting a
// clock: two weeks pass in one line.
func TestTrialExpiryWithoutWaiting(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	user, err := h.service.Signup(ctx, "ada@example.com", "Ada", "pro")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}

	active, err := h.service.TrialActive(ctx, user.ID)
	if err != nil || !active {
		t.Fatalf("trial active = %t (err=%v), want true", active, err)
	}

	h.clock.advance(15 * 24 * time.Hour)

	active, err = h.service.TrialActive(ctx, user.ID)
	if err != nil || active {
		t.Fatalf("trial active after expiry = %t (err=%v), want false", active, err)
	}
}

// TestConstructorRejectsMissingDependencies: a nil dependency is caught at
// construction, not in a handler.
func TestConstructorRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	tests := map[string]SignupConfig{
		"no repository": {Clock: SystemClock{}, IDs: &sequenceIDs{}},
		"no clock":      {Users: NewMemoryUserRepository(), IDs: &sequenceIDs{}},
		"no ids":        {Users: NewMemoryUserRepository(), Clock: SystemClock{}},
	}

	for name, config := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := NewSignupService(config); err == nil {
				t.Fatal("an incomplete service was constructed")
			}
		})
	}

	// The optional ones get working defaults instead.
	service, err := NewSignupService(SignupConfig{
		Users: NewMemoryUserRepository(),
		Clock: SystemClock{},
		IDs:   &sequenceIDs{},
	})
	if err != nil {
		t.Fatalf("build service: %v", err)
	}

	if _, err := service.Signup(context.Background(), "ada@example.com", "Ada", "free"); err != nil {
		t.Fatalf("signup with default mailer and audit: %v", err)
	}
}

// TestParallelSignupsAreIndependent: each test owns its dependencies, so they
// cannot interfere. The locator version below cannot make this claim.
func TestParallelSignupsAreIndependent(t *testing.T) {
	t.Parallel()

	for i := range 4 {
		t.Run("worker-"+strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)

			// Every subtest signs up the same address into its own repository.
			user, err := h.service.Signup(context.Background(), "same@example.com", "Same", "free")
			if err != nil {
				t.Fatalf("signup: %v", err)
			}

			if user.ID != "usr_1" {
				t.Fatalf("id = %q, want usr_1 - state leaked between tests", user.ID)
			}
		})
	}
}
