package config_test

import (
	"strings"
	"testing"
	"time"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-98/internal/config"
)

// env builds a getenv function from a map, so a test never touches the real
// process environment and can run in parallel.
func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestDefaultsAreValid(t *testing.T) {
	if err := config.Default().Validate(); err != nil {
		t.Errorf("the defaults do not validate: %v", err)
	}
}

func TestLoadWithNothingSet(t *testing.T) {
	cfg, err := config.Load(nil, env(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Addr != config.Default().Addr {
		t.Errorf("addr = %q, want the default", cfg.Addr)
	}
}

// Flags beat the environment beats defaults - the conventional precedence, and
// worth a test because getting it backwards is silent.
func TestPrecedence(t *testing.T) {
	environment := env(map[string]string{
		"LINKR_ADDR":       ":7000",
		"LINKR_LOG_LEVEL":  "warn",
		"LINKR_CACHE_TTL":  "5m",
		"LINKR_RATE_LIMIT": "500",
	})

	cfg, err := config.Load([]string{"-addr", ":9000"}, environment)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Addr != ":9000" {
		t.Errorf("addr = %q, want the flag to win", cfg.Addr)
	}

	if cfg.LogLevel != "warn" {
		t.Errorf("log level = %q, want the environment to beat the default", cfg.LogLevel)
	}

	if cfg.CacheTTL != 5*time.Minute {
		t.Errorf("cache ttl = %s, want 5m", cfg.CacheTTL)
	}

	if cfg.RateLimitPerMinute != 500 {
		t.Errorf("rate limit = %d, want 500", cfg.RateLimitPerMinute)
	}
}

// A malformed duration must fail at startup, not produce a zero timeout that
// shows up as a hang three hours later.
func TestMalformedValuesFailAtStartup(t *testing.T) {
	cases := map[string]map[string]string{
		"duration": {"LINKR_CACHE_TTL": "sixty seconds"},
		"integer":  {"LINKR_RATE_LIMIT": "lots"},
	}

	for name, values := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := config.Load(nil, env(values)); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*config.Config){
		"empty addr":      func(c *config.Config) { c.Addr = "" },
		"empty database":  func(c *config.Config) { c.DatabaseURL = "" },
		"base url scheme": func(c *config.Config) { c.BaseURL = "localhost:8096" },
		"unknown env":     func(c *config.Config) { c.Environment = "staging-ish" },
		"unknown level":   func(c *config.Config) { c.LogLevel = "verbose" },
		"zero header":     func(c *config.Config) { c.ReadHeaderTimeout = 0 },
		"zero shutdown":   func(c *config.Config) { c.ShutdownTimeout = 0 },
		"negative ttl":    func(c *config.Config) { c.CacheTTL = -time.Second },
		"zero rate limit": func(c *config.Config) { c.RateLimitPerMinute = 0 },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := config.Default()

			mutate(&cfg)

			if err := cfg.Validate(); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

// A zero timeout means NO timeout, which is how Slowloris works.
func TestZeroReadHeaderTimeoutIsRejected(t *testing.T) {
	cfg := config.Default()
	cfg.ReadHeaderTimeout = 0

	err := cfg.Validate()
	if err == nil {
		t.Fatal("a zero read header timeout was accepted")
	}

	if !strings.Contains(err.Error(), "read header timeout") {
		t.Errorf("error = %v, want it to name the field", err)
	}
}

// /metrics and /debug/pprof expose the heap and allow a CPU profile to be
// started by anyone who can reach them.
func TestPublicMetricsAddressIsRejectedInProduction(t *testing.T) {
	cfg := config.Default()
	cfg.Environment = "production"
	cfg.MetricsAddr = "0.0.0.0:9096"

	if err := cfg.Validate(); err == nil {
		t.Fatal("a public metrics address was accepted in production")
	}

	// The same address is allowed in development, where it is a convenience
	// rather than an exposure.
	cfg.Environment = "development"

	if err := cfg.Validate(); err != nil {
		t.Errorf("development should allow it: %v", err)
	}
}

// Validate reports every problem, not just the first: fixing them one restart
// at a time is how a five-minute config change takes an hour.
func TestValidateReportsEveryProblem(t *testing.T) {
	cfg := config.Default()
	cfg.Addr = ""
	cfg.DatabaseURL = ""
	cfg.LogLevel = "loud"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected errors")
	}

	for _, want := range []string{"addr", "database", "log level"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q is missing %q", err, want)
		}
	}
}

// Nothing secret is in the configuration today, and String lists fields
// explicitly so that adding one does not silently print it.
func TestStringIncludesTheUsefulFields(t *testing.T) {
	rendered := config.Default().String()

	for _, want := range []string{"addr=", "db=", "env=", "rate_limit="} {
		if !strings.Contains(rendered, want) {
			t.Errorf("%q is missing %q", rendered, want)
		}
	}
}
