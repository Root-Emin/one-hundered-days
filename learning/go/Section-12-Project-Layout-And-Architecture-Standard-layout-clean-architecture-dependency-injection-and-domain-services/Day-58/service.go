package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

/*
The service. Note what is NOT in this file:

	time.Now()          - the clock is injected
	rand / uuid.New()   - the id generator is injected
	sql.Open / http.Get - storage and mail are injected
	package-level vars  - nothing is looked up from a global

Everything it needs arrives through the constructor, so the type signature is
also the documentation of its coupling.
*/

type SignupService struct {
	users  UserRepository
	clock  Clock
	ids    IDGenerator
	mailer Mailer
	audit  AuditLogger

	trialLength time.Duration
}

// SignupConfig groups the dependencies.
//
// With three dependencies, positional arguments are fine. Past that they turn
// into a line of five values nobody can read, and every new dependency breaks
// every call site - so the constructor takes a struct instead.
type SignupConfig struct {
	Users       UserRepository
	Clock       Clock
	IDs         IDGenerator
	Mailer      Mailer
	Audit       AuditLogger
	TrialLength time.Duration
}

// NewSignupService validates its dependencies at construction time.
//
// A nil dependency discovered here is a startup failure with a clear message.
// The same nil discovered later is a panic in a request handler at 3am.
func NewSignupService(config SignupConfig) (*SignupService, error) {
	switch {
	case config.Users == nil:
		return nil, errors.New("signup service: Users repository is required")
	case config.Clock == nil:
		return nil, errors.New("signup service: Clock is required")
	case config.IDs == nil:
		return nil, errors.New("signup service: IDGenerator is required")
	}

	// Genuinely optional dependencies get a no-op default rather than a nil
	// check at every call site.
	if config.Mailer == nil {
		config.Mailer = NoopMailer{}
	}

	if config.Audit == nil {
		config.Audit = NoopAudit{}
	}

	if config.TrialLength <= 0 {
		config.TrialLength = 14 * 24 * time.Hour
	}

	return &SignupService{
		users:       config.Users,
		clock:       config.Clock,
		ids:         config.IDs,
		mailer:      config.Mailer,
		audit:       config.Audit,
		trialLength: config.TrialLength,
	}, nil
}

func (s *SignupService) Signup(ctx context.Context, email, name, plan string) (User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	name = strings.TrimSpace(name)

	switch {
	case email == "" || !strings.Contains(email, "@"):
		return User{}, fmt.Errorf("%w: a valid email is required", ErrValidation)
	case name == "":
		return User{}, fmt.Errorf("%w: name is required", ErrValidation)
	case plan != "free" && plan != "pro":
		return User{}, fmt.Errorf("%w: plan must be free or pro", ErrValidation)
	}

	_, err := s.users.ByEmail(ctx, email)

	switch {
	case err == nil:
		return User{}, fmt.Errorf("signup %s: %w", email, ErrDuplicate)
	case !errors.Is(err, ErrNotFound):
		return User{}, fmt.Errorf("signup %s: %w", email, err)
	}

	now := s.clock.Now()

	user := User{
		ID:        s.ids.NewID(),
		Email:     email,
		Name:      name,
		Plan:      plan,
		CreatedAt: now,
	}

	if plan == "pro" {
		user.TrialEnds = now.Add(s.trialLength)
	}

	created, err := s.users.Create(ctx, user)
	if err != nil {
		return User{}, fmt.Errorf("signup %s: %w", email, err)
	}

	// A failed welcome mail must not undo a successful signup: the user
	// exists, and the mail can be retried. Deciding that is the service's
	// job; knowing how mail is sent is not.
	if err := s.mailer.SendWelcome(ctx, created); err != nil {
		s.audit.Record(ctx, "signup.welcome_mail_failed", created)
	}

	s.audit.Record(ctx, "signup.completed", created)

	return created, nil
}

// TrialActive is a time-dependent rule, and therefore a rule that is only
// testable because the clock is injected.
func (s *SignupService) TrialActive(ctx context.Context, id string) (bool, error) {
	user, err := s.users.ByID(ctx, id)
	if err != nil {
		return false, err
	}

	if user.TrialEnds.IsZero() {
		return false, nil
	}

	return s.clock.Now().Before(user.TrialEnds), nil
}

func (s *SignupService) Users(ctx context.Context) (int, error) {
	return s.users.Count(ctx)
}
