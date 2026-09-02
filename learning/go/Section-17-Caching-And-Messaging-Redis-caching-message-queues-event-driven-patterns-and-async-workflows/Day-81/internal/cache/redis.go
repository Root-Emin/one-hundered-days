package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

/*
The Redis implementation.

Redis is the default answer for a shared cache because it is fast, it expires
keys for you, and every instance of the service sees the same entries - which
is what makes invalidation work across replicas.

What it costs: a network hop per lookup, a serialisation step, and a new
dependency that can be down. The code below assumes it WILL be down sometimes:
every failure path degrades to "cache miss" rather than to an error, so a
Redis outage makes the service slower, not broken.
*/

type Redis struct {
	client *redis.Client
	prefix string
}

type RedisConfig struct {
	Address  string
	Password string
	DB       int

	// Prefix namespaces every key. Two services sharing a Redis instance
	// without one will eventually collide on a key like "user:1".
	Prefix string

	// Timeouts, because a slow cache must never become a slow service.
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	PoolSize     int
}

func DefaultRedisConfig(address string) RedisConfig {
	return RedisConfig{
		Address: address,
		Prefix:  "day81",
		// Aggressive on purpose: a cache lookup that takes 200ms has already
		// failed at its job.
		DialTimeout:  500 * time.Millisecond,
		ReadTimeout:  200 * time.Millisecond,
		WriteTimeout: 200 * time.Millisecond,
		PoolSize:     10,
	}
}

func NewRedis(config RedisConfig) *Redis {
	if config.DialTimeout == 0 {
		config = DefaultRedisConfig(config.Address)
	}

	return &Redis{
		client: redis.NewClient(&redis.Options{
			Addr:         config.Address,
			Password:     config.Password,
			DB:           config.DB,
			DialTimeout:  config.DialTimeout,
			ReadTimeout:  config.ReadTimeout,
			WriteTimeout: config.WriteTimeout,
			PoolSize:     config.PoolSize,
		}),
		prefix: config.Prefix,
	}
}

// Ping verifies the connection at startup, so a misconfigured address is a
// startup log line rather than a mystery latency spike.
func (r *Redis) Ping(ctx context.Context) error {
	if err := r.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping redis: %w", err)
	}

	return nil
}

func (r *Redis) key(key string) string {
	if r.prefix == "" {
		return key
	}

	return r.prefix + ":" + key
}

func (r *Redis) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := r.client.Get(ctx, r.key(key)).Bytes()

	switch {
	case errors.Is(err, redis.Nil):
		return nil, ErrMiss

	case err != nil:
		// A Redis failure is reported as a MISS, not as an error: the caller
		// then reads the source of truth and serves the request. A cache
		// outage must never become a service outage.
		return nil, fmt.Errorf("%w: redis unavailable: %w", ErrMiss, err)
	}

	return value, nil
}

func (r *Redis) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := r.client.Set(ctx, r.key(key), value, ttl).Err(); err != nil {
		return fmt.Errorf("cache set %s: %w", key, err)
	}

	return nil
}

func (r *Redis) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}

	prefixed := make([]string, 0, len(keys))

	for _, key := range keys {
		prefixed = append(prefixed, r.key(key))
	}

	if err := r.client.Del(ctx, prefixed...).Err(); err != nil {
		return fmt.Errorf("cache delete: %w", err)
	}

	return nil
}

// DeleteByPattern removes every key matching a glob.
//
// It uses SCAN rather than KEYS: KEYS blocks the whole Redis server while it
// walks the keyspace, which on a production instance is an outage. SCAN
// iterates in small batches instead.
func (r *Redis) DeleteByPattern(ctx context.Context, pattern string) (int, error) {
	var (
		cursor  uint64
		removed int
	)

	for {
		keys, next, err := r.client.Scan(ctx, cursor, r.key(pattern), 100).Result()
		if err != nil {
			return removed, fmt.Errorf("scan %s: %w", pattern, err)
		}

		if len(keys) > 0 {
			if err := r.client.Del(ctx, keys...).Err(); err != nil {
				return removed, fmt.Errorf("delete batch: %w", err)
			}

			removed += len(keys)
		}

		cursor = next

		if cursor == 0 {
			break
		}
	}

	return removed, nil
}

func (r *Redis) Close() error {
	if err := r.client.Close(); err != nil {
		return fmt.Errorf("close redis: %w", err)
	}

	return nil
}
