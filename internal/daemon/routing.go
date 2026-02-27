// Package daemon provides the main Loom daemon orchestrator.
package daemon

import (
	"fmt"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/router"
)

const preferHubBackoffDuration = 30 * time.Second

// RoutingPreference controls how a specific server's traffic is routed.
type RoutingPreference int

const (
	// RoutingHealthBased uses the default health-aware routing (no override).
	RoutingHealthBased RoutingPreference = iota
	// RoutingLocalOnly forces all traffic to the local backend.
	RoutingLocalOnly
	// RoutingHubOnly forces all traffic to the hub backend.
	RoutingHubOnly
	// RoutingPreferLocal prefers local, falls back to hub when unhealthy.
	RoutingPreferLocal
	// RoutingPreferHub prefers hub, falls back to local when unavailable.
	RoutingPreferHub
)

// ParseRoutingPreference parses a string into a RoutingPreference.
func ParseRoutingPreference(s string) (RoutingPreference, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "health-based", "healthbased", "":
		return RoutingHealthBased, nil
	case "local-only", "localonly", "local":
		return RoutingLocalOnly, nil
	case "hub-only", "hubonly", "hub":
		return RoutingHubOnly, nil
	case "prefer-local", "preferlocal":
		return RoutingPreferLocal, nil
	case "prefer-hub", "preferhub":
		return RoutingPreferHub, nil
	default:
		return RoutingHealthBased, fmt.Errorf("unknown routing preference: %q (valid: local-only, hub-only, prefer-local, prefer-hub, health-based)", s)
	}
}

// String returns the canonical string representation.
func (r RoutingPreference) String() string {
	switch r {
	case RoutingLocalOnly:
		return "local-only"
	case RoutingHubOnly:
		return "hub-only"
	case RoutingPreferLocal:
		return "prefer-local"
	case RoutingPreferHub:
		return "prefer-hub"
	default:
		return "health-based"
	}
}

// ValidateRoutingPreferences validates a map of server name -> preference strings.
func ValidateRoutingPreferences(prefs map[string]string) error {
	for server, pref := range prefs {
		if server == "" {
			return fmt.Errorf("empty server name in routing preferences")
		}
		if _, err := ParseRoutingPreference(pref); err != nil {
			return fmt.Errorf("server %q: %w", server, err)
		}
	}
	return nil
}

// applyRoutingPreference overrides a route decision based on per-server config.
// Returns the (possibly modified) target and whether the decision was overridden.
func applyRoutingPreference(pref RoutingPreference, original router.Target, hasHub bool) (router.Target, bool) {
	return applyRoutingPreferenceWithOptions(pref, original, hasHub, true)
}

func applyRoutingPreferenceWithOptions(pref RoutingPreference, original router.Target, hasHub bool, allowPreferHub bool) (router.Target, bool) {
	switch pref {
	case RoutingLocalOnly:
		if original != router.TargetLocal {
			return router.TargetLocal, true
		}
	case RoutingHubOnly:
		if !hasHub {
			return router.TargetUnavailable, true
		}
		if original != router.TargetHub {
			return router.TargetHub, true
		}
	case RoutingPreferHub:
		if !allowPreferHub {
			return original, false
		}
		if hasHub && original != router.TargetHub {
			return router.TargetHub, true
		}
		// Fall through to health-based if no hub
	case RoutingPreferLocal:
		if original != router.TargetLocal {
			return router.TargetLocal, true
		}
	case RoutingHealthBased:
		// No override
	}
	return original, false
}

func (d *Daemon) setPreferHubBackoff(serverName string, dur time.Duration) time.Time {
	if strings.TrimSpace(serverName) == "" {
		return time.Time{}
	}
	if dur <= 0 {
		dur = preferHubBackoffDuration
	}
	until := time.Now().Add(dur)
	d.preferHubBackoff.Store(serverName, until)
	return until
}

func (d *Daemon) preferHubBackoffActive(serverName string) (bool, time.Time) {
	if strings.TrimSpace(serverName) == "" {
		return false, time.Time{}
	}
	v, ok := d.preferHubBackoff.Load(serverName)
	if !ok {
		return false, time.Time{}
	}
	until, ok := v.(time.Time)
	if !ok {
		d.preferHubBackoff.Delete(serverName)
		return false, time.Time{}
	}
	if time.Now().Before(until) {
		return true, until
	}
	d.preferHubBackoff.Delete(serverName)
	return false, time.Time{}
}
