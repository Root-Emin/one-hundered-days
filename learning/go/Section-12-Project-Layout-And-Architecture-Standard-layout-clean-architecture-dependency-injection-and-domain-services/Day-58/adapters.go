package main

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

/*
The production implementations of the seams in ports.go.

Each one is small, and each one is the only place in the program that knows
about its technology: time.Now lives in SystemClock, randomness lives in
RandomIDGenerator, and so on.
*/

type SystemClock struct{}

var _ Clock = SystemClock{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type RandomIDGenerator struct {
	Prefix string
}

var _ IDGenerator = RandomIDGenerator{}

func (g RandomIDGenerator) NewID() string {
	raw := make([]byte, 10)

	if _, err := rand.Read(raw); err != nil {
		// A failing CSPRNG is not recoverable at this layer, and continuing
		// with a predictable id would be worse than crashing.
		panic("crypto/rand failed: " + err.Error())
	}

	encoded := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))

	if g.Prefix == "" {
		return encoded
	}

	return g.Prefix + "_" + encoded
}

// MemoryUserRepository stands in for the database (Section 9/10 has the real
// one). It is a production-shaped implementation, not a test fake: it enforces
// the uniqueness rule the same way a unique index would.
type MemoryUserRepository struct {
	mu    sync.RWMutex
	users map[string]User
}

func NewMemoryUserRepository() *MemoryUserRepository {
	return &MemoryUserRepository{users: make(map[string]User)}
}

var _ UserRepository = (*MemoryUserRepository)(nil)

func (r *MemoryUserRepository) Create(ctx context.Context, user User) (User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, existing := range r.users {
		if existing.Email == user.Email {
			return User{}, ErrDuplicate
		}
	}

	r.users[user.ID] = user

	return user, nil
}

func (r *MemoryUserRepository) ByEmail(ctx context.Context, email string) (User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, user := range r.users {
		if user.Email == email {
			return user, nil
		}
	}

	return User{}, fmt.Errorf("email %s: %w", email, ErrNotFound)
}

func (r *MemoryUserRepository) ByID(ctx context.Context, id string) (User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	user, found := r.users[id]
	if !found {
		return User{}, fmt.Errorf("id %s: %w", id, ErrNotFound)
	}

	return user, nil
}

func (r *MemoryUserRepository) Count(ctx context.Context) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.users), nil
}

// LogMailer is the development mailer. The SMTP one implements the same
// interface and is chosen in main, not here.
type LogMailer struct{}

var _ Mailer = LogMailer{}

func (LogMailer) SendWelcome(ctx context.Context, user User) error {
	log.Printf("mail to=%s subject=%q", user.Email, "Welcome to the service")

	return nil
}

type NoopMailer struct{}

var _ Mailer = NoopMailer{}

func (NoopMailer) SendWelcome(ctx context.Context, user User) error { return nil }

type LogAudit struct{}

var _ AuditLogger = LogAudit{}

func (LogAudit) Record(ctx context.Context, action string, user User) {
	log.Printf("audit action=%s user=%s plan=%s", action, user.ID, user.Plan)
}

type NoopAudit struct{}

var _ AuditLogger = NoopAudit{}

func (NoopAudit) Record(ctx context.Context, action string, user User) {}
