package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

/*
Pool configuration and monitoring.

database/sql does not connect when you call Open: it manages a pool of
connections created on demand. Four knobs control it, and the defaults are
wrong for most servers:

	SetMaxOpenConns     total connections this process may hold (default: unlimited)
	SetMaxIdleConns     connections kept warm for reuse       (default: 2)
	SetConnMaxLifetime  how long a connection may live        (default: forever)
	SetConnMaxIdleTime  how long an idle connection survives  (default: forever)

Unlimited MaxOpenConns is the dangerous default: under a traffic spike one
process can open hundreds of connections and take the database down for
everyone else. A bounded pool turns that into local queueing instead, which is
visible in db.Stats().WaitCount and recoverable.
*/

type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// DefaultPoolConfig is a starting point for a small HTTP service, not a
// universal answer. The right MaxOpenConns comes from the database side:
// Postgres max_connections divided by the number of processes that talk to
// it, leaving headroom for migrations, admin sessions and background jobs.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxOpenConns: envInt("DB_MAX_OPEN_CONNS", 10),
		// Idle should not be far below open, or the pool spends its time
		// closing connections it is about to need again. Keeping them equal
		// is the usual advice for a steady workload.
		MaxIdleConns: envInt("DB_MAX_IDLE_CONNS", 10),
		// A lifetime cap lets connections rotate: it survives database
		// failovers, DNS changes and server-side idle timeouts that would
		// otherwise hand you a dead connection mid-request.
		ConnMaxLifetime: envDuration("DB_CONN_MAX_LIFETIME", 30*time.Minute),
		ConnMaxIdleTime: envDuration("DB_CONN_MAX_IDLE_TIME", 5*time.Minute),
	}
}

func (c PoolConfig) Apply(db *sql.DB) {
	db.SetMaxOpenConns(c.MaxOpenConns)
	db.SetMaxIdleConns(c.MaxIdleConns)
	db.SetConnMaxLifetime(c.ConnMaxLifetime)
	db.SetConnMaxIdleTime(c.ConnMaxIdleTime)
}

func (c PoolConfig) String() string {
	return fmt.Sprintf(
		"max_open=%d max_idle=%d max_lifetime=%s max_idle_time=%s",
		c.MaxOpenConns, c.MaxIdleConns, c.ConnMaxLifetime, c.ConnMaxIdleTime,
	)
}

//
// SATURATION MONITORING
//

// PoolSample is one observation of pool pressure. The absolute counters in
// sql.DBStats are cumulative, so what matters is the delta between samples.
type PoolSample struct {
	At           time.Time
	InUse        int
	Idle         int
	Open         int
	NewWaits     int64
	WaitDuration time.Duration
	Saturated    bool
}

// PoolMonitor samples db.Stats() on an interval and reports saturation.
//
// Connection waits are a leading indicator: latency rises while error rates
// still look fine, so a dashboard that only watches errors sees nothing until
// the timeouts start.
type PoolMonitor struct {
	db       *sql.DB
	interval time.Duration

	mu       sync.Mutex
	samples  []PoolSample
	lastWait int64
	lastTime time.Duration
}

func NewPoolMonitor(db *sql.DB, interval time.Duration) *PoolMonitor {
	return &PoolMonitor{db: db, interval: interval}
}

// Run samples until the context is cancelled. It owns no goroutines of its
// own: the caller decides where it runs and when it stops.
func (m *PoolMonitor) Run(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			m.Sample()
		}
	}
}

func (m *PoolMonitor) Sample() PoolSample {
	stats := m.db.Stats()

	m.mu.Lock()
	defer m.mu.Unlock()

	sample := PoolSample{
		At:           time.Now(),
		InUse:        stats.InUse,
		Idle:         stats.Idle,
		Open:         stats.OpenConnections,
		NewWaits:     stats.WaitCount - m.lastWait,
		WaitDuration: stats.WaitDuration - m.lastTime,
	}

	// Two independent symptoms: goroutines had to queue at all, and the pool
	// is fully checked out right now.
	sample.Saturated = sample.NewWaits > 0 ||
		(stats.MaxOpenConnections > 0 && stats.InUse >= stats.MaxOpenConnections)

	m.lastWait = stats.WaitCount
	m.lastTime = stats.WaitDuration

	m.samples = append(m.samples, sample)

	if sample.Saturated {
		// In a real service this is a metric and a warning log, not a print:
		// see Day 72 for the Prometheus version of exactly this counter.
		log.Printf(
			"pool saturated: in_use=%d/%d idle=%d new_waits=%d waited=%s",
			stats.InUse, stats.MaxOpenConnections, stats.Idle,
			sample.NewWaits, sample.WaitDuration.Round(time.Millisecond),
		)
	}

	return sample
}

func (m *PoolMonitor) Samples() []PoolSample {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]PoolSample(nil), m.samples...)
}

func (m *PoolMonitor) SaturatedSamples() int {
	count := 0

	for _, sample := range m.Samples() {
		if sample.Saturated {
			count++
		}
	}

	return count
}

//
// OPEN AND CLOSE
//

func OpenPool(ctx context.Context, dsn string, config PoolConfig) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn+"?_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	config.Apply(db)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close database after failed ping: %v", closeErr)
		}

		return nil, fmt.Errorf("ping database: %w", err)
	}

	if _, err := db.ExecContext(ctx, Schema); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close database after failed migrate: %v", closeErr)
		}

		return nil, fmt.Errorf("create schema: %w", err)
	}

	return db, nil
}

// ClosePool is what a graceful shutdown must call. Close waits for queries
// that are still running, then releases every connection.
//
// Skipping it is how deploys hang: the process keeps sockets open, the
// database keeps the sessions, and the orchestrator waits for a container
// that will never exit.
func ClosePool(db *sql.DB) error {
	stats := db.Stats()

	log.Printf(
		"closing pool: open=%d in_use=%d idle=%d total_waits=%d total_wait_time=%s",
		stats.OpenConnections, stats.InUse, stats.Idle,
		stats.WaitCount, stats.WaitDuration.Round(time.Millisecond),
	)

	if err := db.Close(); err != nil {
		return fmt.Errorf("close pool: %w", err)
	}

	return nil
}

//
// ENV HELPERS
//

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))

	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		log.Printf("invalid %s=%q, using %d", key, value, fallback)
		return fallback
	}

	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))

	if value == "" {
		return fallback
	}

	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		log.Printf("invalid %s=%q, using %s", key, value, fallback)
		return fallback
	}

	return parsed
}
