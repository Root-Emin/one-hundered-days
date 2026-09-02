// Command linkr is the capstone service.
//
//	go run ./Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-98/cmd/linkr
//	go run ./Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-98/cmd/linkr -issue-key ada      # print a new API key and exit
//
// Day 97: links, redirects and API-key auth, on SQLite.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-98/internal/auth"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-98/internal/cache"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-98/internal/config"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-98/internal/httpserver"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-98/internal/metrics"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-98/internal/ratelimit"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-98/internal/service"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-98/internal/store"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-98/internal/worker"
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

	redirectCache := cache.New(cache.DefaultOptions(cfg.CacheTTL))
	collectors := metrics.New()

	svc.SetCache(redirectCache, collectors)

	api := httpserver.NewAPI(svc, cfg.BaseURL, logger)
	api.SetObserver(collectors)

	server := httpserver.New(cfg, logger)
	server.Mount(api, dataStore)
	server.SetRateLimiter(ratelimit.New(cfg.RateLimitPerMinute))
	server.SetMetrics(collectors)
	server.AddChecker("database", dataStore)

	// The click worker runs in this process. It is a goroutine rather than a
	// second binary because the outbox is a table in the same database - and
	// splitting it out is a deployment change, not a code change.
	clickWorker := worker.New(dataStore, logger, worker.Options{
		Interval: time.Second,
		Observer: collectors,
	})

	workerCtx, stopWorker := context.WithCancel(context.Background())

	workerDone := make(chan struct{})

	go func() {
		defer close(workerDone)

		clickWorker.Run(workerCtx)
	}()

	// The private listener for /metrics: localhost only, and never the public
	// mux. See config.Validate, which refuses a public one in production.
	metricsServer := startMetricsServer(cfg, server, logger)

	server.MarkReady()

	err = server.Start(ctx)

	// Shut the worker down AFTER the HTTP server has drained, so the events
	// written by the last few redirects are aggregated before the process
	// exits.
	if drainErr := clickWorker.Drain(workerCtx); drainErr != nil {
		logger.Error("final outbox drain", slog.String("error", drainErr.Error()))
	}

	stopWorker()

	<-workerDone

	if metricsServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		if shutdownErr := metricsServer.Shutdown(shutdownCtx); shutdownErr != nil {
			logger.Error("shutdown metrics server", slog.String("error", shutdownErr.Error()))
		}
	}

	return err
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

// startMetricsServer runs /metrics on its own listener.
//
// A failure to bind is logged and tolerated: losing metrics is a degraded
// service, and refusing to start over it would turn an observability problem
// into an outage.
func startMetricsServer(cfg config.Config, server *httpserver.Server, logger *slog.Logger) *http.Server {
	if cfg.MetricsAddr == "" {
		return nil
	}

	metricsServer := &http.Server{
		Addr:              cfg.MetricsAddr,
		Handler:           server.MetricsHandler(),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
	}

	go func() {
		logger.Info("metrics listening", slog.String("addr", cfg.MetricsAddr))

		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics server", slog.String("error", err.Error()))
		}
	}()

	return metricsServer
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
