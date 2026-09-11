// pi: auth/oauth/pkce.ts.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// GeneratePKCE produces a PKCE code verifier and S256 challenge (pi:
// generatePKCE): a 32-byte base64url verifier plus its SHA-256 digest.
func GeneratePKCE() (verifier string, challenge string, err error) {
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(verifierBytes)
	digest := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(digest[:])
	return verifier, challenge, nil
}
