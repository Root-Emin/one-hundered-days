// Package todo is the service layer: tasks, their rules, and the token check
// that decides who owns what.
package todo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var (
	ErrUnauthenticated = errors.New("unauthenticated")
	ErrForbidden       = errors.New("forbidden")
	ErrNotFound        = errors.New("task not found")
	ErrValidation      = errors.New("invalid task")
)

type Task struct {
	ID        int64
	Owner     string
	Title     string
	Done      bool
	DueAt     time.Time
	CreatedAt time.Time
}

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

// TokenStore maps API tokens to users. Tokens are stored hashed, as in Day 51.
type TokenStore struct {
	mu     sync.RWMutex
	tokens map[string]string // sha256(token) -> user
}

func NewTokenStore() *TokenStore {
	return &TokenStore{tokens: make(map[string]string)}
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))

	return hex.EncodeToString(sum[:])
}

func (s *TokenStore) Add(token, user string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tokens[hashToken(token)] = user
}

func (s *TokenStore) Resolve(token string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, found := s.tokens[hashToken(strings.TrimSpace(token))]
	if !found {
		return "", ErrUnauthenticated
	}

	return user, nil
}

type Service struct {
	mu     sync.RWMutex
	tasks  map[int64]Task
	nextID int64
	clock  Clock
	tokens *TokenStore
}

func NewService(tokens *TokenStore, clock Clock) *Service {
	if clock == nil {
		clock = SystemClock{}
	}

	if tokens == nil {
		tokens = NewTokenStore()
	}

	return &Service{tasks: make(map[int64]Task), nextID: 1, clock: clock, tokens: tokens}
}

func (s *Service) Tokens() *TokenStore { return s.tokens }

// Authenticate is the one place a token becomes a user.
func (s *Service) Authenticate(token string) (string, error) {
	if strings.TrimSpace(token) == "" {
		return "", ErrUnauthenticated
	}

	return s.tokens.Resolve(token)
}

func (s *Service) Create(ctx context.Context, owner, title string, dueAt time.Time) (Task, error) {
	if owner == "" {
		return Task{}, ErrUnauthenticated
	}

	title = strings.TrimSpace(title)

	switch {
	case title == "":
		return Task{}, fmt.Errorf("%w: title is required", ErrValidation)
	case utf8.RuneCountInString(title) > 140:
		return Task{}, fmt.Errorf("%w: title must be at most 140 characters", ErrValidation)
	}

	now := s.clock.Now()

	if !dueAt.IsZero() && dueAt.Before(now) {
		return Task{}, fmt.Errorf("%w: due date is in the past", ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	task := Task{
		ID:        s.nextID,
		Owner:     owner,
		Title:     title,
		DueAt:     dueAt,
		CreatedAt: now,
	}

	s.tasks[task.ID] = task
	s.nextID++

	return task, nil
}

func (s *Service) Get(ctx context.Context, owner string, id int64) (Task, error) {
	if owner == "" {
		return Task{}, ErrUnauthenticated
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	task, found := s.tasks[id]
	if !found {
		return Task{}, fmt.Errorf("task %d: %w", id, ErrNotFound)
	}

	if task.Owner != owner {
		return Task{}, fmt.Errorf("task %d: %w", id, ErrForbidden)
	}

	return task, nil
}

func (s *Service) List(ctx context.Context, owner string, includeDone bool) ([]Task, error) {
	if owner == "" {
		return nil, ErrUnauthenticated
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	matched := make([]Task, 0, len(s.tasks))

	for _, task := range s.tasks {
		if task.Owner != owner {
			continue
		}

		if task.Done && !includeDone {
			continue
		}

		matched = append(matched, task)
	}

	// Deterministic order. Sorting by CreatedAt alone was the cause of the
	// flaky test documented in flaky_test.go: two tasks created inside the
	// same clock tick compared equal, and map iteration decided the order.
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].ID < matched[j].ID
		}

		return matched[i].CreatedAt.Before(matched[j].CreatedAt)
	})

	return matched, nil
}

func (s *Service) Complete(ctx context.Context, owner string, id int64) (Task, error) {
	task, err := s.Get(ctx, owner, id)
	if err != nil {
		return Task{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	task.Done = true
	s.tasks[id] = task

	return task, nil
}

func (s *Service) Delete(ctx context.Context, owner string, id int64) error {
	if _, err := s.Get(ctx, owner, id); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.tasks, id)

	return nil
}

// Overdue uses the injected clock, so the test does not have to wait for a
// deadline to pass.
func (s *Service) Overdue(ctx context.Context, owner string) ([]Task, error) {
	tasks, err := s.List(ctx, owner, false)
	if err != nil {
		return nil, err
	}

	now := s.clock.Now()
	overdue := make([]Task, 0, len(tasks))

	for _, task := range tasks {
		if !task.DueAt.IsZero() && task.DueAt.Before(now) {
			overdue = append(overdue, task)
		}
	}

	return overdue, nil
}
