package task

import (
	"errors"
	"strings"
)

// ============================================================
// MODEL
// ============================================================

type Task struct {
	ID   int
	Name string

	done bool
}

// ============================================================
// ERRORS
// ============================================================

var (
	ErrEmptyTaskName = errors.New("task name cannot be empty")
	ErrAlreadyDone   = errors.New("task is already completed")
)

// ============================================================
// CREATE TASK
// EXPORTED BEHAVIOR
// ============================================================

func CreateTask(id int, name string) (Task, error) {
	name = strings.TrimSpace(name)

	if name == "" {
		return Task{}, ErrEmptyTaskName
	}

	return Task{
		ID:   id,
		Name: name,
		done: false,
	}, nil
}

// ============================================================
// COMPLETE
// EXPORTED BEHAVIOR
// ============================================================

func (t *Task) Complete() error {
	if t.done {
		return ErrAlreadyDone
	}

	t.done = true

	return nil
}

// ============================================================
// IS DONE
// EXPORTED BEHAVIOR
// ============================================================

func (t Task) IsDone() bool {
	return t.done
}
