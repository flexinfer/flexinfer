package hud

import "testing"

// TestAdminToken_RuntimeOverride covers the SIGHUP-driven env-file
// reload path: SetAdminToken installs a runtime override that
// AdminToken() reads in preference to the static config field; an
// empty token clears the override and falls back to config.
func TestAdminToken_RuntimeOverride(t *testing.T) {
	app := &App{config: Config{AdminToken: "static-token"}}

	if got := app.AdminToken(); got != "static-token" {
		t.Errorf("initial AdminToken() = %q, want %q", got, "static-token")
	}

	app.SetAdminToken("rotated-token")
	if got := app.AdminToken(); got != "rotated-token" {
		t.Errorf("after SetAdminToken AdminToken() = %q, want %q", got, "rotated-token")
	}

	// Empty token clears the runtime override → fall back to config.
	app.SetAdminToken("")
	if got := app.AdminToken(); got != "static-token" {
		t.Errorf("after clear AdminToken() = %q, want fallback %q", got, "static-token")
	}

	// Setting again after clear works (regression: atomic.Pointer
	// store(nil) must not break subsequent stores).
	app.SetAdminToken("rotated-2")
	if got := app.AdminToken(); got != "rotated-2" {
		t.Errorf("re-set AdminToken() = %q, want %q", got, "rotated-2")
	}
}

func TestAdminToken_EmptyConfigEmptyOverride(t *testing.T) {
	app := &App{}
	if got := app.AdminToken(); got != "" {
		t.Errorf("zero-value AdminToken() = %q, want empty", got)
	}
}
