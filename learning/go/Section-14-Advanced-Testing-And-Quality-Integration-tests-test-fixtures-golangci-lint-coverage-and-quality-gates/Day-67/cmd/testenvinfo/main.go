// Command testenvinfo reports which test backend this machine will use, and
// why. Running it is faster than reading skip messages out of a test log.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"context"
)

/*
Day 67 - Advanced Testing & Quality: Test Fixtures and Containers

Tasks covered:

 1. testcontainers-go starts a real Postgres when Docker is available
    (internal/testenv/container.go)
 2. Setup and teardown centralised in one helper: internal/testenv.New(t)
 3. Parallel isolation: a schema per test on Postgres, a file per test on
    SQLite - no shared rows, no port collisions
 4. Teardown through t.Cleanup, so it runs even when a test fails or panics

Run:

	go run ./cmd/testenvinfo

Test:

	go test ./...                     # Postgres if Docker is up, else SQLite
	TESTCONTAINERS=off go test ./...  # force SQLite (fast, no Docker)
	TESTCONTAINERS=on  go test ./...  # require Postgres; a missing Docker
	                                  # fails instead of skipping - use this
	                                  # in CI so coverage cannot quietly drop
*/

func main() {
	fmt.Println("\nTest environment")
	fmt.Println(strings.Repeat("-", 72))

	setting := strings.ToLower(os.Getenv("TESTCONTAINERS"))

	if setting == "" {
		setting = "(unset: auto-detect)"
	}

	fmt.Printf("  TESTCONTAINERS : %s\n", setting)

	docker := dockerReachable()

	fmt.Printf("  docker daemon  : %s\n", yesNo(docker))

	switch {
	case setting == "off" || setting == "0" || setting == "false":
		fmt.Println("  backend        : SQLite (forced)")

	case setting == "on" || setting == "1" || setting == "true":
		if docker {
			fmt.Println("  backend        : Postgres in a container (required, available)")
		} else {
			fmt.Println("  backend        : Postgres REQUIRED but Docker is unreachable -> tests will fail")
		}

	case docker:
		fmt.Println("  backend        : Postgres in a container (auto-detected)")

	default:
		fmt.Println("  backend        : SQLite (Docker unavailable; Postgres-only tests will skip)")
	}

	fmt.Println("\nWhy both")
	fmt.Println(strings.Repeat("-", 72))

	rows := []struct {
		aspect   string
		sqlite   string
		postgres string
	}{
		{"Startup cost", "microseconds", "seconds, once per test binary"},
		{"Needs Docker", "no", "yes"},
		{"SQL dialect", "close, not identical", "exactly production"},
		{"Constraints", "enforced", "enforced, with real error codes"},
		{"Concurrency", "one writer at a time", "real MVCC behaviour"},
		{"Isolation unit", "a file per test", "a schema per test"},
		{"Good for", "fast inner loop", "the suite that must be believed"},
	}

	fmt.Printf("%-16s %-24s %s\n", "ASPECT", "SQLITE", "POSTGRES CONTAINER")

	for _, row := range rows {
		fmt.Printf("%-16s %-24s %s\n", row.aspect, row.sqlite, row.postgres)
	}

	fmt.Println("\n  The suite is written against neither: internal/testenv decides, and")
	fmt.Println("  the tests only ask for a store. A test that genuinely needs Postgres")
	fmt.Println("  calls RequirePostgres and skips with an explanation elsewhere.")
	fmt.Println()
}

func dockerReachable() bool {
	path, err := exec.LookPath("docker")
	if err != nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	return exec.CommandContext(ctx, path, "info").Run() == nil
}

func yesNo(value bool) string {
	if value {
		return "reachable"
	}

	return "not reachable"
}
