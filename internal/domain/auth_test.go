package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bzumhagen/idrometer/internal/domain"
)

// fakeHasher is a reversible stand-in; real hashing lives in an adapter.
type fakeHasher struct{}

func (fakeHasher) Hash(p string) (string, error) { return "hash:" + p, nil }
func (fakeHasher) Verify(p, h string) (bool, error) {
	return h == "hash:"+p, nil
}

func newAuth(t *testing.T, e *env) *domain.Auth {
	t.Helper()
	a, err := domain.NewAuth(e.store, e.clock, fakeHasher{})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestLoginFlow(t *testing.T) {
	e := newEnv(t)
	a := newAuth(t, e)

	if has, _ := a.HasPassword(e.ctx); has {
		t.Fatal("no password should be set yet")
	}
	// Nothing set yet: login is refused, not an error.
	if _, _, err := a.Login(e.ctx, "anything-at-all"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("login before setup: %v", err)
	}
	if err := a.SetPassword(e.ctx, "short"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("short password: %v", err)
	}
	if err := a.SetPassword(e.ctx, "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Login(e.ctx, "wrong password!!"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("wrong password: %v", err)
	}
	tok, exp, err := a.Login(e.ctx, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !exp.Equal(e.clock.t.Add(domain.SessionLifetime)) || len(tok) < 40 {
		t.Fatalf("token len %d, expiry %v", len(tok), exp)
	}
	if user, err := a.Authenticate(e.ctx, tok); err != nil || user != domain.AdminUser {
		t.Fatalf("authenticate: %q %v", user, err)
	}
	if _, err := a.Authenticate(e.ctx, "bogus"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("bogus token: %v", err)
	}
	if _, err := a.Authenticate(e.ctx, ""); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("empty token: %v", err)
	}
}

func TestSessionExpiresAndLogout(t *testing.T) {
	e := newEnv(t)
	a := newAuth(t, e)
	_ = a.SetPassword(e.ctx, "correct horse battery")
	tok, _, _ := a.Login(e.ctx, "correct horse battery")

	e.clock.Advance(domain.SessionLifetime + time.Second)
	if _, err := a.Authenticate(e.ctx, tok); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("expired: %v", err)
	}
	e.clock.t = e.clock.t.Add(-domain.SessionLifetime)
	tok2, _, _ := a.Login(e.ctx, "correct horse battery")
	if err := a.Logout(e.ctx, tok2); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Authenticate(e.ctx, tok2); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("after logout: %v", err)
	}
}

func TestChangingPasswordSignsEveryoneOut(t *testing.T) {
	e := newEnv(t)
	a := newAuth(t, e)
	_ = a.SetPassword(e.ctx, "correct horse battery")
	tok, _, _ := a.Login(e.ctx, "correct horse battery")
	if err := a.SetPassword(e.ctx, "another long password"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Authenticate(e.ctx, tok); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("old session survived password change: %v", err)
	}
}

func TestSessionTokensAreStoredHashed(t *testing.T) {
	if h := domain.HashToken("abc"); len(h) != 64 || strings.Contains(h, "abc") {
		t.Fatalf("hash = %q", h)
	}
}
