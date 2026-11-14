// Package auth signs and verifies owner session tokens and review links.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Signer creates HMAC signatures with a server secret.
type Signer struct {
	key []byte
}

// NewSigner returns a Signer using secret as the HMAC key.
func NewSigner(secret []byte) Signer { return Signer{key: secret} }

// Sign returns a signature binding purpose to an expiry time.
func (s Signer) Sign(purpose string, expires int64) string {
	mac := hmac.New(sha256.New, s.key)
	fmt.Fprintf(mac, "%s|%d", purpose, expires)
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify reports whether sig is valid for purpose and has not expired.
func (s Signer) Verify(purpose string, expires int64, sig string, now time.Time) bool {
	if now.Unix() > expires {
		return false
	}
	return hmac.Equal([]byte(s.Sign(purpose, expires)), []byte(sig))
}

const ownerPurpose = "owner-session"

// OwnerToken returns a session token valid for ttl.
func (s Signer) OwnerToken(now time.Time, ttl time.Duration) string {
	exp := now.Add(ttl).Unix()
	return fmt.Sprintf("%d.%s", exp, s.Sign(ownerPurpose, exp))
}

// VerifyOwnerToken reports whether token is a valid, unexpired owner token.
func (s Signer) VerifyOwnerToken(token string, now time.Time) bool {
	expText, sig, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(expText, 10, 64)
	if err != nil {
		return false
	}
	return s.Verify(ownerPurpose, exp, sig, now)
}

// PasswordMatches compares passwords in constant time.
func PasswordMatches(given, want string) bool {
	g := sha256.Sum256([]byte(given))
	w := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(g[:], w[:]) == 1
}
