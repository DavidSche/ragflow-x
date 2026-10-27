// Package keygen generates attacker-resistant API keys and their hashes.
package keygen

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	prefix = "rgx_"
	length = 40
)

// New creates a prefixed random API key.
func New() (string, error) {
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buf), nil
}

// Hash returns a sha256 hex digest of a key.
func Hash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Prefix returns the short displayable prefix of a key.
func Prefix(key string) string {
	if len(key) <= 8 {
		return key
	}
	return key[:8] + "..." + key[len(key)-4:]
}

// IsValidRaw asserts the key has the expected prefix and length.
func IsValidRaw(key string) bool {
	return strings.HasPrefix(key, prefix) && len(key) > len(prefix)
}
