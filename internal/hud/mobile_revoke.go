package hud

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// MobileTokenRevocationList tracks revoked mobile operator tokens in memory.
// Tokens are stored as SHA-256 hashes to avoid keeping raw secrets.
type MobileTokenRevocationList struct {
	mu      sync.RWMutex
	revoked map[string]time.Time // token hash → revocation time
}

// NewMobileTokenRevocationList creates an empty revocation list.
func NewMobileTokenRevocationList() *MobileTokenRevocationList {
	return &MobileTokenRevocationList{
		revoked: make(map[string]time.Time),
	}
}

// Revoke adds a token to the revocation list.
func (rl *MobileTokenRevocationList) Revoke(token string) {
	h := hashToken(token)
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.revoked[h] = time.Now().UTC()
}

// IsRevoked checks whether a token has been revoked.
func (rl *MobileTokenRevocationList) IsRevoked(token string) bool {
	h := hashToken(token)
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	_, ok := rl.revoked[h]
	return ok
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
