package userauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"strings"
	"unicode/utf8"

	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/phoneid"
	"golang.org/x/crypto/argon2"
)

const passwordPrefix = "$argon2id$v=19$m=19456,t=2,p=1$"

func normalizePhone(raw string) (string, error) { return phoneid.Normalize(raw) }
func maskPhone(phone string) string             { return phoneid.Mask(phone) }

func validatePassword(password string) error {
	n := utf8.RuneCountInString(password)
	if !utf8.ValidString(password) || n < 8 || n > 128 || len(password) > 512 {
		return entity.Fail("INVALID_ARGUMENT", "password must contain 8 to 128 Unicode characters, at most 512 UTF-8 bytes")
	}
	return nil
}

func hashPassword(password string) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", unavailable()
	}
	key := argon2.IDKey([]byte(password), salt, 2, 19456, 1, 32)
	return passwordPrefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}

func passwordMatches(encoded, password string) bool {
	if !strings.HasPrefix(encoded, passwordPrefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(encoded, passwordPrefix), "$")
	if len(parts) != 2 {
		return false
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[0])
	if err != nil || len(salt) != 16 {
		return false
	}
	want, err := base64.RawStdEncoding.Strict().DecodeString(parts[1])
	if err != nil || len(want) != 32 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, 2, 19456, 1, 32)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// Unknown users perform the same Argon2 work as known users with wrong passwords.
// The salt here protects no real credential and is never persisted.
func dummyPasswordWork(password string) {
	_ = argon2.IDKey([]byte(password), []byte("unknown-user-salt"), 2, 19456, 1, 32)
}

func unavailable() error {
	return entity.Fail("DEPENDENCY_UNAVAILABLE", "authentication service is temporarily unavailable")
}
