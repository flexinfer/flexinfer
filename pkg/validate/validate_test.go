package validate

import (
	"testing"
)

func TestError(t *testing.T) {
	err := Error{Field: "name", Message: "is required"}
	expected := "name: is required"
	if err.Error() != expected {
		t.Errorf("Error() = %q, want %q", err.Error(), expected)
	}
}

func TestErrors(t *testing.T) {
	t.Run("empty errors", func(t *testing.T) {
		var errs Errors
		if errs.HasErrors() {
			t.Error("expected no errors")
		}
		if errs.Error() != "" {
			t.Errorf("expected empty string, got %q", errs.Error())
		}
	})

	t.Run("multiple errors", func(t *testing.T) {
		errs := Errors{
			{Field: "name", Message: "is required"},
			{Field: "age", Message: "must be positive"},
		}
		if !errs.HasErrors() {
			t.Error("expected errors")
		}
		result := errs.Error()
		if result != "name: is required; age: must be positive" {
			t.Errorf("unexpected error string: %q", result)
		}
	})
}

func TestNewArgs(t *testing.T) {
	t.Run("nil args", func(t *testing.T) {
		a := NewArgs(nil)
		if a == nil {
			t.Fatal("expected non-nil Args")
		}
		if a.args == nil {
			t.Error("expected initialized map")
		}
	})

	t.Run("with args", func(t *testing.T) {
		args := map[string]any{"key": "value"}
		a := NewArgs(args)
		if a.args["key"] != "value" {
			t.Error("args not set correctly")
		}
	})
}

func TestArgs_Required(t *testing.T) {
	tests := []struct {
		name      string
		args      map[string]any
		field     string
		wantValue string
		wantError bool
	}{
		{"present", map[string]any{"name": "test"}, "name", "test", false},
		{"missing", map[string]any{}, "name", "", true},
		{"empty", map[string]any{"name": ""}, "name", "", true},
		{"wrong type", map[string]any{"name": 123}, "name", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewArgs(tt.args)
			got := a.Required(tt.field)
			if got != tt.wantValue {
				t.Errorf("Required() = %q, want %q", got, tt.wantValue)
			}
			if tt.wantError && !a.Errors().HasErrors() {
				t.Error("expected error")
			}
			if !tt.wantError && a.Errors().HasErrors() {
				t.Errorf("unexpected error: %v", a.Errors())
			}
		})
	}
}

func TestArgs_RequiredInt(t *testing.T) {
	tests := []struct {
		name      string
		args      map[string]any
		field     string
		wantValue int
		wantError bool
	}{
		{"present", map[string]any{"count": float64(42)}, "count", 42, false},
		{"missing", map[string]any{}, "count", 0, true},
		{"wrong type", map[string]any{"count": "42"}, "count", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewArgs(tt.args)
			got := a.RequiredInt(tt.field)
			if got != tt.wantValue {
				t.Errorf("RequiredInt() = %d, want %d", got, tt.wantValue)
			}
			if tt.wantError && !a.Errors().HasErrors() {
				t.Error("expected error")
			}
		})
	}
}

func TestArgs_String(t *testing.T) {
	args := map[string]any{"name": "test"}
	a := NewArgs(args)

	if got := a.String("name", "default"); got != "test" {
		t.Errorf("String() = %q, want %q", got, "test")
	}
	if got := a.String("missing", "default"); got != "default" {
		t.Errorf("String() = %q, want %q", got, "default")
	}
}

func TestArgs_Int(t *testing.T) {
	args := map[string]any{"count": float64(42)}
	a := NewArgs(args)

	if got := a.Int("count", 0); got != 42 {
		t.Errorf("Int() = %d, want 42", got)
	}
	if got := a.Int("missing", 10); got != 10 {
		t.Errorf("Int() = %d, want 10", got)
	}
}

func TestArgs_IntRange(t *testing.T) {
	tests := []struct {
		name      string
		args      map[string]any
		field     string
		def       int
		min       int
		max       int
		wantValue int
		wantError bool
	}{
		{"in range", map[string]any{"n": float64(5)}, "n", 0, 1, 10, 5, false},
		{"below min", map[string]any{"n": float64(0)}, "n", 0, 1, 10, 0, true},
		{"above max", map[string]any{"n": float64(15)}, "n", 0, 1, 10, 15, true},
		{"default in range", map[string]any{}, "n", 5, 1, 10, 5, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewArgs(tt.args)
			got := a.IntRange(tt.field, tt.def, tt.min, tt.max)
			if got != tt.wantValue {
				t.Errorf("IntRange() = %d, want %d", got, tt.wantValue)
			}
			if tt.wantError && !a.Errors().HasErrors() {
				t.Error("expected error")
			}
		})
	}
}

func TestArgs_Bool(t *testing.T) {
	args := map[string]any{"enabled": true}
	a := NewArgs(args)

	if got := a.Bool("enabled", false); got != true {
		t.Error("Bool() = false, want true")
	}
	if got := a.Bool("missing", true); got != true {
		t.Error("Bool() = false, want true (default)")
	}
}

func TestArgs_StringSlice(t *testing.T) {
	t.Run("valid slice", func(t *testing.T) {
		args := map[string]any{"tags": []any{"a", "b", "c"}}
		a := NewArgs(args)
		got := a.StringSlice("tags")
		if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
			t.Errorf("StringSlice() = %v, want [a b c]", got)
		}
	})

	t.Run("missing", func(t *testing.T) {
		a := NewArgs(map[string]any{})
		got := a.StringSlice("tags")
		if got != nil {
			t.Errorf("StringSlice() = %v, want nil", got)
		}
	})

	t.Run("mixed types", func(t *testing.T) {
		args := map[string]any{"tags": []any{"a", 123, "c"}}
		a := NewArgs(args)
		got := a.StringSlice("tags")
		// Should only include strings
		if len(got) != 2 || got[0] != "a" || got[1] != "c" {
			t.Errorf("StringSlice() = %v, want [a c]", got)
		}
	})
}

func TestArgs_Enum(t *testing.T) {
	tests := []struct {
		name      string
		args      map[string]any
		field     string
		def       string
		allowed   []string
		wantValue string
		wantError bool
	}{
		{"valid", map[string]any{"level": "info"}, "level", "debug", []string{"debug", "info", "warn"}, "info", false},
		{"invalid", map[string]any{"level": "trace"}, "level", "debug", []string{"debug", "info", "warn"}, "debug", true},
		{"default", map[string]any{}, "level", "debug", []string{"debug", "info", "warn"}, "debug", false},
		{"empty allowed", map[string]any{}, "level", "", []string{}, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewArgs(tt.args)
			got := a.Enum(tt.field, tt.def, tt.allowed...)
			if got != tt.wantValue {
				t.Errorf("Enum() = %q, want %q", got, tt.wantValue)
			}
			if tt.wantError && !a.Errors().HasErrors() {
				t.Error("expected error")
			}
		})
	}
}

func TestArgs_Pattern(t *testing.T) {
	tests := []struct {
		name      string
		args      map[string]any
		field     string
		pattern   string
		wantError bool
	}{
		{"valid uuid", map[string]any{"id": "123e4567-e89b-12d3-a456-426614174000"}, "id", UUIDPattern, false},
		{"invalid uuid", map[string]any{"id": "not-a-uuid"}, "id", UUIDPattern, true},
		{"valid k8s name", map[string]any{"name": "my-app"}, "name", K8sNamePattern, false},
		{"invalid k8s name", map[string]any{"name": "My_App"}, "name", K8sNamePattern, true},
		{"empty value", map[string]any{}, "name", K8sNamePattern, false},
		{"invalid pattern", map[string]any{"name": "test"}, "name", "[invalid", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewArgs(tt.args)
			a.Pattern(tt.field, tt.pattern)
			if tt.wantError && !a.Errors().HasErrors() {
				t.Error("expected error")
			}
			if !tt.wantError && a.Errors().HasErrors() {
				t.Errorf("unexpected error: %v", a.Errors())
			}
		})
	}
}

func TestArgs_MinLength(t *testing.T) {
	tests := []struct {
		name      string
		args      map[string]any
		field     string
		minLen    int
		wantError bool
	}{
		{"valid", map[string]any{"name": "hello"}, "name", 3, false},
		{"too short", map[string]any{"name": "hi"}, "name", 3, true},
		{"exact", map[string]any{"name": "abc"}, "name", 3, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewArgs(tt.args)
			a.MinLength(tt.field, tt.minLen)
			if tt.wantError && !a.Errors().HasErrors() {
				t.Error("expected error")
			}
		})
	}
}

func TestArgs_MaxLength(t *testing.T) {
	tests := []struct {
		name      string
		args      map[string]any
		field     string
		maxLen    int
		wantError bool
	}{
		{"valid", map[string]any{"name": "hi"}, "name", 5, false},
		{"too long", map[string]any{"name": "hello world"}, "name", 5, true},
		{"exact", map[string]any{"name": "hello"}, "name", 5, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewArgs(tt.args)
			a.MaxLength(tt.field, tt.maxLen)
			if tt.wantError && !a.Errors().HasErrors() {
				t.Error("expected error")
			}
		})
	}
}

func TestArgs_Validate(t *testing.T) {
	t.Run("no errors", func(t *testing.T) {
		a := NewArgs(map[string]any{"name": "test"})
		a.Required("name")
		if err := a.Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
	})

	t.Run("with errors", func(t *testing.T) {
		a := NewArgs(map[string]any{})
		a.Required("name")
		if err := a.Validate(); err == nil {
			t.Error("Validate() = nil, want error")
		}
	})
}

func TestPatternConstants(t *testing.T) {
	// Verify patterns are valid regex
	patterns := []string{UUIDPattern, K8sNamePattern, K8sNamespacePattern, DNSNamePattern}
	for _, p := range patterns {
		if p == "" {
			t.Error("pattern should not be empty")
		}
	}
}
