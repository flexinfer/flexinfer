package backend

import (
	"os"
	"strings"
)

// ImageRule defines a single image resolution rule.
// Rules are evaluated in order; the first match wins.
type ImageRule struct {
	// Vendor restricts this rule to a specific GPU vendor.
	// Empty string means "any vendor" (used for global defaults).
	Vendor GPUVendor

	// ArchPrefix restricts this rule to architectures matching this prefix.
	// Empty string means "any arch within the vendor".
	ArchPrefix string

	// EnvVar is the environment variable checked for a runtime override.
	// If set and non-empty, its value is returned immediately.
	EnvVar string

	// Default is the fallback image when no env var override is set.
	// Empty string means "skip this rule" (fall through to the next).
	Default string
}

// ResolveImage walks the rules in order and returns the image from the first
// matching rule. A rule matches when:
//  1. Vendor is empty OR equals the given vendor.
//  2. ArchPrefix is empty OR the given arch starts with it.
//
// Within a matching rule, the env var override (if any) takes precedence
// over the built-in default. If both are empty the rule is skipped.
func ResolveImage(rules []ImageRule, vendor GPUVendor, arch string) string {
	for _, r := range rules {
		if r.Vendor != "" && r.Vendor != vendor {
			continue
		}
		if r.ArchPrefix != "" && !strings.HasPrefix(arch, r.ArchPrefix) {
			continue
		}
		if r.EnvVar != "" {
			if img := os.Getenv(r.EnvVar); img != "" {
				return img
			}
		}
		if r.Default != "" {
			return r.Default
		}
		// Rule matched vendor+arch but had no env override and no default:
		// fall through to the next rule (allows arch-env-only rules that
		// cascade to a vendor-level default).
	}
	return ""
}

// ApplyImageDigest pins image to the given content digest for reproducible
// deployments. Any existing tag or digest on image is stripped and replaced
// with "@<digest>". A bare hex digest is normalized to "sha256:<hex>".
//
// Empty image or empty digest returns image unchanged. The registry-host port
// colon (e.g. "registry:5000/...") is preserved: only a tag colon appearing
// after the last path separator is treated as a tag.
func ApplyImageDigest(image, digest string) string {
	if image == "" || digest == "" {
		return image
	}
	if !strings.Contains(digest, ":") {
		digest = "sha256:" + digest
	}
	// Strip an existing digest ("repo@sha256:...").
	if at := strings.LastIndex(image, "@"); at != -1 {
		image = image[:at]
	}
	// Strip an existing tag: the last ':' that comes after the last '/', so a
	// registry-host port colon is left intact.
	if colon := strings.LastIndex(image, ":"); colon > strings.LastIndex(image, "/") {
		image = image[:colon]
	}
	return image + "@" + digest
}
