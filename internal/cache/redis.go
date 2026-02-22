package cache

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	redisOpTimeout  = 500 * time.Millisecond
	redisDefaultTTL = 24 * time.Hour
)

// RedisConfig holds connection parameters for a Redis-backed cache.
type RedisConfig struct {
	URL          string   // redis://host:port (standalone)
	ClusterAddrs []string // cluster node addrs; if set, uses cluster mode
	Password     string
	Prefix       string // key prefix (default "loom:cache:")
}

// RedisStore is a Redis-backed Store implementation. Values are
// JSON-serialized. Each operation has a 500ms timeout.
type RedisStore struct {
	client redis.UniversalClient
	prefix string
}

// NewRedisStore creates a RedisStore and pings the server to verify
// connectivity. Returns an error if the server is unreachable.
func NewRedisStore(cfg RedisConfig) (*RedisStore, error) {
	prefix := cfg.Prefix
	if prefix == "" {
		prefix = "loom:cache:"
	}

	var client redis.UniversalClient
	if len(cfg.ClusterAddrs) > 0 {
		client = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:    cfg.ClusterAddrs,
			Password: cfg.Password,
		})
	} else {
		opts, err := redis.ParseURL(cfg.URL)
		if err != nil {
			return nil, err
		}
		if cfg.Password != "" {
			opts.Password = cfg.Password
		}
		client = redis.NewClient(opts)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}

	return &RedisStore{client: client, prefix: prefix}, nil
}

func (r *RedisStore) prefixed(key string) string {
	return r.prefix + key
}

// Get retrieves a value from Redis, JSON-deserializing it.
func (r *RedisStore) Get(key string) (any, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), redisOpTimeout)
	defer cancel()

	data, err := r.client.Get(ctx, r.prefixed(key)).Bytes()
	if err != nil {
		return nil, false
	}

	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, false
	}
	return value, true
}

// Set stores a value in Redis with the given TTL.
func (r *RedisStore) Set(key string, value any, ttl time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), redisOpTimeout)
	defer cancel()

	data, err := json.Marshal(value)
	if err != nil {
		return
	}

	if ttl <= 0 {
		ttl = redisDefaultTTL
	}
	_ = r.client.Set(ctx, r.prefixed(key), data, ttl).Err()
}

// Invalidate removes a single key from Redis.
func (r *RedisStore) Invalidate(key string) {
	ctx, cancel := context.WithTimeout(context.Background(), redisOpTimeout)
	defer cancel()
	_ = r.client.Del(ctx, r.prefixed(key)).Err()
}

// InvalidateAll removes all keys matching the configured prefix using SCAN.
func (r *RedisStore) InvalidateAll() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pattern := r.prefix + "*"
	var cursor uint64
	for {
		keys, next, err := r.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return
		}
		if len(keys) > 0 {
			pipe := r.client.Pipeline()
			for _, k := range keys {
				pipe.Del(ctx, k)
			}
			_, _ = pipe.Exec(ctx)
		}
		cursor = next
		if cursor == 0 {
			return
		}
	}
}

// Len returns an approximate count of keys matching the prefix.
func (r *RedisStore) Len() int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	count := 0
	pattern := r.prefix + "*"
	var cursor uint64
	for {
		keys, next, err := r.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return count
		}
		count += len(keys)
		cursor = next
		if cursor == 0 {
			return count
		}
	}
}

// Close closes the underlying Redis client.
func (r *RedisStore) Close() error {
	return r.client.Close()
}

// parseRedisClusterAddrs splits a comma-separated string of addresses.
func parseRedisClusterAddrs(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	addrs := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			addrs = append(addrs, p)
		}
	}
	if len(addrs) == 0 {
		return nil
	}
	return addrs
}
