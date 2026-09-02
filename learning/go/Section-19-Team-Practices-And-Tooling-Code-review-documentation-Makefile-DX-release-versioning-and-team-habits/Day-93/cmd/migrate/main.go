// Command migrate applies, rolls back and reports database migrations.
//
//	make migrate           # up
//	make migrate-status
//	make migrate-down
//
// Or directly:
//
//	go run ./Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-93/cmd/migrate -db notes.db up
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-93/internal/assets"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-93/internal/migrate"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	command := "up"

	dsn := os.Getenv("NOTES_DB")
	if dsn == "" {
		dsn = "notes.db"
	}

	args := os.Args[1:]

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-db":
			if i+1 >= len(args) {
				return fmt.Errorf("-db needs a value")
			}

			dsn = args[i+1]
			i++

		case "up", "down", "status":
			command = args[i]

		default:
			return fmt.Errorf("unknown argument %q (use up, down or status)", args[i])
		}
	}

	migrations, err := migrate.Load(assets.Migrations, assets.MigrationsDir)
	if err != nil {
		return err
	}

	db, err := sql.Open("sqlite", "file:"+dsn+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("open %s: %w", dsn, err)
	}

	defer func() {
		if err := db.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "close:", err)
		}
	}()

	ctx := context.Background()

	switch command {
	case "up":
		applied, err := migrate.Up(ctx, db, migrations)
		if err != nil {
			return err
		}

		if len(applied) == 0 {
			fmt.Println("already up to date")

			return nil
		}

		for _, migration := range applied {
			fmt.Printf("applied %04d_%s\n", migration.Version, migration.Name)
		}

	case "down":
		rolled, err := migrate.Down(ctx, db, migrations)
		if err != nil {
			return err
		}

		fmt.Printf("rolled back %04d_%s\n", rolled.Version, rolled.Name)

	case "status":
		statuses, err := migrate.Report(ctx, db, migrations)
		if err != nil {
			return err
		}

		for _, status := range statuses {
			state := "pending"

			if status.Applied {
				state = "applied " + status.AppliedAt
			}

			fmt.Printf("%04d %-28s %s\n", status.Version, status.Name, state)
		}
	}

	return nil
}
