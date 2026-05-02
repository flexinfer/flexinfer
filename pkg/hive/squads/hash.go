package squads

import (
	"crypto/sha256"
	"encoding/hex"

	"gopkg.in/yaml.v3"
)

// stableSHA hashes the given bytes with SHA-256 and returns the hex-encoded
// digest. Used as the default content-fingerprint for squad manifests when
// the caller does not plug in a git-aware SHAFn.
func stableSHA(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// marshalForHash serialises a value with the workspace yaml encoder. It
// returns ([], nil) on encode error so callers can treat the result as a
// best-effort fingerprint input rather than a load-bearing operation.
func marshalForHash(v any) ([]byte, error) {
	out, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	return out, nil
}
