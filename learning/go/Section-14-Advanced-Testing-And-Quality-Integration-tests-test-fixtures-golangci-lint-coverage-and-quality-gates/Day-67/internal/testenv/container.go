package testenv

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

/*
The container half of the helper, kept in its own file so the fallback path
above reads without it.

testcontainers-go starts a real Postgres in Docker, waits until it is actually
accepting connections, and hands back a connection string. Compared with a
shared development database it gives every run a clean, version-pinned engine;
compared with SQLite it gives the real SQL dialect, real constraints, and real
concurrency behaviour.

The cost is Docker: on a machine without it, everything here is skipped and the
suite falls back to SQLite.
*/

// dockerReachable is a cheap probe. `docker info` fails fast when the daemon
// is not running, which is better than waiting for a container start to time
// out on every test.
func dockerReachable() bool {
	path, err := exec.LookPath("docker")
	if err != nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	return exec.CommandContext(ctx, path, "info").Run() == nil
}

// containerDataSource starts the shared Postgres container on first use.
func containerDataSource() (string, error) {
	containerOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		// The image tag is pinned: a test suite that follows :latest changes
		// behaviour when someone else publishes a release.
		container, err := postgres.Run(ctx, "postgres:16-alpine",
			postgres.WithDatabase("testdb"),
			postgres.WithUsername("test"),
			postgres.WithPassword("test"),
			testcontainers.WithWaitStrategy(
				// Postgres logs "ready to accept connections" twice during
				// startup; waiting for the second one avoids connecting to an
				// instance that is about to restart.
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(60*time.Second),
			),
		)
		if err != nil {
			containerErr = fmt.Errorf("start postgres container: %w", err)
			return
		}

		dsn, err := container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			containerErr = fmt.Errorf("connection string: %w", err)

			terminate(container)

			return
		}

		containerDSN = dsn

		containerStop = func() {
			terminate(container)
		}
	})

	if containerErr != nil {
		return "", containerErr
	}

	return containerDSN, nil
}

func terminate(container testcontainers.Container) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := container.Terminate(ctx); err != nil {
		// Worth a log, not a panic: the test run is over either way, and
		// testcontainers' reaper removes stragglers.
		log.Printf("terminate container: %v", err)
	}
}
