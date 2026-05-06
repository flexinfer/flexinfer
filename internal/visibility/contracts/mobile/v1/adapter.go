// Package v1 is reserved for mobile companion v1 wire adapters.
//
// The mobile v1 wire format is FROZEN. See docs/MOBILE_COMPANION_API.md and
// the byte-identity goldens at internal/contracts/testdata/mobile_*.golden
// for the canonical proof. Any change to the bytes the iOS companion
// receives must go through a deliberate v2 surface; this package is the
// landing zone for that future work.
//
// Today the mobile API is wired from internal/hud/api_mobile.go directly
// against bridge DTOs. Later EPIC 2 (#66) slices will move the adapter
// shims here so the HUD handlers depend on internal/visibility/contracts
// instead of internal/hud/bridge for mobile responses, without changing
// the bytes on the wire.
package v1

// Reserved is a placeholder export so the package is non-empty and reachable
// via go-doc. Reserved for future mobile v1 adapters; the mobile v1 wire
// format is frozen, see docs/MOBILE_COMPANION_API.md.
func Reserved() {}
