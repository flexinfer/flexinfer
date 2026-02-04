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
		{"returns false for '0'", "TEST_BOOL", "0", true, false},
		{"returns false for 'false'", "TEST_BOOL", "false", true, false},
		{"returns fallback when empty", "TEST_BOOL", "", true, true},
		{"returns false for other values", "TEST_BOOL", "maybe", true, false},
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

func TestMissingEnvError(t *testing.T) {
	err := &MissingEnvError{Key: "MY_VAR"}
	expected := "MY_VAR environment variable is required"
	if err.Error() != expected {
		t.Errorf("MissingEnvError.Error() = %q, want %q", err.Error(), expected)
	}
}
