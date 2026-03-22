package envutil

import (
	"testing"
	"time"
)

func TestStringOrDefault(t *testing.T) {
	const key = "TEST_ENVUTIL_STRING"

	t.Setenv(key, "custom")
	if got := StringOrDefault(key, "fallback"); got != "custom" {
		t.Errorf("StringOrDefault() = %q, want %q", got, "custom")
	}

	t.Setenv(key, "  ")
	if got := StringOrDefault(key, "fallback"); got != "fallback" {
		t.Errorf("StringOrDefault(whitespace) = %q, want %q", got, "fallback")
	}

	t.Setenv(key, "")
	if got := StringOrDefault(key, "fallback"); got != "fallback" {
		t.Errorf("StringOrDefault(empty) = %q, want %q", got, "fallback")
	}
}

func TestIntOrDefault(t *testing.T) {
	const key = "TEST_ENVUTIL_INT"

	t.Setenv(key, "42")
	if got := IntOrDefault(key, 10); got != 42 {
		t.Errorf("IntOrDefault() = %d, want 42", got)
	}

	t.Setenv(key, "0")
	if got := IntOrDefault(key, 10); got != 10 {
		t.Errorf("IntOrDefault(0) = %d, want 10", got)
	}

	t.Setenv(key, "-1")
	if got := IntOrDefault(key, 10); got != 10 {
		t.Errorf("IntOrDefault(-1) = %d, want 10", got)
	}

	t.Setenv(key, "notanumber")
	if got := IntOrDefault(key, 10); got != 10 {
		t.Errorf("IntOrDefault(invalid) = %d, want 10", got)
	}

	t.Setenv(key, "")
	if got := IntOrDefault(key, 10); got != 10 {
		t.Errorf("IntOrDefault(empty) = %d, want 10", got)
	}
}

func TestBoolOrDefault(t *testing.T) {
	const key = "TEST_ENVUTIL_BOOL"

	for _, v := range []string{"1", "true", "yes", "on", "TRUE", "Yes"} {
		t.Setenv(key, v)
		if got := BoolOrDefault(key, false); !got {
			t.Errorf("BoolOrDefault(%q) = false, want true", v)
		}
	}

	for _, v := range []string{"0", "false", "no", "off", "FALSE", "No"} {
		t.Setenv(key, v)
		if got := BoolOrDefault(key, true); got {
			t.Errorf("BoolOrDefault(%q) = true, want false", v)
		}
	}

	t.Setenv(key, "garbage")
	if got := BoolOrDefault(key, true); !got {
		t.Errorf("BoolOrDefault(garbage, true) = false, want true")
	}
	if got := BoolOrDefault(key, false); got {
		t.Errorf("BoolOrDefault(garbage, false) = true, want false")
	}

	t.Setenv(key, "")
	if got := BoolOrDefault(key, true); !got {
		t.Errorf("BoolOrDefault(empty, true) = false, want true")
	}
}

func TestDurationOrDefault(t *testing.T) {
	const key = "TEST_ENVUTIL_DURATION"

	t.Setenv(key, "5s")
	if got := DurationOrDefault(key, time.Minute); got != 5*time.Second {
		t.Errorf("DurationOrDefault() = %s, want 5s", got)
	}

	t.Setenv(key, "invalid")
	if got := DurationOrDefault(key, time.Minute); got != time.Minute {
		t.Errorf("DurationOrDefault(invalid) = %s, want 1m0s", got)
	}

	t.Setenv(key, "")
	if got := DurationOrDefault(key, time.Minute); got != time.Minute {
		t.Errorf("DurationOrDefault(empty) = %s, want 1m0s", got)
	}
}

func TestFloat64OrDefault(t *testing.T) {
	const key = "TEST_ENVUTIL_FLOAT"

	t.Setenv(key, "3.14")
	if got := Float64OrDefault(key, 1.0); got != 3.14 {
		t.Errorf("Float64OrDefault() = %f, want 3.14", got)
	}

	t.Setenv(key, "invalid")
	if got := Float64OrDefault(key, 1.0); got != 1.0 {
		t.Errorf("Float64OrDefault(invalid) = %f, want 1.0", got)
	}

	t.Setenv(key, "")
	if got := Float64OrDefault(key, 1.0); got != 1.0 {
		t.Errorf("Float64OrDefault(empty) = %f, want 1.0", got)
	}
}
