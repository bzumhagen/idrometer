// Package argon2 implements domain.PasswordHasher with argon2id, storing
// hashes in the standard PHC string format.
package argon2

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Hasher uses the OWASP minimum argon2id profile (19 MiB, 2 iterations,
// 1 lane), which is gentle enough for a small LXC container.
type Hasher struct {
	MemoryKiB uint32
	Time      uint32
	Threads   uint8
}

func New() Hasher { return Hasher{MemoryKiB: 19 * 1024, Time: 2, Threads: 1} }

const keyLen = 32

var b64 = base64.RawStdEncoding

func (h Hasher) Hash(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, h.Time, h.MemoryKiB, h.Threads, keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.MemoryKiB, h.Time, h.Threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

func (Hasher) Verify(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("argon2: unsupported hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("argon2: unsupported version")
	}
	var mem, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &p); err != nil {
		return false, errors.New("argon2: bad parameters")
	}
	// Refuse absurd parameters from a tampered database.
	if mem == 0 || mem > 1<<20 || t == 0 || t > 20 || p == 0 {
		return false, errors.New("argon2: parameters out of range")
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, errors.New("argon2: bad salt")
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, errors.New("argon2: bad hash")
	}
	got := argon2.IDKey([]byte(password), salt, t, mem, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
