package tasks_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-95/internal/tasks"
)

func newStore(t *testing.T) *tasks.Store {
	t.Helper()

	store := tasks.New()

	fixed := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return fixed })

	return store
}

func TestCreateStartsInTodo(t *testing.T) {
	store := newStore(t)

	task, err := store.Create("write the release notes", "ada")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if task.Status != tasks.Todo {
		t.Errorf("status = %s, want todo", task.Status)
	}

	if task.ID != 1 || task.CreatedAt.IsZero() {
		t.Errorf("task = %+v", task)
	}
}

func TestCreateValidates(t *testing.T) {
	store := newStore(t)

	for name, title := range map[string]string{
		"empty":      "",
		"whitespace": "   ",
		"too long":   string(make([]byte, 201)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := store.Create(title, ""); !errors.Is(err, tasks.ErrInvalidTask) {
				t.Errorf("Create(%q) = %v, want ErrInvalidTask", name, err)
			}
		})
	}
}

// The invariant the package exists for: forward only.
func TestCannotMoveBackwards(t *testing.T) {
	store := newStore(t)

	task, err := store.Create("ship it", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := store.Advance(task.ID, tasks.Doing); err != nil {
		t.Fatalf("Advance to doing: %v", err)
	}

	if _, err := store.Advance(task.ID, tasks.Todo); !errors.Is(err, tasks.ErrInvalidTransition) {
		t.Errorf("doing -> todo = %v, want ErrInvalidTransition", err)
	}

	// And the state really is unchanged.
	current, err := store.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if current.Status != tasks.Doing {
		t.Errorf("status = %s after a rejected transition, want doing", current.Status)
	}
}

// A repeated advance is almost always a double submit; accepting it silently
// would hide the bug in the caller.
func TestAdvancingToTheSameStateIsRejected(t *testing.T) {
	store := newStore(t)

	task, _ := store.Create("ship it", "")

	if _, err := store.Advance(task.ID, tasks.Doing); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	if _, err := store.Advance(task.ID, tasks.Doing); !errors.Is(err, tasks.ErrInvalidTransition) {
		t.Errorf("doing -> doing = %v, want ErrInvalidTransition", err)
	}
}

// Skipping a state forward is allowed: a task can go straight from todo to
// done, because plenty of work is finished before anyone marks it started.
func TestCanSkipForward(t *testing.T) {
	store := newStore(t)

	task, _ := store.Create("ship it", "")

	advanced, err := store.Advance(task.ID, tasks.Done)
	if err != nil {
		t.Fatalf("todo -> done: %v", err)
	}

	if advanced.Status != tasks.Done {
		t.Errorf("status = %s, want done", advanced.Status)
	}
}

func TestCanTransition(t *testing.T) {
	cases := []struct {
		from, to tasks.Status
		want     bool
	}{
		{tasks.Todo, tasks.Doing, true},
		{tasks.Todo, tasks.Done, true},
		{tasks.Doing, tasks.Done, true},
		{tasks.Doing, tasks.Todo, false},
		{tasks.Done, tasks.Doing, false},
		{tasks.Done, tasks.Done, false},
		{tasks.Todo, "archived", false},
		{"unknown", tasks.Done, false},
	}

	for _, testCase := range cases {
		if got := tasks.CanTransition(testCase.from, testCase.to); got != testCase.want {
			t.Errorf("CanTransition(%s, %s) = %t, want %t",
				testCase.from, testCase.to, got, testCase.want)
		}
	}
}

func TestGetMissingIsNotFound(t *testing.T) {
	if _, err := newStore(t).Get(999); !errors.Is(err, tasks.ErrNotFound) {
		t.Errorf("Get(999) = %v, want ErrNotFound", err)
	}

	if _, err := newStore(t).Advance(999, tasks.Doing); !errors.Is(err, tasks.ErrNotFound) {
		t.Errorf("Advance(999) = %v, want ErrNotFound", err)
	}
}

func TestListIsOrderedAndFilterable(t *testing.T) {
	store := newStore(t)

	for i := 0; i < 5; i++ {
		if _, err := store.Create("task", ""); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	if _, err := store.Advance(2, tasks.Doing); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	all := store.List("")

	if len(all) != 5 {
		t.Fatalf("List = %d, want 5", len(all))
	}

	for i := 1; i < len(all); i++ {
		if all[i-1].ID > all[i].ID {
			t.Fatalf("out of order: %d before %d", all[i-1].ID, all[i].ID)
		}
	}

	doing := store.List(tasks.Doing)

	if len(doing) != 1 || doing[0].ID != 2 {
		t.Errorf("List(doing) = %+v, want just task 2", doing)
	}
}

func TestCounts(t *testing.T) {
	store := newStore(t)

	for i := 0; i < 3; i++ {
		if _, err := store.Create("task", ""); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	if _, err := store.Advance(1, tasks.Done); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	counts := store.Counts()

	if counts[tasks.Todo] != 2 || counts[tasks.Done] != 1 || counts[tasks.Doing] != 0 {
		t.Errorf("counts = %v", counts)
	}
}

// Concurrent advances on one task: exactly one wins, because the transition
// check and the write happen under the same lock.
func TestConcurrentAdvancesPickOneWinner(t *testing.T) {
	store := newStore(t)

	task, _ := store.Create("ship it", "")

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
	)

	for i := 0; i < 20; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			if _, err := store.Advance(task.ID, tasks.Doing); err == nil {
				mu.Lock()
				succeeded++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if succeeded != 1 {
		t.Errorf("%d advances succeeded, want exactly 1", succeeded)
	}
}
