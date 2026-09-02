// Package tasks is the MVP this day reviews, documents and releases.
//
// It is small on purpose - the subject of Day 95 is the process, and the
// process needs something real to be applied to. What it does have is the
// shape a reviewer looks for: one invariant, typed errors, no transport
// concerns, and no global state.
//
// # Invariant
//
// A task moves todo -> doing -> done, and never backwards. Reopening is a new
// task with a link to the old one, so the history of what actually happened
// survives - a status field that can move both ways loses it.
package tasks

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Status is where a task is in its life.
type Status string

// The three states, in order.
const (
	// Todo is a task nobody has started.
	Todo Status = "todo"
	// Doing is a task in progress.
	Doing Status = "doing"
	// Done is a finished task.
	Done Status = "done"
)

// Sentinel errors. These are the API; the message text is not.
var (
	// ErrNotFound means no task has that id.
	ErrNotFound = errors.New("task not found")
	// ErrInvalidTransition means the requested status change moves backwards.
	ErrInvalidTransition = errors.New("invalid status transition")
	// ErrInvalidTask means a field failed validation.
	ErrInvalidTask = errors.New("invalid task")
)

// Task is one unit of work.
type Task struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Status    Status    `json:"status"`
	Assignee  string    `json:"assignee,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// order ranks the states, so a transition can be checked with a comparison
// rather than a table of pairs that grows quadratically.
var order = map[Status]int{Todo: 0, Doing: 1, Done: 2}

// CanTransition reports whether a task may move from one status to another.
//
// Forward only, and never to the same state: "mark it doing" twice is almost
// always a double submit, and silently accepting it hides the bug.
func CanTransition(from, to Status) bool {
	fromRank, fromKnown := order[from]
	toRank, toKnown := order[to]

	return fromKnown && toKnown && toRank > fromRank
}

// Store holds tasks in memory.
//
// The zero value is not usable; call New.
type Store struct {
	mu     sync.RWMutex
	tasks  map[int64]Task
	nextID int64
	now    func() time.Time
}

// New returns an empty Store.
func New() *Store {
	return &Store{tasks: make(map[int64]Task), nextID: 1, now: time.Now}
}

// SetClock replaces the time source, for tests that need predictable stamps.
func (s *Store) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.now = now
}

// Create adds a task in the todo state.
//
// It returns ErrInvalidTask if the title is empty or longer than 200
// characters.
func (s *Store) Create(title, assignee string) (Task, error) {
	title = strings.TrimSpace(title)

	switch {
	case title == "":
		return Task{}, fmt.Errorf("%w: title is required", ErrInvalidTask)
	case len(title) > 200:
		return Task{}, fmt.Errorf("%w: title is longer than 200 characters", ErrInvalidTask)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().UTC()

	task := Task{
		ID:        s.nextID,
		Title:     title,
		Status:    Todo,
		Assignee:  strings.TrimSpace(assignee),
		CreatedAt: now,
		UpdatedAt: now,
	}

	s.tasks[task.ID] = task
	s.nextID++

	return task, nil
}

// Get returns one task, or ErrNotFound.
func (s *Store) Get(id int64) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, found := s.tasks[id]
	if !found {
		return Task{}, fmt.Errorf("task %d: %w", id, ErrNotFound)
	}

	return task, nil
}

// Advance moves a task to a later status.
//
// It returns ErrInvalidTransition, and changes nothing, if the move is
// backwards or to the same state.
func (s *Store) Advance(id int64, to Status) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, found := s.tasks[id]
	if !found {
		return Task{}, fmt.Errorf("task %d: %w", id, ErrNotFound)
	}

	if !CanTransition(task.Status, to) {
		return Task{}, fmt.Errorf("task %d: %w: %s -> %s", id, ErrInvalidTransition, task.Status, to)
	}

	task.Status = to
	task.UpdatedAt = s.now().UTC()

	s.tasks[id] = task

	return task, nil
}

// List returns tasks, optionally filtered by status, ordered by id.
//
// The order is defined so the response is stable: a list that comes back in a
// different order every call breaks caching, pagination and every test that
// touches it.
func (s *Store) List(status Status) []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tasks := make([]Task, 0, len(s.tasks))

	for _, task := range s.tasks {
		if status != "" && task.Status != status {
			continue
		}

		tasks = append(tasks, task)
	}

	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })

	return tasks
}

// Counts returns how many tasks are in each status.
func (s *Store) Counts() map[Status]int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	counts := map[Status]int{Todo: 0, Doing: 0, Done: 0}

	for _, task := range s.tasks {
		counts[task.Status]++
	}

	return counts
}
