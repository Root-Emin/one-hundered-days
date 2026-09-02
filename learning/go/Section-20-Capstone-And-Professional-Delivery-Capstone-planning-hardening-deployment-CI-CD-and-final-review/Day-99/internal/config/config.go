// Package config loads and validates the service's configuration.
//
// Two rules, both learned the expensive way:
//
//   - validate at STARTUP, not at first use. A typo in a duration should stop
//     the process in the first second, not produce a zero timeout that shows
//     up as a mysterious hang three hours later.
//   - every value has a default that works on a laptop, so a new contributor
//     runs the service with no configuration at all.
package config

import (
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Config is everything the service needs to start.
type Config struct {
	// Addr is the public listen address.
	Addr string
	// MetricsAddr is a separate listener for /metrics and /debug/pprof.
	//
	// Separate, and bound to localhost: those endpoints expose the heap and
	// let anyone start a 30-second CPU profile on the box.
	MetricsAddr string
	// DatabaseURL is the SQLite file.
	DatabaseURL string
	// BaseURL is the origin short links are printed with.
	BaseURL string
	// Environment is "development" or "production"; it changes the log format
	// and nothing else.
	Environment string
	// LogLevel is debug, info, warn or error.
	LogLevel string

	// ReadHeaderTimeout bounds how long a client may take to send headers.
	// Without it, an idle connection holds a goroutine indefinitely - the
	// Slowloris attack, which needs no bandwidth at all.
	ReadHeaderTimeout time.Duration
	// WriteTimeout bounds a response.
	WriteTimeout time.Duration
	// IdleTimeout bounds a keep-alive connection between requests.
	IdleTimeout time.Duration
	// ShutdownTimeout is how long in-flight requests get to finish.
	ShutdownTimeout time.Duration

	// CacheTTL bounds how stale a cached redirect may be.
	CacheTTL time.Duration
	// RateLimitPerMinute is the per-API-key request budget.
	RateLimitPerMinute int
}

// Default returns a configuration that works with nothing set.
func Default() Config {
	return Config{
		Addr:               ":8096",
		MetricsAddr:        "127.0.0.1:9096",
		DatabaseURL:        "linkr.db",
		BaseURL:            "http://localhost:8096",
		Environment:        "development",
		LogLevel:           "info",
		ReadHeaderTimeout:  5 * time.Second,
		WriteTimeout:       10 * time.Second,
		IdleTimeout:        120 * time.Second,
		ShutdownTimeout:    15 * time.Second,
		CacheTTL:           60 * time.Second,
		RateLimitPerMinute: 120,
	}
}

// Load builds a configuration from defaults, the environment, and flags - in
// that order, so a flag beats an environment variable beats a default.
//
// That precedence is the conventional one and worth stating: a developer
// overriding one value on the command line should not have to reproduce the
// whole environment.
func Load(args []string, getenv func(string) string) (Config, error) {
	config := Default()

	// The environment first, so flags can override it.
	config.Addr = stringEnv(getenv, "LINKR_ADDR", config.Addr)
	config.MetricsAddr = stringEnv(getenv, "LINKR_METRICS_ADDR", config.MetricsAddr)
	config.DatabaseURL = stringEnv(getenv, "LINKR_DATABASE_URL", config.DatabaseURL)
	config.BaseURL = stringEnv(getenv, "LINKR_BASE_URL", config.BaseURL)
	config.Environment = stringEnv(getenv, "LINKR_ENV", config.Environment)
	config.LogLevel = stringEnv(getenv, "LINKR_LOG_LEVEL", config.LogLevel)

	var err error

	if config.CacheTTL, err = durationEnv(getenv, "LINKR_CACHE_TTL", config.CacheTTL); err != nil {
		return Config{}, err
	}

	if config.ShutdownTimeout, err = durationEnv(getenv, "LINKR_SHUTDOWN_TIMEOUT", config.ShutdownTimeout); err != nil {
		return Config{}, err
	}

	if config.RateLimitPerMinute, err = intEnv(getenv, "LINKR_RATE_LIMIT", config.RateLimitPerMinute); err != nil {
		return Config{}, err
	}

	flags := flag.NewFlagSet("linkr", flag.ContinueOnError)

	flags.StringVar(&config.Addr, "addr", config.Addr, "public listen address")
	flags.StringVar(&config.MetricsAddr, "metrics-addr", config.MetricsAddr, "metrics listen address (keep it on localhost)")
	flags.StringVar(&config.DatabaseURL, "db", config.DatabaseURL, "sqlite database file")
	flags.StringVar(&config.BaseURL, "base-url", config.BaseURL, "origin short links are printed with")
	flags.StringVar(&config.Environment, "env", config.Environment, "development or production")
	flags.StringVar(&config.LogLevel, "log-level", config.LogLevel, "debug, info, warn or error")
	flags.DurationVar(&config.CacheTTL, "cache-ttl", config.CacheTTL, "redirect cache TTL")
	flags.DurationVar(&config.ShutdownTimeout, "shutdown-timeout", config.ShutdownTimeout, "grace period for in-flight requests")
	flags.IntVar(&config.RateLimitPerMinute, "rate-limit", config.RateLimitPerMinute, "requests per minute per API key")

	if err := flags.Parse(args); err != nil {
		return Config{}, fmt.Errorf("parse flags: %w", err)
	}

	if err := config.Validate(); err != nil {
		return Config{}, err
	}

	return config, nil
}

// Validate rejects a configuration that would fail later and less clearly.
func (c Config) Validate() error {
	var problems []error

	if c.Addr == "" {
		problems = append(problems, errors.New("addr is empty"))
	}

	if c.DatabaseURL == "" {
		problems = append(problems, errors.New("database url is empty"))
	}

	if !strings.HasPrefix(c.BaseURL, "http://") && !strings.HasPrefix(c.BaseURL, "https://") {
		problems = append(problems, fmt.Errorf("base url %q must start with http:// or https://", c.BaseURL))
	}

	switch c.Environment {
	case "development", "production":
	default:
		problems = append(problems, fmt.Errorf("environment %q is not development or production", c.Environment))
	}

	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		problems = append(problems, fmt.Errorf("log level %q is not debug, info, warn or error", c.LogLevel))
	}

	if c.ReadHeaderTimeout <= 0 {
		// A zero timeout means NO timeout, which is how a Slowloris attack
		// works. Rejecting zero is not pedantry.
		problems = append(problems, errors.New("read header timeout must be positive"))
	}

	if c.ShutdownTimeout <= 0 {
		problems = append(problems, errors.New("shutdown timeout must be positive"))
	}

	if c.CacheTTL < 0 {
		problems = append(problems, errors.New("cache ttl cannot be negative"))
	}

	if c.RateLimitPerMinute <= 0 {
		problems = append(problems, errors.New("rate limit must be positive"))
	}

	// A metrics endpoint on a public interface exposes the heap and lets
	// anyone start a CPU profile. Warned about in development, refused in
	// production.
	if c.Environment == "production" && c.MetricsAddr != "" && !isLoopback(c.MetricsAddr) {
		problems = append(problems, fmt.Errorf(
			"metrics address %q is not on localhost: /metrics and /debug/pprof must not be public", c.MetricsAddr))
	}

	return errors.Join(problems...)
}

func isLoopback(address string) bool {
	host, _, found := strings.Cut(address, ":")

	if !found {
		return false
	}

	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// Production reports whether this is a production configuration.
func (c Config) Production() bool {
	return c.Environment == "production"
}

// String renders the configuration for a startup log line.
//
// There is nothing secret in it today. When there is - a database password, a
// signing key - it must not be printed, and the safest habit is a String that
// lists fields explicitly rather than one that reflects over the struct and
// picks up whatever is added later.
func (c Config) String() string {
	return fmt.Sprintf("addr=%s metrics=%s db=%s base=%s env=%s log=%s cache_ttl=%s rate_limit=%d/min",
		c.Addr, c.MetricsAddr, c.DatabaseURL, c.BaseURL, c.Environment, c.LogLevel,
		c.CacheTTL, c.RateLimitPerMinute)
}

func stringEnv(getenv func(string) string, key, fallback string) string {
	if value := getenv(key); value != "" {
		return value
	}

	return fallback
}

func durationEnv(getenv func(string) string, key string, fallback time.Duration) (time.Duration, error) {
	raw := getenv(key)
	if raw == "" {
		return fallback, nil
	}

	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s=%q: %w", key, raw, err)
	}

	return parsed, nil
}

func intEnv(getenv func(string) string, key string, fallback int) (int, error) {
	raw := getenv(key)
	if raw == "" {
		return fallback, nil
	}

	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s=%q: %w", key, raw, err)
	}

	return parsed, nil
}
