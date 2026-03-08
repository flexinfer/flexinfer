package env

import (
	"os"
	"testing"
	"time"
)

func TestString(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		envValue string
		fallback string
		want     string
	}{
		{"returns env value when set", "TEST_STRING", "hello", "default", "hello"},
		{"returns fallback when empty", "TEST_STRING", "", "default", "default"},
		{"returns fallback when not set", "TEST_STRING_UNSET", "", "default", "default"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.key, tt.envValue)
				defer os.Unsetenv(tt.key)
			}

			got := String(tt.key, tt.fallback)
			if got != tt.want {
				t.Errorf("String(%q, %q) = %q, want %q", tt.key, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestInt(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		envValue string
		fallback int
		want     int
	}{
		{"returns env value when set", "TEST_INT", "42", 10, 42},
		{"returns fallback when empty", "TEST_INT", "", 10, 10},
		{"returns fallback for invalid", "TEST_INT", "abc", 10, 10},
		{"returns fallback for zero", "TEST_INT", "0", 10, 10},
		{"returns fallback for negative", "TEST_INT", "-5", 10, 10},
		{"handles whitespace", "TEST_INT", "  42  ", 10, 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.key, tt.envValue)
				defer os.Unsetenv(tt.key)
			}

			got := Int(tt.key, tt.fallback)
			if got != tt.want {
				t.Errorf("Int(%q, %d) = %d, want %d", tt.key, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestIntWithZero(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		envValue string
		fallback int
		want     int
	}{
		{"returns env value when set", "TEST_INT_ZERO", "42", 10, 42},
		{"returns zero when set to zero", "TEST_INT_ZERO", "0", 10, 0},
		{"returns negative when set", "TEST_INT_ZERO", "-5", 10, -5},
		{"returns fallback when empty", "TEST_INT_ZERO", "", 10, 10},
		{"returns fallback for invalid", "TEST_INT_ZERO", "abc", 10, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.key, tt.envValue)
				defer os.Unsetenv(tt.key)
			}

			got := IntWithZero(tt.key, tt.fallback)
			if got != tt.want {
				t.Errorf("IntWithZero(%q, %d) = %d, want %d", tt.key, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestBool(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		envValue string
		fallback bool
		want     bool
	}{
		{"returns true for '1'", "TEST_BOOL", "1", false, true},
		{"returns true for 'true'", "TEST_BOOL", "true", false, true},
		{"returns true for 'TRUE'", "TEST_BOOL", "TRUE", false, true},
		{"returns true for 'yes'", "TEST_BOOL", "yes", false, true},
		{"returns true for 'on'", "TEST_BOOL", "on", false, true},
		{"returns true for 't'", "TEST_BOOL", "t", false, true},
		{"returns true for 'y'", "TEST_BOOL", "y", false, true},
		{"returns false for '0'", "TEST_BOOL", "0", true, false},
		{"returns false for 'false'", "TEST_BOOL", "false", true, false},
		{"returns false for 'f'", "TEST_BOOL", "f", true, false},
		{"returns false for 'no'", "TEST_BOOL", "no", true, false},
		{"returns false for 'n'", "TEST_BOOL", "n", true, false},
		{"returns false for 'off'", "TEST_BOOL", "off", true, false},
		{"returns fallback when empty", "TEST_BOOL", "", true, true},
		{"returns fallback for unrecognized", "TEST_BOOL", "maybe", true, true},
		{"trims whitespace", "TEST_BOOL", "  true  ", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.key, tt.envValue)
				defer os.Unsetenv(tt.key)
			}

			got := Bool(tt.key, tt.fallback)
			if got != tt.want {
				t.Errorf("Bool(%q, %v) = %v, want %v", tt.key, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestDuration(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		envValue string
		fallback time.Duration
		want     time.Duration
	}{
		{"returns env value when set", "TEST_DUR", "10s", time.Second, 10 * time.Second},
		{"returns minutes", "TEST_DUR", "5m", time.Second, 5 * time.Minute},
		{"returns hours", "TEST_DUR", "2h", time.Second, 2 * time.Hour},
		{"returns fallback when empty", "TEST_DUR", "", 30 * time.Second, 30 * time.Second},
		{"returns fallback for invalid", "TEST_DUR", "invalid", 30 * time.Second, 30 * time.Second},
		{"handles whitespace", "TEST_DUR", "  1m  ", time.Second, time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.key, tt.envValue)
				defer os.Unsetenv(tt.key)
			}

			got := Duration(tt.key, tt.fallback)
			if got != tt.want {
				t.Errorf("Duration(%q, %v) = %v, want %v", tt.key, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestMustString(t *testing.T) {
	t.Run("returns value when set", func(t *testing.T) {
		os.Setenv("TEST_MUST", "value")
		defer os.Unsetenv("TEST_MUST")

		got, err := MustString("TEST_MUST")
		if err != nil {
			t.Errorf("MustString returned error: %v", err)
		}
		if got != "value" {
			t.Errorf("MustString = %q, want %q", got, "value")
		}
	})

	t.Run("returns error when not set", func(t *testing.T) {
		_, err := MustString("TEST_MUST_UNSET")
		if err == nil {
			t.Error("MustString should return error for unset variable")
		}
		if _, ok := err.(*MissingEnvError); !ok {
			t.Errorf("MustString error type = %T, want *MissingEnvError", err)
		}
	})
}

func TestStringWithFallbacks(t *testing.T) {
	tests := []struct {
		name  string
		setup map[string]string
		keys  []string
		want  string
	}{
		{
			"returns first set value",
			map[string]string{"FIRST": "first_val", "SECOND": "second_val"},
			[]string{"FIRST", "SECOND"},
			"first_val",
		},
		{
			"returns second when first empty",
			map[string]string{"SECOND": "second_val"},
			[]string{"FIRST_EMPTY", "SECOND"},
			"second_val",
		},
		{
			"returns empty when none set",
			map[string]string{},
			[]string{"NONE1", "NONE2"},
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.setup {
				os.Setenv(k, v)
				defer os.Unsetenv(k)
			}

			got := StringWithFallbacks(tt.keys...)
			if got != tt.want {
				t.Errorf("StringWithFallbacks(%v) = %q, want %q", tt.keys, got, tt.want)
			}
		})
	}
}

func TestStringChain(t *testing.T) {
	t.Run("returns first set value", func(t *testing.T) {
		t.Setenv("SC_A", "")
		t.Setenv("SC_B", "value_b")
		got := StringChain([]string{"SC_A", "SC_B"}, "default")
		if got != "value_b" {
			t.Errorf("StringChain = %q, want %q", got, "value_b")
		}
	})

	t.Run("returns fallback when all empty", func(t *testing.T) {
		got := StringChain([]string{"SC_NONE_1", "SC_NONE_2"}, "fallback")
		if got != "fallback" {
			t.Errorf("StringChain = %q, want %q", got, "fallback")
		}
	})

	t.Run("returns first non-empty", func(t *testing.T) {
		t.Setenv("SC_FIRST", "first_val")
		got := StringChain([]string{"SC_FIRST", "SC_SECOND"}, "default")
		if got != "first_val" {
			t.Errorf("StringChain = %q, want %q", got, "first_val")
		}
	})

	t.Run("empty keys returns fallback", func(t *testing.T) {
		got := StringChain(nil, "fb")
		if got != "fb" {
			t.Errorf("StringChain(nil) = %q, want %q", got, "fb")
		}
	})
}

func TestInt64(t *testing.T) {
	t.Run("returns env value", func(t *testing.T) {
		t.Setenv("TEST_I64", "4194304")
		got := Int64("TEST_I64", 100)
		if got != 4194304 {
			t.Errorf("Int64 = %d, want 4194304", got)
		}
	})

	t.Run("returns fallback when missing", func(t *testing.T) {
		got := Int64("TEST_I64_MISSING", 2097152)
		if got != 2097152 {
			t.Errorf("Int64 = %d, want 2097152", got)
		}
	})

	t.Run("returns fallback for invalid", func(t *testing.T) {
		t.Setenv("TEST_I64_BAD", "xyz")
		got := Int64("TEST_I64_BAD", 99)
		if got != 99 {
			t.Errorf("Int64 = %d, want 99", got)
		}
	})

	t.Run("returns fallback for zero", func(t *testing.T) {
		t.Setenv("TEST_I64_ZERO", "0")
		got := Int64("TEST_I64_ZERO", 42)
		if got != 42 {
			t.Errorf("Int64 = %d, want 42", got)
		}
	})
}

func TestMissingEnvError(t *testing.T) {
	err := &MissingEnvError{Key: "MY_VAR"}
	expected := "MY_VAR environment variable is required"
	if err.Error() != expected {
		t.Errorf("MissingEnvError.Error() = %q, want %q", err.Error(), expected)
	}
}
