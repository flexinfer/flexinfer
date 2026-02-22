package cache

import (
	"os"
	"testing"
)

func TestRedisStore(t *testing.T) {
	url := os.Getenv("REDIS_URL")
	if url == "" {
		t.Skip("REDIS_URL not set; skipping Redis integration test")
	}

	s, err := NewRedisStore(RedisConfig{
		URL:    url,
		Prefix: "loom:test:cache:",
	})
	if err != nil {
		t.Fatalf("connect to Redis: %v", err)
	}
	defer func() {
		s.InvalidateAll()
		s.Close()
	}()

	storeTests(t, s)
}
