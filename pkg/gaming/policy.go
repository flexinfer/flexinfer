// Package gaming holds the GitOps-owned gaming lease policy shared by the
// GamingSession controller and the `flexinfer game` CLI. The policy is
// deliberately not part of GamingSessionSpec: a session must never be able to
// approve its own runtime image or extend its own ceiling — those decisions
// stay in Git (chart values -> ConfigMap) with the node's gaming capability.
package gaming

import (
	"strings"
	"time"
)

// PolicyConfigMapName is the well-known ConfigMap the controller and CLI read
// the policy from, in the namespace the GamingSession lives in. Rendered by
// the chart from .Values.gaming.
const PolicyConfigMapName = "flexinfer-gaming-policy"

// ConfigMap keys understood by ParsePolicy.
const (
	KeyApprovedImages = "approvedImages"
	KeyDefaultTTL     = "defaultTTL"
	KeyMaxTTL         = "maxTTL"
	KeyDrainTimeout   = "drainTimeout"
	KeyRequireOwner   = "requireOwner"
)

// Policy is the resolved gaming lease policy.
type Policy struct {
	// ApprovedImages lists runtime images allowed to host a gaming session.
	// Entries are full image refs (registry/repo@sha256:... or registry/repo:tag)
	// or bare "sha256:..." digests. EMPTY MEANS REFUSE ACTIVATION: an image only
	// joins this list after the exact-hardware streaming + security canary
	// passes (see docs/dev/gaming-image-canary.md).
	ApprovedImages []string
	// DefaultTTL bounds sessions that set neither spec.ttl nor spec.expiresAt.
	DefaultTTL time.Duration
	// MaxTTL caps every session's effective deadline at creation+MaxTTL.
	// Zero disables the cap.
	MaxTTL time.Duration
	// DrainTimeout is how long activation waits for inference pods to leave
	// the node before reporting DrainBlocked, unless the session overrides it.
	DrainTimeout time.Duration
	// RequireOwner refuses activation of sessions without spec.owner.
	RequireOwner bool
}

// Default returns the built-in policy used when the ConfigMap is absent:
// secure (no approved images, owner required) with sane lease bounds.
func Default() Policy {
	return Policy{
		ApprovedImages: nil,
		DefaultTTL:     2 * time.Hour,
		MaxTTL:         8 * time.Hour,
		DrainTimeout:   10 * time.Minute,
		RequireOwner:   true,
	}
}

// ParsePolicy resolves a policy from ConfigMap data, falling back to Default()
// per key. Unparseable durations/bools keep their defaults rather than failing
// the reconcile — the gate that matters (ApprovedImages) is fail-closed by
// construction because an unreadable list parses to empty.
func ParsePolicy(data map[string]string) Policy {
	p := Default()
	if data == nil {
		return p
	}
	if raw, ok := data[KeyApprovedImages]; ok {
		p.ApprovedImages = parseImageList(raw)
	}
	if d, ok := parseDuration(data[KeyDefaultTTL]); ok {
		p.DefaultTTL = d
	}
	if d, ok := parseDuration(data[KeyMaxTTL]); ok {
		p.MaxTTL = d
	}
	if d, ok := parseDuration(data[KeyDrainTimeout]); ok {
		p.DrainTimeout = d
	}
	if raw, ok := data[KeyRequireOwner]; ok {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "false", "no", "0":
			p.RequireOwner = false
		case "true", "yes", "1":
			p.RequireOwner = true
		}
	}
	return p
}

// parseImageList splits a newline/comma separated list, dropping blanks and
// full-line comments so the values file can annotate why a digest is approved.
func parseImageList(raw string) []string {
	var out []string
	for _, line := range strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == ',' }) {
		entry := strings.TrimSpace(line)
		if entry == "" || strings.HasPrefix(entry, "#") {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func parseDuration(raw string) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return 0, false
	}
	return d, true
}

// ImageApproved reports whether a runtime container's image satisfies the
// approved list. Both the pod spec image (ref as pinned in values) and the
// containerStatuses imageID (registry-resolved digest form) should be offered;
// a match on either passes. Bare "sha256:..." entries match any ref pinned to
// that digest.
func (p Policy) ImageApproved(image, imageID string) bool {
	for _, entry := range p.ApprovedImages {
		if entry == image || entry == imageID {
			return true
		}
		if strings.HasPrefix(entry, "sha256:") {
			if strings.HasSuffix(image, "@"+entry) || strings.HasSuffix(imageID, "@"+entry) {
				return true
			}
		}
	}
	return false
}
