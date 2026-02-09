package coordinator

import (
	"testing"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func TestMostLoadedTier(t *testing.T) {
	tests := []struct {
		name       string
		stats      *bridge.MemoryStatsResult
		wantTier   string
		wantTokens int
	}{
		{
			name: "working is largest",
			stats: &bridge.MemoryStatsResult{
				WorkingMemory:   bridge.MemoryTierStats{Tokens: 5000},
				ShortTermMemory: bridge.MemoryTierStats{Tokens: 2000},
				LongTermMemory:  bridge.MemoryTierStats{Tokens: 1000},
			},
			wantTier:   "working",
			wantTokens: 5000,
		},
		{
			name: "long term is largest",
			stats: &bridge.MemoryStatsResult{
				WorkingMemory:   bridge.MemoryTierStats{Tokens: 100},
				ShortTermMemory: bridge.MemoryTierStats{Tokens: 200},
				LongTermMemory:  bridge.MemoryTierStats{Tokens: 10000},
			},
			wantTier:   "long_term",
			wantTokens: 10000,
		},
		{
			name: "all empty",
			stats: &bridge.MemoryStatsResult{
				WorkingMemory:   bridge.MemoryTierStats{Tokens: 0},
				ShortTermMemory: bridge.MemoryTierStats{Tokens: 0},
				LongTermMemory:  bridge.MemoryTierStats{Tokens: 0},
			},
			wantTier:   "",
			wantTokens: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tier, tokens := mostLoadedTier(tt.stats)
			if tier != tt.wantTier {
				t.Errorf("tier = %q, want %q", tier, tt.wantTier)
			}
			if tokens != tt.wantTokens {
				t.Errorf("tokens = %d, want %d", tokens, tt.wantTokens)
			}
		})
	}
}
