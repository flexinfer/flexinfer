package validate

import (
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// Error / Errors types
// ---------------------------------------------------------------------------

func TestError_Error(t *testing.T) {
	t.Parallel()
	err := Error{Field: "name", Message: "is required"}
	want := "name: is required"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestErrors_Error(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		errs Errors
		want string
	}{
		{"empty", Errors{}, ""},
		{"nil", nil, ""},
		{"single", Errors{{Field: "a", Message: "bad"}}, "a: bad"},
		{"multiple", Errors{
			{Field: "name", Message: "is required"},
			{Field: "age", Message: "must be positive"},
		}, "name: is required; age: must be positive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.errs.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestErrors_HasErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		errs Errors
		want bool
	}{
		{"nil", nil, false},
		{"empty", Errors{}, false},
		{"non-empty", Errors{{Field: "x", Message: "y"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.errs.HasErrors(); got != tt.want {
				t.Errorf("HasErrors() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// NewArgs
// ---------------------------------------------------------------------------

func TestNewArgs(t *testing.T) {
	t.Parallel()
	t.Run("nil map initializes empty", func(t *testing.T) {
		t.Parallel()
		a := NewArgs(nil)
		if a == nil {
			t.Fatal("expected non-nil Args")
		}
		if a.args == nil {
			t.Error("expected initialized map")
		}
	})
	t.Run("with values", func(t *testing.T) {
		t.Parallel()
		a := NewArgs(map[string]any{"k": "v"})
		if a.args["k"] != "v" {
			t.Error("args not set correctly")
		}
	})
}

// ---------------------------------------------------------------------------
// Required
// ---------------------------------------------------------------------------

func TestArgs_Required(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		args      map[string]any
		field     string
		wantValue string
		wantError bool
	}{
		{"present", map[string]any{"name": "test"}, "name", "test", false},
		{"missing key", map[string]any{}, "name", "", true},
		{"empty string", map[string]any{"name": ""}, "name", "", true},
		{"wrong type int", map[string]any{"name": 123}, "name", "", true},
		{"wrong type bool", map[string]any{"name": true}, "name", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := NewArgs(tt.args)
			got := a.Required(tt.field)
			if got != tt.wantValue {
				t.Errorf("Required() = %q, want %q", got, tt.wantValue)
			}
			if tt.wantError && !a.Errors().HasErrors() {
				t.Error("expected validation error")
			}
			if !tt.wantError && a.Errors().HasErrors() {
				t.Errorf("unexpected error: %v", a.Errors())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// RequiredInt
// ---------------------------------------------------------------------------

func TestArgs_RequiredInt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		args      map[string]any
		field     string
		wantValue int
		wantError bool
	}{
		{"int value", map[string]any{"n": 42}, "n", 42, false},
		{"float64 value", map[string]any{"n": float64(7)}, "n", 7, false},
		{"string value (invalid)", map[string]any{"n": "42"}, "n", 0, true},
		{"missing", map[string]any{}, "n", 0, true},
		{"json.Number", map[string]any{"n": json.Number("99")}, "n", 99, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := NewArgs(tt.args)
			got := a.RequiredInt(tt.field)
			if got != tt.wantValue {
				t.Errorf("RequiredInt() = %d, want %d", got, tt.wantValue)
			}
			if tt.wantError && !a.Errors().HasErrors() {
				t.Error("expected validation error")
			}
			if !tt.wantError && a.Errors().HasErrors() {
				t.Errorf("unexpected error: %v", a.Errors())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// asInt (unexported helper)
// ---------------------------------------------------------------------------

func TestAsInt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  any
		wantV  int
		wantOK bool
	}{
		{"float64", float64(3.9), 3, true},
		{"float32", float32(2.1), 2, true},
		{"int", int(10), 10, true},
		{"int64", int64(20), 20, true},
		{"int32", int32(30), 30, true},
		{"uint", uint(40), 40, true},
		{"uint64", uint64(50), 50, true},
		{"uint32", uint32(60), 60, true},
		{"json.Number valid", json.Number("77"), 77, true},
		{"json.Number invalid", json.Number("not_a_number"), 0, false},
		{"string", "nope", 0, false},
		{"nil", nil, 0, false},
		{"bool", true, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v, ok := asInt(tt.input)
			if ok != tt.wantOK {
				t.Errorf("asInt(%v) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if v != tt.wantV {
				t.Errorf("asInt(%v) = %d, want %d", tt.input, v, tt.wantV)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// String
// ---------------------------------------------------------------------------

func TestArgs_String(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args map[string]any
		key  string
		def  string
		want string
	}{
		{"present", map[string]any{"k": "val"}, "k", "def", "val"},
		{"missing returns default", map[string]any{}, "k", "def", "def"},
		{"wrong type returns default", map[string]any{"k": 123}, "k", "def", "def"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := NewArgs(tt.args)
			if got := a.String(tt.key, tt.def); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Int
// ---------------------------------------------------------------------------

func TestArgs_Int(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args map[string]any
		key  string
		def  int
		want int
	}{
		{"present int", map[string]any{"n": 5}, "n", 0, 5},
		{"present float64", map[string]any{"n": float64(42)}, "n", 0, 42},
		{"missing returns default", map[string]any{}, "n", 10, 10},
		{"wrong type returns default", map[string]any{"n": "bad"}, "n", 10, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := NewArgs(tt.args)
			if got := a.Int(tt.key, tt.def); got != tt.want {
				t.Errorf("Int() = %d, want %d", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// IntRange
// ---------------------------------------------------------------------------

func TestArgs_IntRange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		args      map[string]any
		field     string
		def       int
		min, max  int
		wantValue int
		wantError bool
	}{
		{"within range", map[string]any{"n": float64(5)}, "n", 0, 1, 10, 5, false},
		{"below min", map[string]any{"n": float64(0)}, "n", 0, 1, 10, 0, true},
		{"above max", map[string]any{"n": float64(15)}, "n", 0, 1, 10, 15, true},
		{"default in range", map[string]any{}, "n", 5, 1, 10, 5, false},
		{"default out of range", map[string]any{}, "n", 0, 1, 10, 0, true},
		{"exact min", map[string]any{"n": float64(1)}, "n", 0, 1, 10, 1, false},
		{"exact max", map[string]any{"n": float64(10)}, "n", 0, 1, 10, 10, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := NewArgs(tt.args)
			got := a.IntRange(tt.field, tt.def, tt.min, tt.max)
			if got != tt.wantValue {
				t.Errorf("IntRange() = %d, want %d", got, tt.wantValue)
			}
			if tt.wantError && !a.Errors().HasErrors() {
				t.Error("expected validation error")
			}
			if !tt.wantError && a.Errors().HasErrors() {
				t.Errorf("unexpected error: %v", a.Errors())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Bool
// ---------------------------------------------------------------------------

func TestArgs_Bool(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args map[string]any
		key  string
		def  bool
		want bool
	}{
		{"true present", map[string]any{"f": true}, "f", false, true},
		{"false present", map[string]any{"f": false}, "f", true, false},
		{"missing returns default true", map[string]any{}, "f", true, true},
		{"missing returns default false", map[string]any{}, "f", false, false},
		{"wrong type returns default", map[string]any{"f": "yes"}, "f", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := NewArgs(tt.args)
			if got := a.Bool(tt.key, tt.def); got != tt.want {
				t.Errorf("Bool() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// StringSlice
// ---------------------------------------------------------------------------

func TestArgs_StringSlice(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args map[string]any
		want []string
	}{
		{"valid slice", map[string]any{"t": []any{"a", "b"}}, []string{"a", "b"}},
		{"mixed types filters non-strings", map[string]any{"t": []any{"a", 1, "c"}}, []string{"a", "c"}},
		{"missing returns nil", map[string]any{}, nil},
		{"wrong type returns nil", map[string]any{"t": "single"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := NewArgs(tt.args)
			got := a.StringSlice("t")
			if tt.want == nil {
				if got != nil {
					t.Errorf("StringSlice() = %v, want nil", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("StringSlice() len = %d, want %d", len(got), len(tt.want))
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("StringSlice()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// RequiredStringSlice
// ---------------------------------------------------------------------------

func TestArgs_RequiredStringSlice(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		args      map[string]any
		wantLen   int
		wantError bool
	}{
		{"present", map[string]any{"tags": []any{"x", "y"}}, 2, false},
		{"missing", map[string]any{}, 0, true},
		{"empty slice", map[string]any{"tags": []any{}}, 0, true},
		{"all non-strings", map[string]any{"tags": []any{1, 2}}, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := NewArgs(tt.args)
			got := a.RequiredStringSlice("tags")
			if tt.wantError {
				if !a.Errors().HasErrors() {
					t.Error("expected validation error")
				}
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
			} else {
				if a.Errors().HasErrors() {
					t.Errorf("unexpected error: %v", a.Errors())
				}
				if len(got) != tt.wantLen {
					t.Errorf("len = %d, want %d", len(got), tt.wantLen)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Float
// ---------------------------------------------------------------------------

func TestArgs_Float(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args map[string]any
		key  string
		def  float64
		want float64
	}{
		{"present", map[string]any{"v": 3.14}, "v", 0, 3.14},
		{"missing returns default", map[string]any{}, "v", 2.71, 2.71},
		{"wrong type returns default", map[string]any{"v": "nope"}, "v", 1.0, 1.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := NewArgs(tt.args)
			if got := a.Float(tt.key, tt.def); got != tt.want {
				t.Errorf("Float() = %f, want %f", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// RequiredBool
// ---------------------------------------------------------------------------

func TestArgs_RequiredBool(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		args      map[string]any
		wantValue bool
		wantError bool
	}{
		{"present true", map[string]any{"confirm": true}, true, false},
		{"present false", map[string]any{"confirm": false}, false, false},
		{"missing", map[string]any{}, false, true},
		{"wrong type", map[string]any{"confirm": "yes"}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := NewArgs(tt.args)
			got := a.RequiredBool("confirm")
			if got != tt.wantValue {
				t.Errorf("RequiredBool() = %v, want %v", got, tt.wantValue)
			}
			if tt.wantError && !a.Errors().HasErrors() {
				t.Error("expected validation error")
			}
			if !tt.wantError && a.Errors().HasErrors() {
				t.Errorf("unexpected error: %v", a.Errors())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Any
// ---------------------------------------------------------------------------

func TestArgs_Any(t *testing.T) {
	t.Parallel()
	t.Run("present", func(t *testing.T) {
		t.Parallel()
		a := NewArgs(map[string]any{"data": []int{1, 2, 3}})
		got := a.Any("data")
		if got == nil {
			t.Error("expected non-nil value")
		}
	})
	t.Run("missing", func(t *testing.T) {
		t.Parallel()
		a := NewArgs(map[string]any{})
		got := a.Any("data")
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})
}

// ---------------------------------------------------------------------------
// RequiredAny
// ---------------------------------------------------------------------------

func TestArgs_RequiredAny(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		args      map[string]any
		wantNil   bool
		wantError bool
	}{
		{"present", map[string]any{"obj": map[string]any{"a": 1}}, false, false},
		{"present with zero value", map[string]any{"obj": 0}, false, false},
		{"missing key", map[string]any{}, true, true},
		{"nil value", map[string]any{"obj": nil}, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := NewArgs(tt.args)
			got := a.RequiredAny("obj")
			if tt.wantNil && got != nil {
				t.Errorf("expected nil, got %v", got)
			}
			if !tt.wantNil && got == nil {
				t.Error("expected non-nil")
			}
			if tt.wantError && !a.Errors().HasErrors() {
				t.Error("expected validation error")
			}
			if !tt.wantError && a.Errors().HasErrors() {
				t.Errorf("unexpected error: %v", a.Errors())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Enum
// ---------------------------------------------------------------------------

func TestArgs_Enum(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		args      map[string]any
		field     string
		def       string
		allowed   []string
		wantValue string
		wantError bool
	}{
		{"valid value", map[string]any{"l": "info"}, "l", "debug", []string{"debug", "info", "warn"}, "info", false},
		{"invalid value returns default", map[string]any{"l": "trace"}, "l", "debug", []string{"debug", "info", "warn"}, "debug", true},
		{"missing uses default (allowed)", map[string]any{}, "l", "debug", []string{"debug", "info", "warn"}, "debug", false},
		{"empty default empty value", map[string]any{}, "l", "", []string{"a"}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := NewArgs(tt.args)
			got := a.Enum(tt.field, tt.def, tt.allowed...)
			if got != tt.wantValue {
				t.Errorf("Enum() = %q, want %q", got, tt.wantValue)
			}
			if tt.wantError && !a.Errors().HasErrors() {
				t.Error("expected validation error")
			}
			if !tt.wantError && a.Errors().HasErrors() {
				t.Errorf("unexpected error: %v", a.Errors())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Pattern
// ---------------------------------------------------------------------------

func TestArgs_Pattern(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		args      map[string]any
		field     string
		pattern   string
		wantValue string
		wantError bool
	}{
		{"matching", map[string]any{"id": "123e4567-e89b-12d3-a456-426614174000"}, "id", UUIDPattern, "123e4567-e89b-12d3-a456-426614174000", false},
		{"non-matching", map[string]any{"id": "not-a-uuid"}, "id", UUIDPattern, "not-a-uuid", true},
		{"empty value no error", map[string]any{}, "id", UUIDPattern, "", false},
		{"invalid regex pattern", map[string]any{"x": "test"}, "x", "[invalid", "test", true},
		{"k8s name valid", map[string]any{"n": "my-app"}, "n", K8sNamePattern, "my-app", false},
		{"k8s name invalid", map[string]any{"n": "My_App"}, "n", K8sNamePattern, "My_App", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := NewArgs(tt.args)
			got := a.Pattern(tt.field, tt.pattern)
			if got != tt.wantValue {
				t.Errorf("Pattern() = %q, want %q", got, tt.wantValue)
			}
			if tt.wantError && !a.Errors().HasErrors() {
				t.Error("expected validation error")
			}
			if !tt.wantError && a.Errors().HasErrors() {
				t.Errorf("unexpected error: %v", a.Errors())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// MinLength
// ---------------------------------------------------------------------------

func TestArgs_MinLength(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		args      map[string]any
		field     string
		minLen    int
		wantError bool
	}{
		{"valid length", map[string]any{"n": "hello"}, "n", 3, false},
		{"too short", map[string]any{"n": "hi"}, "n", 3, true},
		{"exact length", map[string]any{"n": "abc"}, "n", 3, false},
		{"empty missing field", map[string]any{}, "n", 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := NewArgs(tt.args)
			a.MinLength(tt.field, tt.minLen)
			if tt.wantError && !a.Errors().HasErrors() {
				t.Error("expected validation error")
			}
			if !tt.wantError && a.Errors().HasErrors() {
				t.Errorf("unexpected error: %v", a.Errors())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// MaxLength
// ---------------------------------------------------------------------------

func TestArgs_MaxLength(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		args      map[string]any
		field     string
		maxLen    int
		wantError bool
	}{
		{"valid length", map[string]any{"n": "hi"}, "n", 5, false},
		{"too long", map[string]any{"n": "hello world"}, "n", 5, true},
		{"exact length", map[string]any{"n": "hello"}, "n", 5, false},
		{"empty missing field ok", map[string]any{}, "n", 5, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := NewArgs(tt.args)
			a.MaxLength(tt.field, tt.maxLen)
			if tt.wantError && !a.Errors().HasErrors() {
				t.Error("expected validation error")
			}
			if !tt.wantError && a.Errors().HasErrors() {
				t.Errorf("unexpected error: %v", a.Errors())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Validate
// ---------------------------------------------------------------------------

func TestArgs_Validate(t *testing.T) {
	t.Parallel()
	t.Run("no errors returns nil", func(t *testing.T) {
		t.Parallel()
		a := NewArgs(map[string]any{"name": "ok"})
		a.Required("name")
		if err := a.Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
	})
	t.Run("with errors returns error", func(t *testing.T) {
		t.Parallel()
		a := NewArgs(map[string]any{})
		a.Required("name")
		a.Required("email")
		err := a.Validate()
		if err == nil {
			t.Fatal("Validate() = nil, want error")
		}
		errs, ok := err.(Errors)
		if !ok {
			t.Fatalf("expected Errors type, got %T", err)
		}
		if len(errs) != 2 {
			t.Errorf("expected 2 errors, got %d", len(errs))
		}
	})
}

// ---------------------------------------------------------------------------
// Errors() accessor
// ---------------------------------------------------------------------------

func TestArgs_Errors_Accessor(t *testing.T) {
	t.Parallel()
	a := NewArgs(map[string]any{})
	if a.Errors().HasErrors() {
		t.Error("fresh Args should have no errors")
	}
	a.Required("missing")
	if !a.Errors().HasErrors() {
		t.Error("should have errors after failed Required")
	}
}

// ---------------------------------------------------------------------------
// NormalizePage
// ---------------------------------------------------------------------------

func TestNormalizePage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input int
		want  int
	}{
		{"zero becomes 1", 0, 1},
		{"negative becomes 1", -5, 1},
		{"positive stays", 3, 3},
		{"one stays", 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := NormalizePage(tt.input); got != tt.want {
				t.Errorf("NormalizePage(%d) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// NormalizePerPage
// ---------------------------------------------------------------------------

func TestNormalizePerPage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		perPage int
		def     int
		max     int
		want    int
	}{
		{"zero becomes default", 0, 30, 100, 30},
		{"negative becomes default", -1, 30, 100, 30},
		{"over max becomes max", 200, 30, 100, 100},
		{"normal value stays", 50, 30, 100, 50},
		{"exact max stays", 100, 30, 100, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := NormalizePerPage(tt.perPage, tt.def, tt.max); got != tt.want {
				t.Errorf("NormalizePerPage(%d, %d, %d) = %d, want %d",
					tt.perPage, tt.def, tt.max, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// GetPagination
// ---------------------------------------------------------------------------

func TestArgs_GetPagination(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		args        map[string]any
		wantPage    int
		wantPerPage int
	}{
		{"defaults", map[string]any{}, 1, 30},
		{"custom page", map[string]any{"page": float64(3)}, 3, 30},
		{"custom per_page", map[string]any{"per_page": float64(50)}, 1, 50},
		{"both custom", map[string]any{"page": float64(2), "per_page": float64(10)}, 2, 10},
		{"zero page normalizes", map[string]any{"page": float64(0)}, 1, 30},
		{"negative page normalizes", map[string]any{"page": float64(-1)}, 1, 30},
		{"over max per_page clamps", map[string]any{"per_page": float64(999)}, 1, 100},
		{"zero per_page normalizes", map[string]any{"per_page": float64(0)}, 1, 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := NewArgs(tt.args)
			p := a.GetPagination()
			if p.Page != tt.wantPage {
				t.Errorf("Page = %d, want %d", p.Page, tt.wantPage)
			}
			if p.PerPage != tt.wantPerPage {
				t.Errorf("PerPage = %d, want %d", p.PerPage, tt.wantPerPage)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Pattern constants are valid regex
// ---------------------------------------------------------------------------

func TestPatternConstants(t *testing.T) {
	t.Parallel()
	patterns := map[string]string{
		"UUID":         UUIDPattern,
		"K8sName":      K8sNamePattern,
		"K8sNamespace": K8sNamespacePattern,
		"DNSName":      DNSNamePattern,
	}
	for name, p := range patterns {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if p == "" {
				t.Errorf("%s pattern is empty", name)
			}
		})
	}
}
