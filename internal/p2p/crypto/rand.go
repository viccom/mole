//go:build p2p

package crypto

import (
	cryptoRand "crypto/rand"
	"encoding/binary"
	"math/big"
	mrand "math/rand"
	"strings"
	"time"
	"unicode"
)

// charset is the alphanumeric alphabet used by the random-string helpers
// (gonc parity). Used for MQTT topicSalt / clientID / tid derivation.
const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// GenerateSecureRandomString returns a crypto-random string of the given
// length from charset, re-rolling if the result is a weak password.
// Replaces gonc secure.GenerateSecureRandomString (signature + behavior parity).
func GenerateSecureRandomString(length int) (string, error) {
	for {
		result, err := generateRandomString(length)
		if err != nil {
			return "", err
		}
		if !IsWeakPassword(result) {
			return result, nil
		}
	}
}

func generateRandomString(length int) (string, error) {
	result := make([]byte, length)
	max := big.NewInt(int64(len(charset)))
	for i := 0; i < length; i++ {
		n, err := cryptoRand.Int(cryptoRand.Reader, max)
		if err != nil {
			return "", err
		}
		result[i] = charset[n.Int64()]
	}
	return string(result), nil
}

// MakeSeed returns a crypto-random int64 seed, falling back to time on error.
// Replaces gonc secure.MakeSeed.
func MakeSeed() int64 {
	var seed int64
	if err := binary.Read(cryptoRand.Reader, binary.BigEndian, &seed); err != nil {
		return time.Now().UnixNano()
	}
	return seed
}

// GenerateSeededRandomString returns a deterministic pseudo-random string of
// the given length derived from seed (math/rand). Replaces gonc
// secure.GenerateSeededRandomString.
func GenerateSeededRandomString(length int, seed int64) string {
	r := mrand.New(mrand.NewSource(seed))
	result := make([]byte, length)
	for i := range result {
		result[i] = charset[r.Intn(len(charset))]
	}
	return string(result)
}

// IsWeakPassword reports whether password is too weak (gonc parity):
// length < 8, in a common-weak list, or missing a letter or digit.
func IsWeakPassword(password string) bool {
	if len(password) < 8 {
		return true
	}
	lower := strings.ToLower(password)
	weakList := []string{"123456", "password", "12345678", "qwerty", "abc123", "111111", "123123"}
	for _, w := range weakList {
		if lower == w {
			return true
		}
	}
	var hasLetter, hasDigit bool
	for _, c := range password {
		if unicode.IsLetter(c) {
			hasLetter = true
		}
		if unicode.IsDigit(c) {
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return true
	}
	return false
}
