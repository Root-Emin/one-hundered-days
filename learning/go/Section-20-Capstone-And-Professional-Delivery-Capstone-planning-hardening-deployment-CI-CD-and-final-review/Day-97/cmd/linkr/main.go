// Command linkr is the capstone service.
//
//	go run ./Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-97/cmd/linkr
//	go run ./Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-97/cmd/linkr -issue-key ada      # print a new API key and exit
//
// Day 97: links, redirects and API-key auth, on SQLite.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-97/internal/auth"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-97/internal/config"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-97/internal/httpserver"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-97/internal/service"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-97/internal/store"
)

func main() {
	if err := run(os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// run is main's testable half: everything it touches arrives as an argument.
func run(args []string, getenv func(string) string, output *os.File) error {
	// -issue-key is handled before the config parser sees it: it is an
	// administrative command that happens to live in the same binary, not a
	// setting.
	if owner, found := issueKeyOwner(args); found {
		return issueKey(args, getenv, output, owner)
	}

	cfg, err := config.Load(args, getenv)
	if err != nil {
		return err
	}

	logger := newLogger(cfg, output)

	logger.Info("starting", slog.String("config", cfg.String()))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dataStore, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}

	defer func() {
		if err := dataStore.Close(); err != nil {
			logger.Error("close store", slog.String("error", err.Error()))
		}
	}()

	// Migrations run before the listener opens, so the service is never
	// reachable with a schema it cannot use.
	applied, err := store.Migrate(ctx, dataStore.DB())
	if err != nil {
		return err
	}

	logger.Info("migrations applied", slog.Int("count", len(applied)))

	svc := service.New(dataStore, logger)

	server := httpserver.New(cfg, logger)
	server.Mount(httpserver.NewAPI(svc, cfg.BaseURL, logger), dataStore)
	server.AddChecker("database", dataStore)

	server.MarkReady()

	return server.Start(ctx)
}

// issueKeyOwner finds the -issue-key flag without parsing the rest.
func issueKeyOwner(args []string) (string, bool) {
	for i, arg := range args {
		if arg == "-issue-key" || arg == "--issue-key" {
			if i+1 < len(args) {
				return args[i+1], true
			}

			return "", true
		}

		if owner, found := strings.CutPrefix(arg, "-issue-key="); found {
			return owner, true
		}
	}

	return "", false
}

// issueKey creates an API key and prints it once.
func issueKey(args []string, getenv func(string) string, output *os.File, owner string) error {
	if owner == "" {
		return errors.New("-issue-key needs an owner, e.g. -issue-key ada")
	}

	// Drop the flag and its value so the config parser does not see them.
	remaining := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		if args[i] == "-issue-key" || args[i] == "--issue-key" {
			i++

			continue
		}

		if strings.HasPrefix(args[i], "-issue-key=") {
			continue
		}

		remaining = append(remaining, args[i])
	}

	cfg, err := config.Load(remaining, getenv)
	if err != nil {
		return err
	}

	ctx := context.Background()

	dataStore, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}

	defer func() {
		if err := dataStore.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "close store:", err)
		}
	}()

	if _, err := store.Migrate(ctx, dataStore.DB()); err != nil {
		return err
	}

	generated, err := auth.Generate()
	if err != nil {
		return err
	}

	if err := dataStore.CreateAPIKey(ctx, store.APIKey{
		ID:        generated.ID,
		Owner:     owner,
		Hash:      generated.Hash,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}

	// Printed once, and only here. The plaintext is not stored, not logged,
	// and cannot be recovered - which is the property that makes a leaked
	// database survivable.
	fmt.Fprintf(output, "owner:  %s\nkey id: %s\nkey:    %s\n\n", owner, generated.ID, generated.Plaintext)
	fmt.Fprintln(output, "This key is shown once. Store it now; it cannot be recovered.")

	return nil
}

func newLogger(cfg config.Config, output *os.File) *slog.Logger {
	level := slog.LevelInfo

	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	options := &slog.HandlerOptions{Level: level}

	if cfg.Production() {
		return slog.New(slog.NewJSONHandler(output, options))
	}

	return slog.New(slog.NewTextHandler(output, options))
}
