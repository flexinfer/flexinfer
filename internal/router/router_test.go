package router

import (
	"testing"
)

func TestNew(t *testing.T) {
	t.Parallel()

	r := New(Config{})
	if r == nil {
		t.Fatal("expected non-nil router")
	}
}

func TestTargetConstants(t *testing.T) {
	t.Parallel()

	// Verify the forwarded constants have the expected values
	if TargetLocal == TargetHub {
		t.Error("TargetLocal and TargetHub should differ")
	}
	if TargetLocal == TargetUnavailable {
		t.Error("TargetLocal and TargetUnavailable should differ")
	}
}
