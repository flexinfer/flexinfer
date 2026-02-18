package proxy

import (
	"testing"

	"github.com/flexinfer/flexinfer/internal/routing"
)

func TestConfigFromEnv_RoutingKeyStrictness(t *testing.T) {
	t.Setenv("PROXY_ROUTING_EXPLICIT_KEY_MAX_LENGTH", "64")
	t.Setenv("PROXY_ROUTING_SYSTEM_SEGMENT_MAX_LENGTH", "256")
	t.Setenv("PROXY_ROUTING_DOCUMENT_SEGMENT_MAX_LENGTH", "120")

	cfg := ConfigFromEnv(nil, "flexinfer-system")
	if cfg.RoutingExplicitCacheKeyMaxLength != 64 {
		t.Fatalf("explicit max length=%d want 64", cfg.RoutingExplicitCacheKeyMaxLength)
	}
	if cfg.RoutingSystemSegmentMaxLength != 256 {
		t.Fatalf("system max length=%d want 256", cfg.RoutingSystemSegmentMaxLength)
	}
	if cfg.RoutingDocSegmentMaxLength != 120 {
		t.Fatalf("document max length=%d want 120", cfg.RoutingDocSegmentMaxLength)
	}
}

func TestNew_AppliesRoutingKeyStrictness(t *testing.T) {
	original := routing.CurrentPrefixKeyConfig()
	t.Cleanup(func() {
		routing.SetPrefixKeyConfig(original)
	})

	_ = New(Config{
		Namespace:                        "flexinfer-system",
		RoutingExplicitCacheKeyMaxLength: 48,
		RoutingSystemSegmentMaxLength:    128,
		RoutingDocSegmentMaxLength:       64,
	})

	got := routing.CurrentPrefixKeyConfig()
	if got.ExplicitCacheKeyMaxLength != 48 {
		t.Fatalf("explicit max length=%d want 48", got.ExplicitCacheKeyMaxLength)
	}
	if got.SystemSegmentMaxLength != 128 {
		t.Fatalf("system max length=%d want 128", got.SystemSegmentMaxLength)
	}
	if got.DocSegmentMaxLength != 64 {
		t.Fatalf("document max length=%d want 64", got.DocSegmentMaxLength)
	}
}
