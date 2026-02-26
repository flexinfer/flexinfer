package backend

import "testing"

func TestEstimateVRAMMB(t *testing.T) {
	tests := []struct {
		name       string
		params     float64
		quant      string
		contextLen int
		wantMinMB  int
		wantMaxMB  int
	}{
		{
			name:       "7B FP16 4K context",
			params:     7.0,
			quant:      "FP16",
			contextLen: 4096,
			wantMinMB:  14000,
			wantMaxMB:  20000,
		},
		{
			name:       "7B Q4 4K context",
			params:     7.0,
			quant:      "Q4_K_M",
			contextLen: 4096,
			wantMinMB:  4000,
			wantMaxMB:  7000,
		},
		{
			name:       "13B Q8 8K context",
			params:     13.0,
			quant:      "Q8_0",
			contextLen: 8192,
			wantMinMB:  15000,
			wantMaxMB:  25000,
		},
		{
			name:       "70B Q4 4K context",
			params:     70.0,
			quant:      "AWQ",
			contextLen: 4096,
			wantMinMB:  40000,
			wantMaxMB:  60000,
		},
		{
			name:       "zero params returns 0",
			params:     0,
			quant:      "FP16",
			contextLen: 4096,
			wantMinMB:  0,
			wantMaxMB:  0,
		},
		{
			name:       "negative params returns 0",
			params:     -1,
			quant:      "FP16",
			contextLen: 4096,
			wantMinMB:  0,
			wantMaxMB:  0,
		},
		{
			name:       "unknown quant defaults to FP16",
			params:     7.0,
			quant:      "UNKNOWN",
			contextLen: 4096,
			wantMinMB:  14000,
			wantMaxMB:  20000,
		},
		{
			name:       "zero context uses default",
			params:     7.0,
			quant:      "FP16",
			contextLen: 0,
			wantMinMB:  14000,
			wantMaxMB:  20000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateVRAMMB(tt.params, tt.quant, tt.contextLen)
			if got < tt.wantMinMB || got > tt.wantMaxMB {
				t.Errorf("EstimateVRAMMB(%g, %q, %d) = %d, want [%d, %d]",
					tt.params, tt.quant, tt.contextLen, got, tt.wantMinMB, tt.wantMaxMB)
			}
		})
	}
}

func TestBytesPerParamForFormat(t *testing.T) {
	tests := []struct {
		format string
		want   float64
	}{
		{"FP16", 2.0},
		{"fp16", 2.0},
		{"BF16", 2.0},
		{"FP32", 4.0},
		{"FP8", 1.0},
		{"Q4_K_M", 0.6},
		{"AWQ", 0.6},
		{"GPTQ", 0.6},
		{"Q8_0", 1.1},
		{"INT8", 1.1},
		{"Q5_K_M", 0.7},
		{"Q6_K", 0.8},
		{"Q2_K", 0.35},
		{"Q3_K_M", 0.45},
		{"", 2.0},       // unknown defaults to FP16
		{"CUSTOM", 2.0}, // unknown defaults to FP16
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			got := bytesPerParamForFormat(tt.format)
			if got != tt.want {
				t.Errorf("bytesPerParamForFormat(%q) = %g, want %g", tt.format, got, tt.want)
			}
		})
	}
}
