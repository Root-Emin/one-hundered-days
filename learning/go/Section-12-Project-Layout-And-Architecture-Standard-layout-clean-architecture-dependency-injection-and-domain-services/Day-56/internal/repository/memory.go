// Package repository implements the storage interfaces declared in the domain
// package.
//
// It is an outer layer: it imports the domain, and the domain knows nothing
// about it. Swapping this in-memory implementation for Postgres changes this
// package and one line in cmd/api/main.go - nothing else.
package repository

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-56/internal/domain"
)

type MemoryTaskRepository struct {
	mu     sync.RWMutex
	tasks  map[int64]domain.Task
	nextID int64
}

func NewMemoryTaskRepository() *MemoryTaskRepository {
	return &MemoryTaskRepository{
		tasks:  make(map[int64]domain.Task),
		nextID: 1,
	}
}

// Compile-time check that the contract still holds.
var _ domain.TaskRepository = (*MemoryTaskRepository)(nil)

func (r *MemoryTaskRepository) Create(ctx context.Context, task domain.Task) (domain.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, existing := range r.tasks {
		if existing.Reference == task.Reference {
			return domain.Task{}, fmt.Errorf("create task %s: %w", task.Reference, domain.ErrConflict)
		}
	}

	task.ID = r.nextID

	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now().UTC()
	}

	r.tasks[task.ID] = task
	r.nextID++

	return task, nil
}

func (r *MemoryTaskRepository) ByID(ctx context.Context, id int64) (domain.Task, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	task, found := r.tasks[id]
	if !found {
		return domain.Task{}, fmt.Errorf("task %d: %w", id, domain.ErrNotFound)
	}

	return task, nil
}

func (r *MemoryTaskRepository) ByReference(ctx context.Context, reference string) (domain.Task, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, task := range r.tasks {
		if task.Reference == reference {
			return task, nil
		}
	}

	return domain.Task{}, fmt.Errorf("task %s: %w", reference, domain.ErrNotFound)
}

func (r *MemoryTaskRepository) List(ctx context.Context, status domain.Status, limit int) ([]domain.Task, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	matched := make([]domain.Task, 0, len(r.tasks))

	for _, task := range r.tasks {
		if status != "" && task.Status != status {
			continue
		}

		matched = append(matched, task)
	}

	sort.Slice(matched, func(i, j int) bool { return matched[i].ID < matched[j].ID })

	if len(matched) > limit {
		matched = matched[:limit]
	}

	return matched, nil
}

func (r *MemoryTaskRepository) UpdateStatus(ctx context.Context, id int64, status domain.Status) (domain.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	task, found := r.tasks[id]
	if !found {
		return domain.Task{}, fmt.Errorf("task %d: %w", id, domain.ErrNotFound)
	}

	task.Status = status
	r.tasks[id] = task

	return task, nil
}

func (r *MemoryTaskRepository) Delete(ctx context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, found := r.tasks[id]; !found {
		return fmt.Errorf("task %d: %w", id, domain.ErrNotFound)
	}

	delete(r.tasks, id)

	return nil
}
