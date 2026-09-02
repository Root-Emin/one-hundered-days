package greeting_test

import (
	"testing"
	"time"

	greeting "example.com/onehundredday/Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-78/internal/greeting"
)

// The tests CI runs. Deliberately ordinary: the lesson is the pipeline, not
// the assertions.
func TestGreet(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		hour int
		name string
		want string
	}{
		{3, "Ada", "Still up, Ada?"},
		{9, "Ada", "Good morning, Ada"},
		{14, "Ada", "Good afternoon, Ada"},
		{21, "Ada", "Good evening, Ada"},
		{9, "  ", "Good morning, there"},
	}

	for _, test := range tests {
		got := greeting.Greet(test.name, day.Add(time.Duration(test.hour)*time.Hour))

		if got != test.want {
			t.Errorf("Greet(%q, %02d:00) = %q, want %q", test.name, test.hour, got, test.want)
		}
	}
}
