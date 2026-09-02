// Package notes is the MVP's service layer.
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
	ErrValidation = errors.New("invalid note")
	ErrNotFound   = errors.New("note not found")
)

type Note struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type Service struct {
	mu     sync.RWMutex
	notes  map[int64]Note
	nextID int64
}

func NewService() *Service {
	return &Service{notes: make(map[int64]Note), nextID: 1}
}

func (s *Service) Create(ctx context.Context, title, body string) (Note, error) {
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

	note := Note{ID: s.nextID, Title: title, Body: body, CreatedAt: time.Now().UTC()}

	s.notes[note.ID] = note
	s.nextID++

	return note, nil
}

func (s *Service) ByID(ctx context.Context, id int64) (Note, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	note, found := s.notes[id]
	if !found {
		return Note{}, fmt.Errorf("note %d: %w", id, ErrNotFound)
	}

	return note, nil
}

func (s *Service) List(ctx context.Context, limit int) ([]Note, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	notes := make([]Note, 0, len(s.notes))

	for _, note := range s.notes {
		notes = append(notes, note)
	}

	sort.Slice(notes, func(i, j int) bool { return notes[i].ID > notes[j].ID })

	if len(notes) > limit {
		notes = notes[:limit]
	}

	return notes, nil
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, found := s.notes[id]; !found {
		return fmt.Errorf("note %d: %w", id, ErrNotFound)
	}

	delete(s.notes, id)

	return nil
}

func (s *Service) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.notes)
}
