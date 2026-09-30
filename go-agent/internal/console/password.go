package console

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const hashMemory uint32 = 64 * 1024
const hashTime uint32 = 3
const hashThreads uint8 = 4
const hashSize uint32 = 32

var hashSlots = make(chan struct{}, 2)

func validPassword(password string) bool {
	if !utf8.ValidString(password) || len(password) > 512 {
		return false
	}
	n := utf8.RuneCountInString(password)
	return n >= 12 && n <= 128
}

func withHashSlot(fn func() (string, error)) (string, error) {
	select {
	case hashSlots <- struct{}{}:
		defer func() { <-hashSlots }()
		return fn()
	default:
		return "", ErrBusy
	}
}

func hashPassword(password string) (string, error) {
	return withHashSlot(func() (string, error) {
		salt := make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return "", fmt.Errorf("%w: password salt: %v", ErrUnavailable, err)
		}
		key := argon2.IDKey([]byte(password), salt, hashTime, hashMemory, hashThreads, hashSize)
		return fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=4$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
	})
}

func parseHash(encoded string) ([]byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=65536,t=3,p=4" {
		return nil, nil, ErrUnavailable
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != 16 {
		return nil, nil, ErrUnavailable
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) != 32 {
		return nil, nil, ErrUnavailable
	}
	return salt, key, nil
}

func verifyPassword(encoded, password string) (bool, error) {
	salt, want, err := parseHash(encoded)
	if err != nil {
		return false, err
	}
	matched := false
	_, err = withHashSlot(func() (string, error) {
		got := argon2.IDKey([]byte(password), salt, hashTime, hashMemory, hashThreads, hashSize)
		matched = subtle.ConstantTimeCompare(got, want) == 1
		return "", nil
	})
	if err != nil {
		return false, err
	}
	return matched, nil
}
