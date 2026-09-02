// Package service implements the use cases of the application: create a task,
// move it through its statuses, list what is overdue.
//
// It depends on the domain (inward) and on the domain's repository interface.
// It has no idea whether it is being called by an HTTP handler, a gRPC server
// or a test.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-56/internal/domain"
)

// Clock is injected so "what time is it?" can be controlled in tests. A
// service that calls time.Now() directly cannot be tested for anything that
// depends on the date.
type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type TaskService struct {
	tasks domain.TaskRepository
	clock Clock
}

// NewTaskService states every dependency in its signature. Nothing is
// discovered from a global at call time.
func NewTaskService(tasks domain.TaskRepository, clock Clock) *TaskService {
	if clock == nil {
		clock = SystemClock{}
	}

	return &TaskService{tasks: tasks, clock: clock}
}

// CreateTask is a use case, not a database call: it normalises input, applies
// the domain rules, checks the uniqueness rule and only then persists.
func (s *TaskService) CreateTask(ctx context.Context, reference, title, assignee string, dueAt time.Time) (domain.Task, error) {
	task := domain.Task{
		Reference: strings.ToUpper(strings.TrimSpace(reference)),
		Title:     strings.TrimSpace(title),
		Assignee:  strings.TrimSpace(assignee),
		Status:    domain.StatusTodo,
		DueAt:     dueAt,
		CreatedAt: s.clock.Now(),
	}

	if err := task.Validate(); err != nil {
		return domain.Task{}, err
	}

	if !dueAt.IsZero() && dueAt.Before(s.clock.Now()) {
		return domain.Task{}, fmt.Errorf("%w: due date is in the past", domain.ErrValidation)
	}

	existing, err := s.tasks.ByReference(ctx, task.Reference)

	switch {
	case err == nil:
		return domain.Task{}, fmt.Errorf("%w: reference %s belongs to task %d",
			domain.ErrConflict, task.Reference, existing.ID)

	case !errors.Is(err, domain.ErrNotFound):
		return domain.Task{}, fmt.Errorf("create task %s: %w", task.Reference, err)
	}

	return s.tasks.Create(ctx, task)
}

func (s *TaskService) Task(ctx context.Context, id int64) (domain.Task, error) {
	return s.tasks.ByID(ctx, id)
}

func (s *TaskService) List(ctx context.Context, status domain.Status, limit int) ([]domain.Task, error) {
	if status != "" && !status.Valid() {
		return nil, fmt.Errorf("%w: status %q is not valid", domain.ErrValidation, status)
	}

	if limit <= 0 || limit > 100 {
		limit = 20
	}

	return s.tasks.List(ctx, status, limit)
}

// Advance encodes the workflow rule. The allowed transitions are a business
// decision, so they live above storage and below transport.
func (s *TaskService) Advance(ctx context.Context, id int64, target domain.Status) (domain.Task, error) {
	if !target.Valid() {
		return domain.Task{}, fmt.Errorf("%w: status %q is not valid", domain.ErrValidation, target)
	}

	task, err := s.tasks.ByID(ctx, id)
	if err != nil {
		return domain.Task{}, err
	}

	if !allowedTransition(task.Status, target) {
		return domain.Task{}, fmt.Errorf("%w: cannot move a task from %s to %s",
			domain.ErrValidation, task.Status, target)
	}

	return s.tasks.UpdateStatus(ctx, id, target)
}

func allowedTransition(from, to domain.Status) bool {
	transitions := map[domain.Status][]domain.Status{
		domain.StatusTodo:  {domain.StatusDoing},
		domain.StatusDoing: {domain.StatusDone, domain.StatusTodo},
		domain.StatusDone:  {},
	}

	for _, candidate := range transitions[from] {
		if candidate == to {
			return true
		}
	}

	return false
}

// Overdue uses the injected clock, so a test can ask "what is overdue next
// Tuesday?" without waiting for Tuesday.
func (s *TaskService) Overdue(ctx context.Context, limit int) ([]domain.Task, error) {
	tasks, err := s.tasks.List(ctx, "", limit)
	if err != nil {
		return nil, err
	}

	now := s.clock.Now()
	overdue := make([]domain.Task, 0, len(tasks))

	for _, task := range tasks {
		if task.Overdue(now) {
			overdue = append(overdue, task)
		}
	}

	return overdue, nil
}

func (s *TaskService) Delete(ctx context.Context, id int64) error {
	return s.tasks.Delete(ctx, id)
}
