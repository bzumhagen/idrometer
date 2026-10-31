package argon2_test

import (
	"strings"
	"testing"

	"github.com/bzumhagen/idrometer/internal/crypto/argon2"
)

func TestHashAndVerify(t *testing.T) {
	h := argon2.Hasher{MemoryKiB: 64, Time: 1, Threads: 1} // cheap for tests
	enc, err := h.Hash("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=64,t=1,p=1$") || strings.Contains(enc, "correct") {
		t.Fatalf("encoded = %q", enc)
	}
	if ok, err := h.Verify("correct horse", enc); !ok || err != nil {
		t.Fatalf("verify good: %v %v", ok, err)
	}
	if ok, err := h.Verify("wrong horse", enc); ok || err != nil {
		t.Fatalf("verify bad: %v %v", ok, err)
	}
	other, _ := h.Hash("correct horse")
	if other == enc {
		t.Fatal("salts must differ between hashes")
	}
}

func TestVerifyRejectsMalformedAndHostileHashes(t *testing.T) {
	h := argon2.New()
	for _, enc := range []string{
		"", "plain", "$argon2id$v=19$m=64,t=1,p=1$AAAA", "$bcrypt$v=19$m=64,t=1,p=1$AAAA$AAAA",
		"$argon2id$v=19$m=4294967295,t=1,p=1$AAAA$AAAA", // would exhaust memory
		"$argon2id$v=19$m=64,t=999,p=1$AAAA$AAAA",
	} {
		if ok, err := h.Verify("x", enc); ok || err == nil {
			t.Errorf("Verify(%q) = %v, %v; want error", enc, ok, err)
		}
	}
}
