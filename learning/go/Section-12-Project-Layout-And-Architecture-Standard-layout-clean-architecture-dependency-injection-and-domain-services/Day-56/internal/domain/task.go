// Package domain holds the business concepts of the service: the types, the
// rules they enforce, and the errors they raise.
//
// It is the innermost layer. It imports no HTTP package, no SQL driver and no
// package of this service - which is what makes the rules testable in
// isolation and survivable across a change of database or transport.
package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrNotFound   = errors.New("task not found")
	ErrValidation = errors.New("invalid task")
	ErrConflict   = errors.New("task already exists")
)

type Status string

const (
	StatusTodo  Status = "todo"
	StatusDoing Status = "doing"
	StatusDone  Status = "done"
)

func (s Status) Valid() bool {
	switch s {
	case StatusTodo, StatusDoing, StatusDone:
		return true
	default:
		return false
	}
}

// Task is the aggregate this service is about.
type Task struct {
	ID        int64
	Reference string
	Title     string
	Status    Status
	Assignee  string
	DueAt     time.Time
	CreatedAt time.Time
}

// Validate is the single definition of "a valid task". Keeping it here rather
// than in a handler means every entry point - HTTP today, a CLI or a queue
// consumer tomorrow - gets the same rules for free.
func (t Task) Validate() error {
	switch {
	case strings.TrimSpace(t.Reference) == "":
		return fmt.Errorf("%w: reference is required", ErrValidation)

	case len(t.Reference) > 32:
		return fmt.Errorf("%w: reference must be at most 32 characters", ErrValidation)

	case strings.TrimSpace(t.Title) == "":
		return fmt.Errorf("%w: title is required", ErrValidation)

	case len(t.Title) > 200:
		return fmt.Errorf("%w: title must be at most 200 characters", ErrValidation)

	case !t.Status.Valid():
		return fmt.Errorf("%w: status %q is not one of todo, doing, done", ErrValidation, t.Status)
	}

	return nil
}

// Overdue is business logic, so it lives on the domain type. A handler asking
// "is DueAt before now?" would be the same rule, written in the wrong place
// and duplicated the second time somebody needs it.
func (t Task) Overdue(now time.Time) bool {
	return t.Status != StatusDone && !t.DueAt.IsZero() && t.DueAt.Before(now)
}

// TaskRepository is declared here, next to its consumers, not next to any
// implementation. The service depends on this interface; the repository
// package depends on the domain. Dependencies point inward.
type TaskRepository interface {
	Create(ctx context.Context, task Task) (Task, error)
	ByID(ctx context.Context, id int64) (Task, error)
	ByReference(ctx context.Context, reference string) (Task, error)
	List(ctx context.Context, status Status, limit int) ([]Task, error)
	UpdateStatus(ctx context.Context, id int64, status Status) (Task, error)
	Delete(ctx context.Context, id int64) error
}
