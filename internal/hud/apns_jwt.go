package hud

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
)

// readKeyFile reads the .p8 key file from disk.
func readKeyFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// parseP8Key parses a PEM-encoded PKCS#8 ECDSA private key (.p8 file).
func parseP8Key(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in key data")
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse PKCS8 key: %w", err)
	}

	ecKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is not ECDSA")
	}
	return ecKey, nil
}

// signES256 creates an ES256-signed JWT from header and claims JSON.
func signES256(headerJSON, claimsJSON []byte, key *ecdsa.PrivateKey) (string, error) {
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)

	signingInput := headerB64 + "." + claimsB64
	hash := sha256.Sum256([]byte(signingInput))

	r, s, err := ecdsa.Sign(rand.Reader, key, hash[:])
	if err != nil {
		return "", fmt.Errorf("ECDSA sign: %w", err)
	}

	// Encode r and s as fixed-size 32-byte big-endian values.
	keyBytes := 32
	if key.Curve == elliptic.P384() {
		keyBytes = 48
	}
	rBytes := padBigInt(r, keyBytes)
	sBytes := padBigInt(s, keyBytes)

	sig := append(rBytes, sBytes...)
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)

	return signingInput + "." + sigB64, nil
}

// padBigInt returns the big-endian representation of n, left-padded to size bytes.
func padBigInt(n *big.Int, size int) []byte {
	b := n.Bytes()
	if len(b) >= size {
		return b[:size]
	}
	padded := make([]byte, size)
	copy(padded[size-len(b):], b)
	return padded
}
