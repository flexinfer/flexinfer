package agentcontext

import "testing"

func TestGetBool(t *testing.T) {
	tests := []struct {
		name string
		v    any
		def  bool
		want bool
	}{
		{"true value", true, false, true},
		{"false value", false, true, false},
		{"nil with true default", nil, true, true},
		{"nil with false default", nil, false, false},
		{"string value", "true", true, true}, // should return default
		{"int value", 1, false, false},       // should return default
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getBool(tt.v, tt.def)
			if got != tt.want {
				t.Errorf("getBool() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToFloat(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want float64
	}{
		{"float64", float64(1.5), 1.5},
		{"int", int(5), 5.0},
		{"int64", int64(10), 10.0},
		{"string", "1.5", 0},
		{"nil", nil, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toFloat(tt.v)
			if got != tt.want {
				t.Errorf("toFloat() = %v, want %v", got, tt.want)
			}
		})
	}
}
