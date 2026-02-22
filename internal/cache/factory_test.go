package cache

import (
	"log/slog"
	"testing"
)

func TestNew_DefaultMemory(t *testing.T) {
	s := New(StoreConfig{Backend: "memory"}, slog.Default())
	defer s.Close()

	if _, ok := s.(*MemoryStore); !ok {
		t.Fatalf("expected *MemoryStore, got %T", s)
	}
}

func TestNew_RedisFallback(t *testing.T) {
	// Unreachable Redis should fall back to MemoryStore.
	s := New(StoreConfig{
		Backend: "redis",
		Redis: RedisConfig{
			URL: "redis://192.0.2.1:6379", // TEST-NET, unreachable
		},
	}, slog.Default())
	defer s.Close()

	if _, ok := s.(*MemoryStore); !ok {
		t.Fatalf("expected fallback to *MemoryStore, got %T", s)
	}
}

func TestLoadConfigFromEnv_Defaults(t *testing.T) {
	// With no env vars set, should default to memory backend.
	cfg := LoadConfigFromEnv()
	if cfg.Backend != "memory" {
		t.Fatalf("expected 'memory' backend, got %q", cfg.Backend)
	}
	if cfg.Redis.Prefix != "loom:cache:" {
		t.Fatalf("expected default prefix 'loom:cache:', got %q", cfg.Redis.Prefix)
	}
}
