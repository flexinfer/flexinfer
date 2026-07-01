package backend

import "testing"

func TestSunshineBackendRegistered(t *testing.T) {
	b, ok := Get(NameSunshine)
	if !ok {
		t.Fatalf("sunshine backend not registered")
	}
	if b.Name() != NameSunshine {
		t.Errorf("Name() = %q, want %q", b.Name(), NameSunshine)
	}
}

func TestGamingAliasResolvesToSunshine(t *testing.T) {
	// The "gaming" alias must resolve to Sunshine (the default gaming backend),
	// NOT the legacy Steam Remote Play backend.
	b, ok := Get("gaming")
	if !ok {
		t.Fatal("alias 'gaming' not registered")
	}
	if b.Name() != NameSunshine {
		t.Errorf("Get(\"gaming\").Name() = %q, want %q", b.Name(), NameSunshine)
	}

	for _, alias := range []string{"moonlight", "sunshine-headless"} {
		got, ok := Get(alias)
		if !ok {
			t.Errorf("alias %q not registered", alias)
			continue
		}
		if got.Name() != NameSunshine {
			t.Errorf("Get(%q).Name() = %q, want %q", alias, got.Name(), NameSunshine)
		}
	}
}

func TestSteamNoLongerClaimsGamingAlias(t *testing.T) {
	// Steam is demoted to explicit opt-in; it keeps only "steam-headless".
	b, ok := Get("steam-headless")
	if !ok || b.Name() != NameSteam {
		t.Fatalf("Get(\"steam-headless\") = (%v, %v), want steam backend", b, ok)
	}
	if got, _ := Get("gaming"); got != nil && got.Name() == NameSteam {
		t.Error("'gaming' alias must not resolve to steam")
	}
}

func TestSunshineBackendProperties(t *testing.T) {
	b := &SunshineBackend{}

	if got := b.Port(); got != PortSunshine {
		t.Errorf("Port() = %d, want %d", got, PortSunshine)
	}
	if got := b.Image(GPUVendorAMD, "gfx1100"); got != "" {
		t.Errorf("Image() = %q, want empty (in-pod subprocess)", got)
	}
	cmd := b.Command()
	if len(cmd) != 1 || cmd[0] != "/opt/flexinfer/sunshine-headless.sh" {
		t.Errorf("Command() = %v, want [/opt/flexinfer/sunshine-headless.sh]", cmd)
	}
	if b.NeedsVolume() {
		t.Error("NeedsVolume() = true, want false (gaming state wired at DaemonSet layer)")
	}
	if got := b.DefaultIdleTimeout(); got != 0 {
		t.Errorf("DefaultIdleTimeout() = %v, want 0 (never auto-idle)", got)
	}
	if b.ReadinessProbe() == nil {
		t.Error("ReadinessProbe() = nil, want a TCP probe")
	}
}

func TestSunshineBackendGPUSupport(t *testing.T) {
	b := &SunshineBackend{}
	tests := []struct {
		vendor GPUVendor
		want   bool
	}{
		{GPUVendorAMD, true},
		{GPUVendorNVIDIA, true},
		{GPUVendorCPU, false},
	}
	for _, tt := range tests {
		if got := b.SupportsGPUVendor(tt.vendor); got != tt.want {
			t.Errorf("SupportsGPUVendor(%q) = %v, want %v", tt.vendor, got, tt.want)
		}
	}
}
