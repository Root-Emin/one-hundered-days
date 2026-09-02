// Command api is the binary the images in ./dockerfiles build.
//
// It exists mainly to be compiled with different flags and measured, so it
// prints its build metadata and exits when asked.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
)

// Injected at link time with -ldflags "-X main.version=...".
var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

type buildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
	VCS       string `json:"vcs,omitempty"`
	LDFlags   string `json:"ldflags,omitempty"`
}

func currentBuild() buildInfo {
	info := buildInfo{
		Version:   version,
		Commit:    commit,
		BuildTime: buildTime,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}

	// Go embeds VCS information automatically when building inside a git
	// checkout, which is a second source of truth for "what is this binary?".
	if build, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range build.Settings {
			switch setting.Key {
			case "vcs.revision":
				info.VCS = setting.Value
			case "-ldflags":
				// Recorded by the toolchain, so the binary can report how it
				// was linked without being told.
				info.LDFlags = setting.Value
			}
		}
	}

	return info
}

func main() {
	printVersion := flag.Bool("version", false, "print build metadata as JSON and exit")

	flag.Parse()

	if *printVersion {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")

		if err := encoder.Encode(currentBuild()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
			logger.Error("encode", slog.String("error", err.Error()))
		}
	})

	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(currentBuild()); err != nil {
			logger.Error("encode", slog.String("error", err.Error()))
		}
	})

	server := &http.Server{
		Addr:              ":" + envOr("PORT", "8080"),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("starting", slog.String("addr", server.Addr), slog.String("version", version))

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("listen", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	<-shutdown

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("shutdown", slog.String("error", err.Error()))
	}
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
