package todo_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-70/internal/testsupport"
	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-70/internal/todo"
)

/*
Unit tests for the service layer: fast, no HTTP, no sockets.

These run on every save. The HTTP-level suite lives in internal/httpapi, and
the slow end-to-end one is behind the 'integration' build tag.
*/

func TestCreate(t *testing.T) {
	t.Parallel()

	service, clock := testsupport.NewService(t)

	task, err := service.Create(context.Background(), "ada", "  write tests  ", time.Time{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if task.Title != "write tests" {
		t.Fatalf("title = %q, want it trimmed", task.Title)
	}

	if !task.CreatedAt.Equal(clock.Current) {
		t.Fatalf("created_at = %s, want the injected clock", task.CreatedAt)
	}
}

func TestCreateValidation(t *testing.T) {
	t.Parallel()

	service, _ := testsupport.NewService(t)
	ctx := context.Background()

	tests := []struct {
		name  string
		owner string
		title string
		due   time.Time
		want  error
	}{
		{"no owner", "", "title", time.Time{}, todo.ErrUnauthenticated},
		{"empty title", "ada", "   ", time.Time{}, todo.ErrValidation},
		{"long title", "ada", strings.Repeat("a", 141), time.Time{}, todo.ErrValidation},
		{"due in the past", "ada", "title", testsupport.Reference.Add(-time.Hour), todo.ErrValidation},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.Create(ctx, test.owner, test.title, test.due)

			if !errors.Is(err, test.want) {
				t.Fatalf("err = %v, want %v", err, test.want)
			}
		})
	}
}

func TestOwnership(t *testing.T) {
	t.Parallel()

	service, _ := testsupport.NewService(t)
	tasks := testsupport.SeedTasks(t, service)

	ctx := context.Background()

	alansTask := tasks[2]

	if _, err := service.Get(ctx, "ada", alansTask.ID); !errors.Is(err, todo.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}

	if err := service.Delete(ctx, "ada", alansTask.ID); !errors.Is(err, todo.ErrForbidden) {
		t.Fatalf("delete err = %v, want ErrForbidden", err)
	}

	if _, err := service.Get(ctx, "ada", 9999); !errors.Is(err, todo.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestAuthenticate(t *testing.T) {
	t.Parallel()

	service, _ := testsupport.NewService(t)

	owner, err := service.Authenticate(testsupport.AdaToken)
	if err != nil || owner != "ada" {
		t.Fatalf("owner = %q (err=%v), want ada", owner, err)
	}

	for _, token := range []string{"", "   ", "made-up-token"} {
		if _, err := service.Authenticate(token); !errors.Is(err, todo.ErrUnauthenticated) {
			t.Fatalf("token %q err = %v, want ErrUnauthenticated", token, err)
		}
	}
}

func TestCompleteAndList(t *testing.T) {
	t.Parallel()

	service, _ := testsupport.NewService(t)
	tasks := testsupport.SeedTasks(t, service)

	ctx := context.Background()

	if _, err := service.Complete(ctx, "ada", tasks[0].ID); err != nil {
		t.Fatalf("complete: %v", err)
	}

	open, err := service.List(ctx, "ada", false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(open) != 1 {
		t.Fatalf("open tasks = %d, want 1", len(open))
	}

	all, err := service.List(ctx, "ada", true)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(all) != 2 {
		t.Fatalf("all tasks = %d, want 2", len(all))
	}
}

// TestOverdueUsesTheClock: time passes in one line instead of one day.
func TestOverdueUsesTheClock(t *testing.T) {
	t.Parallel()

	service, clock := testsupport.NewService(t)

	testsupport.SeedTasks(t, service)

	ctx := context.Background()

	overdue, err := service.Overdue(ctx, "ada")
	if err != nil {
		t.Fatalf("overdue: %v", err)
	}

	if len(overdue) != 0 {
		t.Fatalf("overdue = %d, want 0", len(overdue))
	}

	clock.Advance(48 * time.Hour)

	if overdue, err = service.Overdue(ctx, "ada"); err != nil || len(overdue) != 1 {
		t.Fatalf("overdue after two days = %d (err=%v), want 1", len(overdue), err)
	}
}
