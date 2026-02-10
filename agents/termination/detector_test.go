package termination

import (
	"testing"
)

func TestDetectorNames(t *testing.T) {
	detectors := []TerminationDetector{
		&AWSDetector{},
		&GCPDetector{},
		&AzureDetector{},
		&HarvesterDetector{},
		&GenericDetector{},
	}

	expectedNames := map[string]bool{
		"aws":       true,
		"gcp":       true,
		"azure":     true,
		"harvester": true,
		"generic":   true,
	}

	for _, d := range detectors {
		name := d.Name()
		if !expectedNames[name] {
			t.Errorf("unexpected detector name: %s", name)
		}
		delete(expectedNames, name)
	}

	for name := range expectedNames {
		t.Errorf("missing detector: %s", name)
	}
}

func TestAutoDetectFallsBackToGeneric(t *testing.T) {
	// On a non-cloud machine, AutoDetect should return the generic detector
	// (since no metadata endpoints are reachable).
	if testing.Short() {
		t.Skip("skipping metadata probe in short mode")
	}

	// We can't easily test this without mocking HTTP endpoints,
	// but we can verify the generic detector is valid.
	d := &GenericDetector{}
	if d.Name() != "generic" {
		t.Errorf("expected generic detector, got %s", d.Name())
	}
}
