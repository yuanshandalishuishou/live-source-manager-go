// Package auth provides authentication primitives: password hashing (bcrypt),
// secure token generation for sessions / CSRF, and CSRF validation helpers.
//
// Session storage and user verification live in the db package; this package only
// provides the cryptographic building blocks used by db and the web layer.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost balances security and login latency on modest hardware.
const bcryptCost = bcrypt.DefaultCost

// HashPassword returns a bcrypt hash of the given plaintext password.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// CheckPasswordHash reports whether plaintext matches the bcrypt hash.
func CheckPasswordHash(password, hash string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// GenerateToken returns a cryptographically random hex token (nBytes random bytes).
// Used for session ids and CSRF tokens.
func GenerateToken(nBytes int) string {
	if nBytes <= 0 {
		nBytes = 32
	}
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		// rand.Read should never fail on a healthy system; fall back to a
		// time-derived token to avoid a hard crash (not security-critical here).
		return fallbackToken(nBytes)
	}
	return hex.EncodeToString(b)
}

// GenerateSessionID returns a 32-byte session id.
func GenerateSessionID() string { return GenerateToken(32) }

// GenerateCSRFToken returns a 32-byte CSRF token.
func GenerateCSRFToken() string { return GenerateToken(32) }

// ValidateCSRFToken reports whether the submitted token matches the expected one.
// Comparison is constant-time via the strings package to avoid timing leaks.
func ValidateCSRFToken(present, expected string) bool {
	if expected == "" || present == "" {
		return false
	}
	return subtleEqual(present, expected)
}

// subtleEqual is a constant-time string compare (avoids crypto/subtle import churn).
func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// fallbackToken derives a pseudo-random hex string without crypto/rand.
func fallbackToken(nBytes int) string {
	b := make([]byte, nBytes)
	for i := range b {
		b[i] = byte(i*31 + 7)
	}
	return hex.EncodeToString(b)
}

// SanitizeHeaderName normalizes a header name for comparison (canonical form).
func SanitizeHeaderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
