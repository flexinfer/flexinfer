package config

import (
	"testing"
	"time"
)

func TestGetEnv(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		envVal     string
		setEnv     bool
		defaultVal string
		want       string
	}{
		{
			name:       "returns default when unset",
			key:        "TEST_GETENV_UNSET",
			setEnv:     false,
			defaultVal: "fallback",
			want:       "fallback",
		},
		{
			name:       "returns env value when set",
			key:        "TEST_GETENV_SET",
			envVal:     "override",
			setEnv:     true,
			defaultVal: "fallback",
			want:       "override",
		},
		{
			name:       "returns default when empty string",
			key:        "TEST_GETENV_EMPTY",
			envVal:     "",
			setEnv:     true,
			defaultVal: "fallback",
			want:       "fallback",
		},
		{
			name:       "returns value with whitespace preserved",
			key:        "TEST_GETENV_SPACES",
			envVal:     "  spaced  ",
			setEnv:     true,
			defaultVal: "fallback",
			want:       "  spaced  ",
		},
		{
			name:       "returns empty default when both empty",
			key:        "TEST_GETENV_BOTH_EMPTY",
			setEnv:     false,
			defaultVal: "",
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv(tt.key, tt.envVal)
			}
			got := GetEnv(tt.key, tt.defaultVal)
			if got != tt.want {
				t.Errorf("GetEnv(%q, %q) = %q, want %q", tt.key, tt.defaultVal, got, tt.want)
			}
		})
	}
}

func TestGetEnvInt(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		envVal     string
		setEnv     bool
		defaultVal int
		want       int
	}{
		{
			name:       "returns default when unset",
			key:        "TEST_GETENVINT_UNSET",
			setEnv:     false,
			defaultVal: 42,
			want:       42,
		},
		{
			name:       "parses valid int",
			key:        "TEST_GETENVINT_VALID",
			envVal:     "100",
			setEnv:     true,
			defaultVal: 42,
			want:       100,
		},
		{
			name:       "returns default on invalid string",
			key:        "TEST_GETENVINT_INVALID",
			envVal:     "notanumber",
			setEnv:     true,
			defaultVal: 42,
			want:       42,
		},
		{
			name:       "parses negative int",
			key:        "TEST_GETENVINT_NEGATIVE",
			envVal:     "-5",
			setEnv:     true,
			defaultVal: 42,
			want:       -5,
		},
		{
			name:       "parses zero",
			key:        "TEST_GETENVINT_ZERO",
			envVal:     "0",
			setEnv:     true,
			defaultVal: 42,
			want:       0,
		},
		{
			name:       "returns default on float string",
			key:        "TEST_GETENVINT_FLOAT",
			envVal:     "3.14",
			setEnv:     true,
			defaultVal: 42,
			want:       42,
		},
		{
			name:       "returns default on empty string",
			key:        "TEST_GETENVINT_EMPTY",
			envVal:     "",
			setEnv:     true,
			defaultVal: 42,
			want:       42,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv(tt.key, tt.envVal)
			}
			got := GetEnvInt(tt.key, tt.defaultVal)
			if got != tt.want {
				t.Errorf("GetEnvInt(%q, %d) = %d, want %d", tt.key, tt.defaultVal, got, tt.want)
			}
		})
	}
}

func TestGetEnvInt64(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		envVal     string
		setEnv     bool
		defaultVal int64
		want       int64
	}{
		{
			name:       "returns default when unset",
			key:        "TEST_GETENVINT64_UNSET",
			setEnv:     false,
			defaultVal: 100,
			want:       100,
		},
		{
			name:       "parses valid int64",
			key:        "TEST_GETENVINT64_VALID",
			envVal:     "9223372036854775807",
			setEnv:     true,
			defaultVal: 0,
			want:       9223372036854775807,
		},
		{
			name:       "returns default on invalid",
			key:        "TEST_GETENVINT64_INVALID",
			envVal:     "abc",
			setEnv:     true,
			defaultVal: 100,
			want:       100,
		},
		{
			name:       "returns default on empty",
			key:        "TEST_GETENVINT64_EMPTY",
			envVal:     "",
			setEnv:     true,
			defaultVal: 100,
			want:       100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv(tt.key, tt.envVal)
			}
			got := GetEnvInt64(tt.key, tt.defaultVal)
			if got != tt.want {
				t.Errorf("GetEnvInt64(%q, %d) = %d, want %d", tt.key, tt.defaultVal, got, tt.want)
			}
		})
	}
}

func TestGetEnvFloat64(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		envVal     string
		setEnv     bool
		defaultVal float64
		want       float64
	}{
		{
			name:       "returns default when unset",
			key:        "TEST_GETENVFLOAT_UNSET",
			setEnv:     false,
			defaultVal: 3.14,
			want:       3.14,
		},
		{
			name:       "parses valid float",
			key:        "TEST_GETENVFLOAT_VALID",
			envVal:     "99.5",
			setEnv:     true,
			defaultVal: 3.14,
			want:       99.5,
		},
		{
			name:       "parses integer as float",
			key:        "TEST_GETENVFLOAT_INT",
			envVal:     "100",
			setEnv:     true,
			defaultVal: 3.14,
			want:       100.0,
		},
		{
			name:       "returns default on invalid",
			key:        "TEST_GETENVFLOAT_INVALID",
			envVal:     "notafloat",
			setEnv:     true,
			defaultVal: 3.14,
			want:       3.14,
		},
		{
			name:       "parses negative float",
			key:        "TEST_GETENVFLOAT_NEG",
			envVal:     "-1.5",
			setEnv:     true,
			defaultVal: 3.14,
			want:       -1.5,
		},
		{
			name:       "parses zero",
			key:        "TEST_GETENVFLOAT_ZERO",
			envVal:     "0",
			setEnv:     true,
			defaultVal: 3.14,
			want:       0.0,
		},
		{
			name:       "returns default on empty",
			key:        "TEST_GETENVFLOAT_EMPTY",
			envVal:     "",
			setEnv:     true,
			defaultVal: 3.14,
			want:       3.14,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv(tt.key, tt.envVal)
			}
			got := GetEnvFloat64(tt.key, tt.defaultVal)
			if got != tt.want {
				t.Errorf("GetEnvFloat64(%q, %f) = %f, want %f", tt.key, tt.defaultVal, got, tt.want)
			}
		})
	}
}

func TestGetEnvBool(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		envVal     string
		setEnv     bool
		defaultVal bool
		want       bool
	}{
		// Truthy values
		{name: "true lowercase", key: "TEST_BOOL_1", envVal: "true", setEnv: true, defaultVal: false, want: true},
		{name: "TRUE uppercase", key: "TEST_BOOL_2", envVal: "TRUE", setEnv: true, defaultVal: false, want: true},
		{name: "True mixed case", key: "TEST_BOOL_3", envVal: "True", setEnv: true, defaultVal: false, want: true},
		{name: "1", key: "TEST_BOOL_4", envVal: "1", setEnv: true, defaultVal: false, want: true},
		{name: "yes", key: "TEST_BOOL_5", envVal: "yes", setEnv: true, defaultVal: false, want: true},
		{name: "YES uppercase", key: "TEST_BOOL_6", envVal: "YES", setEnv: true, defaultVal: false, want: true},
		{name: "on", key: "TEST_BOOL_7", envVal: "on", setEnv: true, defaultVal: false, want: true},
		{name: "ON uppercase", key: "TEST_BOOL_8", envVal: "ON", setEnv: true, defaultVal: false, want: true},

		// Falsy values
		{name: "false lowercase", key: "TEST_BOOL_9", envVal: "false", setEnv: true, defaultVal: true, want: false},
		{name: "FALSE uppercase", key: "TEST_BOOL_10", envVal: "FALSE", setEnv: true, defaultVal: true, want: false},
		{name: "False mixed case", key: "TEST_BOOL_11", envVal: "False", setEnv: true, defaultVal: true, want: false},
		{name: "0", key: "TEST_BOOL_12", envVal: "0", setEnv: true, defaultVal: true, want: false},
		{name: "no", key: "TEST_BOOL_13", envVal: "no", setEnv: true, defaultVal: true, want: false},
		{name: "NO uppercase", key: "TEST_BOOL_14", envVal: "NO", setEnv: true, defaultVal: true, want: false},
		{name: "off", key: "TEST_BOOL_15", envVal: "off", setEnv: true, defaultVal: true, want: false},
		{name: "OFF uppercase", key: "TEST_BOOL_16", envVal: "OFF", setEnv: true, defaultVal: true, want: false},

		// Default cases
		{name: "unset returns default true", key: "TEST_BOOL_17", setEnv: false, defaultVal: true, want: true},
		{name: "unset returns default false", key: "TEST_BOOL_18", setEnv: false, defaultVal: false, want: false},
		{name: "empty returns default", key: "TEST_BOOL_19", envVal: "", setEnv: true, defaultVal: true, want: true},
		{name: "invalid string returns default true", key: "TEST_BOOL_20", envVal: "maybe", setEnv: true, defaultVal: true, want: true},
		{name: "invalid string returns default false", key: "TEST_BOOL_21", envVal: "maybe", setEnv: true, defaultVal: false, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv(tt.key, tt.envVal)
			}
			got := GetEnvBool(tt.key, tt.defaultVal)
			if got != tt.want {
				t.Errorf("GetEnvBool(%q, %v) = %v, want %v", tt.key, tt.defaultVal, got, tt.want)
			}
		})
	}
}

func TestGetEnvDuration(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		envVal     string
		setEnv     bool
		defaultVal time.Duration
		want       time.Duration
	}{
		{
			name:       "returns default when unset",
			key:        "TEST_DUR_UNSET",
			setEnv:     false,
			defaultVal: 30 * time.Second,
			want:       30 * time.Second,
		},
		{
			name:       "parses seconds",
			key:        "TEST_DUR_SEC",
			envVal:     "30s",
			setEnv:     true,
			defaultVal: time.Minute,
			want:       30 * time.Second,
		},
		{
			name:       "parses minutes",
			key:        "TEST_DUR_MIN",
			envVal:     "5m",
			setEnv:     true,
			defaultVal: time.Minute,
			want:       5 * time.Minute,
		},
		{
			name:       "parses hours",
			key:        "TEST_DUR_HOUR",
			envVal:     "1h",
			setEnv:     true,
			defaultVal: time.Minute,
			want:       time.Hour,
		},
		{
			name:       "parses complex duration",
			key:        "TEST_DUR_COMPLEX",
			envVal:     "1h30m",
			setEnv:     true,
			defaultVal: time.Minute,
			want:       90 * time.Minute,
		},
		{
			name:       "parses milliseconds",
			key:        "TEST_DUR_MS",
			envVal:     "500ms",
			setEnv:     true,
			defaultVal: time.Second,
			want:       500 * time.Millisecond,
		},
		{
			name:       "returns default on invalid",
			key:        "TEST_DUR_INVALID",
			envVal:     "notaduration",
			setEnv:     true,
			defaultVal: 30 * time.Second,
			want:       30 * time.Second,
		},
		{
			name:       "returns default on plain number",
			key:        "TEST_DUR_PLAIN_NUM",
			envVal:     "30",
			setEnv:     true,
			defaultVal: time.Minute,
			want:       time.Minute,
		},
		{
			name:       "returns default on empty",
			key:        "TEST_DUR_EMPTY",
			envVal:     "",
			setEnv:     true,
			defaultVal: 30 * time.Second,
			want:       30 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv(tt.key, tt.envVal)
			}
			got := GetEnvDuration(tt.key, tt.defaultVal)
			if got != tt.want {
				t.Errorf("GetEnvDuration(%q, %v) = %v, want %v", tt.key, tt.defaultVal, got, tt.want)
			}
		})
	}
}

func TestGetEnvUint64(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		envVal     string
		setEnv     bool
		defaultVal uint64
		want       uint64
	}{
		{
			name:       "returns default when unset",
			key:        "TEST_UINT64_UNSET",
			setEnv:     false,
			defaultVal: 10,
			want:       10,
		},
		{
			name:       "parses valid uint64",
			key:        "TEST_UINT64_VALID",
			envVal:     "18446744073709551615",
			setEnv:     true,
			defaultVal: 0,
			want:       18446744073709551615,
		},
		{
			name:       "parses zero",
			key:        "TEST_UINT64_ZERO",
			envVal:     "0",
			setEnv:     true,
			defaultVal: 10,
			want:       0,
		},
		{
			name:       "returns default on negative",
			key:        "TEST_UINT64_NEG",
			envVal:     "-1",
			setEnv:     true,
			defaultVal: 10,
			want:       10,
		},
		{
			name:       "returns default on invalid",
			key:        "TEST_UINT64_INVALID",
			envVal:     "abc",
			setEnv:     true,
			defaultVal: 10,
			want:       10,
		},
		{
			name:       "returns default on empty",
			key:        "TEST_UINT64_EMPTY",
			envVal:     "",
			setEnv:     true,
			defaultVal: 10,
			want:       10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv(tt.key, tt.envVal)
			}
			got := GetEnvUint64(tt.key, tt.defaultVal)
			if got != tt.want {
				t.Errorf("GetEnvUint64(%q, %d) = %d, want %d", tt.key, tt.defaultVal, got, tt.want)
			}
		})
	}
}
