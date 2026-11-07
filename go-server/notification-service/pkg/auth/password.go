// Package auth holds the people side of access: users, passwords, sessions,
// roles, login throttling, an audit trail and OpenID Connect sign-in. The
// shared API key stays for service-to-service calls and is handled by the
// server; nothing here depends on HTTP.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (OWASP's minimum profile: 19 MiB, 2 passes, 1 lane).
const (
	argonMemoryKiB = 19 * 1024
	argonTime      = 2
	argonThreads   = 1
	argonKeyLen    = 32
	saltLen        = 16

	// MinPasswordLength is the shortest password accepted. Length matters more than composition rules.
	MinPasswordLength = 12
	// maxPasswordLength bounds hashing work for a hostile request.
	maxPasswordLength = 256
)

// ErrWeakPassword is returned for a password that is too short or too long.
var ErrWeakPassword = fmt.Errorf("password must be between %d and %d characters", MinPasswordLength, maxPasswordLength)

// CheckPassword reports whether a new password is acceptable.
func CheckPassword(p string) error {
	if n := len([]rune(p)); n < MinPasswordLength || len(p) > maxPasswordLength {
		return ErrWeakPassword
	}
	return nil
}

// HashPassword returns a self-describing argon2id hash with a random salt.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	return fmt.Sprintf("argon2id$m=%d,t=%d,p=%d$%s$%s", argonMemoryKiB, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks a password against a hash from HashPassword in constant time.
func VerifyPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "argon2id" || len(password) > maxPasswordLength {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[1], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m == 0 || t == 0 || p == 0 || m > 1<<20 {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[2])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil || len(want) == 0 || len(want) > maxHashBytes {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want))) //nolint:gosec // len(want) is bounded by maxHashBytes above
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is verified against when the account does not exist, so a login
// for an unknown email takes as long as one for a real account.
var dummyHash = func() string {
	h, err := HashPassword("not-a-real-password")
	if err != nil {
		panic(errors.New("auth: cannot create dummy hash"))
	}
	return h
}()

// maxHashBytes bounds the length of a stored hash we are willing to verify against.
const maxHashBytes = 1024
