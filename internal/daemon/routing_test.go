package daemon

import (
	"testing"

	"github.com/crb2nu/loom/internal/router"
)

func TestParseRoutingPreference_Valid(t *testing.T) {
	tests := []struct {
		input string
		want  RoutingPreference
	}{
		{"local-only", RoutingLocalOnly},
		{"localonly", RoutingLocalOnly},
		{"local", RoutingLocalOnly},
		{"hub-only", RoutingHubOnly},
		{"hubonly", RoutingHubOnly},
		{"hub", RoutingHubOnly},
		{"prefer-local", RoutingPreferLocal},
		{"preferlocal", RoutingPreferLocal},
		{"prefer-hub", RoutingPreferHub},
		{"preferhub", RoutingPreferHub},
		{"health-based", RoutingHealthBased},
		{"healthbased", RoutingHealthBased},
		{"", RoutingHealthBased},
		{"  LOCAL-ONLY  ", RoutingLocalOnly},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseRoutingPreference(tt.input)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ParseRoutingPreference(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseRoutingPreference_Invalid(t *testing.T) {
	_, err := ParseRoutingPreference("invalid-value")
	if err == nil {
		t.Error("expected error for invalid preference")
	}
}

func TestRoutingPreference_String(t *testing.T) {
	tests := []struct {
		pref RoutingPreference
		want string
	}{
		{RoutingLocalOnly, "local-only"},
		{RoutingHubOnly, "hub-only"},
		{RoutingPreferLocal, "prefer-local"},
		{RoutingPreferHub, "prefer-hub"},
		{RoutingHealthBased, "health-based"},
	}

	for _, tt := range tests {
		if got := tt.pref.String(); got != tt.want {
			t.Errorf("%v.String() = %q, want %q", tt.pref, got, tt.want)
		}
	}
}

func TestValidateRoutingPreferences_Valid(t *testing.T) {
	prefs := map[string]string{
		"k8s":        "hub-only",
		"git":        "local-only",
		"prometheus": "prefer-hub",
	}
	if err := ValidateRoutingPreferences(prefs); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateRoutingPreferences_InvalidPreference(t *testing.T) {
	prefs := map[string]string{
		"k8s": "invalid",
	}
	if err := ValidateRoutingPreferences(prefs); err == nil {
		t.Error("expected error for invalid preference")
	}
}

func TestValidateRoutingPreferences_EmptyServer(t *testing.T) {
	prefs := map[string]string{
		"": "hub-only",
	}
	if err := ValidateRoutingPreferences(prefs); err == nil {
		t.Error("expected error for empty server name")
	}
}

func TestValidateRoutingPreferences_Empty(t *testing.T) {
	if err := ValidateRoutingPreferences(nil); err != nil {
		t.Errorf("nil map should be valid: %v", err)
	}
	if err := ValidateRoutingPreferences(map[string]string{}); err != nil {
		t.Errorf("empty map should be valid: %v", err)
	}
}

func TestApplyRoutingPreference_LocalOnly(t *testing.T) {
	// Forces local even when health-based says hub
	target, overridden := applyRoutingPreference(RoutingLocalOnly, router.TargetHub, true)
	if !overridden {
		t.Error("expected override")
	}
	if target != router.TargetLocal {
		t.Errorf("target = %v, want TargetLocal", target)
	}

	// No override when already local
	target, overridden = applyRoutingPreference(RoutingLocalOnly, router.TargetLocal, true)
	if overridden {
		t.Error("expected no override when already local")
	}
	if target != router.TargetLocal {
		t.Errorf("target = %v, want TargetLocal", target)
	}
}

func TestApplyRoutingPreference_HubOnly(t *testing.T) {
	// Forces hub when hub is available
	target, overridden := applyRoutingPreference(RoutingHubOnly, router.TargetLocal, true)
	if !overridden {
		t.Error("expected override")
	}
	if target != router.TargetHub {
		t.Errorf("target = %v, want TargetHub", target)
	}

	// Returns unavailable when no hub pool
	target, overridden = applyRoutingPreference(RoutingHubOnly, router.TargetLocal, false)
	if !overridden {
		t.Error("expected override")
	}
	if target != router.TargetUnavailable {
		t.Errorf("target = %v, want TargetUnavailable", target)
	}

	// No override when already hub
	target, overridden = applyRoutingPreference(RoutingHubOnly, router.TargetHub, true)
	if overridden {
		t.Error("expected no override when already hub")
	}
	if target != router.TargetHub {
		t.Errorf("target = %v, want TargetHub", target)
	}
}

func TestApplyRoutingPreference_PreferHub(t *testing.T) {
	// Overrides to hub when hub is available
	target, overridden := applyRoutingPreference(RoutingPreferHub, router.TargetLocal, true)
	if !overridden {
		t.Error("expected override")
	}
	if target != router.TargetHub {
		t.Errorf("target = %v, want TargetHub", target)
	}

	// Falls through when no hub
	target, overridden = applyRoutingPreference(RoutingPreferHub, router.TargetLocal, false)
	if overridden {
		t.Error("expected no override when hub unavailable (fallback to health-based)")
	}
	if target != router.TargetLocal {
		t.Errorf("target = %v, want TargetLocal", target)
	}
}

func TestApplyRoutingPreference_PreferLocal(t *testing.T) {
	// Overrides hub decision to local
	target, overridden := applyRoutingPreference(RoutingPreferLocal, router.TargetHub, true)
	if !overridden {
		t.Error("expected override")
	}
	if target != router.TargetLocal {
		t.Errorf("target = %v, want TargetLocal", target)
	}

	// No override when already local
	_, overridden = applyRoutingPreference(RoutingPreferLocal, router.TargetLocal, true)
	if overridden {
		t.Error("expected no override")
	}
}

func TestApplyRoutingPreference_HealthBased(t *testing.T) {
	// Passthrough - no override
	target, overridden := applyRoutingPreference(RoutingHealthBased, router.TargetLocal, true)
	if overridden {
		t.Error("expected no override for health-based")
	}
	if target != router.TargetLocal {
		t.Errorf("target = %v, want TargetLocal", target)
	}

	target, overridden = applyRoutingPreference(RoutingHealthBased, router.TargetHub, true)
	if overridden {
		t.Error("expected no override for health-based")
	}
	if target != router.TargetHub {
		t.Errorf("target = %v, want TargetHub", target)
	}
}
