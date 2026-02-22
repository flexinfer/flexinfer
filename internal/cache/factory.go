package cache

import (
	"log/slog"
	"os"
	"strings"
)

// StoreConfig describes which backend to use and its parameters.
type StoreConfig struct {
	Backend string      // "memory" or "redis"
	Redis   RedisConfig // only used when Backend == "redis"
}

// LoadConfigFromEnv reads cache configuration from environment variables.
//
//   - CACHE_BACKEND: "memory" (default) or "redis"
//   - CACHE_REDIS_URL: standalone Redis URL (falls back to REDIS_URL, then redis://localhost:6379)
//   - CACHE_REDIS_CLUSTER_ADDRS: comma-separated cluster addrs (overrides URL when set)
//   - CACHE_REDIS_PASSWORD: auth password
//   - CACHE_REDIS_PREFIX: key prefix (default "loom:cache:")
func LoadConfigFromEnv() StoreConfig {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("CACHE_BACKEND")))
	if backend == "" {
		backend = "memory"
	}

	redisURL := strings.TrimSpace(os.Getenv("CACHE_REDIS_URL"))
	if redisURL == "" {
		redisURL = strings.TrimSpace(os.Getenv("REDIS_URL"))
	}
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}

	return StoreConfig{
		Backend: backend,
		Redis: RedisConfig{
			URL:          redisURL,
			ClusterAddrs: parseRedisClusterAddrs(os.Getenv("CACHE_REDIS_CLUSTER_ADDRS")),
			Password:     os.Getenv("CACHE_REDIS_PASSWORD"),
			Prefix:       envOrDefault("CACHE_REDIS_PREFIX", "loom:cache:"),
		},
	}
}

// New creates a Store from the given config. If the requested backend is
// unavailable (e.g. Redis unreachable), it falls back to MemoryStore and
// logs a warning.
func New(cfg StoreConfig, logger *slog.Logger) Store {
	if logger == nil {
		logger = slog.Default()
	}

	if cfg.Backend == "redis" {
		store, err := NewRedisStore(cfg.Redis)
		if err != nil {
			logger.Warn("redis cache unavailable, falling back to memory",
				"error", err, "url", cfg.Redis.URL)
			return NewMemoryStore()
		}
		logger.Info("cache backend: redis", "prefix", cfg.Redis.Prefix)
		return store
	}

	logger.Info("cache backend: memory")
	return NewMemoryStore()
}

func envOrDefault(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}
