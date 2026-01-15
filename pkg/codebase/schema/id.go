package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

func ShortSHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

func ContentHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:32]
}

func ChunkID(repoID, filePath string, startLine, endLine int, contentHash string) string {
	return ShortSHA256Hex(fmt.Sprintf("%s:%s:%d:%d:%s", repoID, filePath, startLine, endLine, contentHash))
}
