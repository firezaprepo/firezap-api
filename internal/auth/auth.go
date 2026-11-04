// Package auth generates and checks API keys.
//
// Instance keys are shown once, when the instance is created, and only their SHA-256 hash is
// stored: a leaked database does not leak working keys.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"strings"
)

// keyPrefix makes FireZAP API keys easy to recognize (for example by secret scanners).
const keyPrefix = "fzk_"

// NewKey returns a new random API key.
func NewKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return keyPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// NewSecret returns a random secret for webhook signatures.
func NewSecret() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// HashKey returns the value stored in the database for a key.
func HashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Equal compares two secrets in constant time.
func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// BearerToken extracts the token from an "Authorization: Bearer <token>" header value.
func BearerToken(header string) string {
	const prefix = "bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)

// ValidInstanceID reports whether an ID is safe to use in URLs and logs:
// 3 to 40 lowercase letters, digits or hyphens, not starting or ending with a hyphen.
func ValidInstanceID(id string) bool {
	return idPattern.MatchString(id)
}
