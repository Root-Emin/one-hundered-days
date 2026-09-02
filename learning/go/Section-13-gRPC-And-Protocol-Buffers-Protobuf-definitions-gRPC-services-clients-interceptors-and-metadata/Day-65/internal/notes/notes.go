// Package notes is the shared service layer.
//
// Both transports call into this package, and neither transport contains a
// business rule. That is the whole point of today: two protocols, one
// implementation.
package notes

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var (
	ErrNotFound        = errors.New("note not found")
	ErrValidation      = errors.New("invalid note")
	ErrForbidden       = errors.New("not your note")
	ErrUnauthenticated = errors.New("no identity")
)

type Note struct {
	ID        int64
	OwnerID   string
	Title     string
	Body      string
	Archived  bool
	CreatedAt time.Time
}

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type Service struct {
	mu     sync.RWMutex
	notes  map[int64]Note
	nextID int64
	clock  Clock
}

func NewService(clock Clock) *Service {
	if clock == nil {
		clock = SystemClock{}
	}

	return &Service{notes: make(map[int64]Note), nextID: 1, clock: clock}
}

func (s *Service) Create(ctx context.Context, ownerID, title, body string) (Note, error) {
	if ownerID == "" {
		return Note{}, ErrUnauthenticated
	}

	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)

	switch {
	case title == "":
		return Note{}, fmt.Errorf("%w: title is required", ErrValidation)
	case utf8.RuneCountInString(title) > 200:
		return Note{}, fmt.Errorf("%w: title must be at most 200 characters", ErrValidation)
	case utf8.RuneCountInString(body) > 10_000:
		return Note{}, fmt.Errorf("%w: body must be at most 10000 characters", ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	note := Note{
		ID:        s.nextID,
		OwnerID:   ownerID,
		Title:     title,
		Body:      body,
		CreatedAt: s.clock.Now(),
	}

	s.notes[note.ID] = note
	s.nextID++

	return note, nil
}

// Get enforces ownership here, in the service, so both transports get the same
// answer without either of them knowing the rule.
func (s *Service) Get(ctx context.Context, ownerID string, id int64) (Note, error) {
	if ownerID == "" {
		return Note{}, ErrUnauthenticated
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	note, found := s.notes[id]
	if !found {
		return Note{}, fmt.Errorf("note %d: %w", id, ErrNotFound)
	}

	if note.OwnerID != ownerID {
		return Note{}, fmt.Errorf("note %d: %w", id, ErrForbidden)
	}

	return note, nil
}

func (s *Service) List(ctx context.Context, ownerID string, pageSize int32, includeArchived bool) ([]Note, int32, error) {
	if ownerID == "" {
		return nil, 0, ErrUnauthenticated
	}

	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	matched := make([]Note, 0, len(s.notes))

	for _, note := range s.notes {
		if note.OwnerID != ownerID {
			continue
		}

		if note.Archived && !includeArchived {
			continue
		}

		matched = append(matched, note)
	}

	sort.Slice(matched, func(i, j int) bool { return matched[i].ID > matched[j].ID })

	total := int32(len(matched))

	if int32(len(matched)) > pageSize {
		matched = matched[:pageSize]
	}

	return matched, total, nil
}

func (s *Service) Archive(ctx context.Context, ownerID string, id int64) (Note, error) {
	note, err := s.Get(ctx, ownerID, id)
	if err != nil {
		return Note{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	note.Archived = true
	s.notes[id] = note

	return note, nil
}

func (s *Service) Delete(ctx context.Context, ownerID string, id int64) (bool, error) {
	if ownerID == "" {
		return false, ErrUnauthenticated
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	note, found := s.notes[id]
	if !found {
		// Idempotent: deleting something already gone is success.
		return false, nil
	}

	if note.OwnerID != ownerID {
		return false, fmt.Errorf("note %d: %w", id, ErrForbidden)
	}

	delete(s.notes, id)

	return true, nil
}
