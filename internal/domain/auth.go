package domain

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// AdminUser is the single account. Viewing needs no login; editing does.
const AdminUser = "admin"

const (
	SessionLifetime = 30 * 24 * time.Hour
	MinPasswordLen  = 10
)

type User struct {
	Username     string
	PasswordHash string
	CreatedAt    time.Time
}

// Session is stored by token hash; the raw token only lives in the cookie.
type Session struct {
	TokenHash string
	Username  string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Auth handles the admin password and login sessions.
type Auth struct {
	tx     TxManager
	clock  Clock
	hasher PasswordHasher
	// dummyHash is verified against when the user doesn't exist, so a missing
	// user costs the same as a wrong password.
	dummyHash string
}

func NewAuth(tx TxManager, clock Clock, hasher PasswordHasher) (*Auth, error) {
	d, err := hasher.Hash("not-a-real-password")
	if err != nil {
		return nil, err
	}
	return &Auth{tx: tx, clock: clock, hasher: hasher, dummyHash: d}, nil
}

// SetPassword creates or replaces the admin password and signs out every
// existing session.
func (a *Auth) SetPassword(ctx context.Context, password string) error {
	if len(password) < MinPasswordLen {
		return fmt.Errorf("%w: password must be at least %d characters", ErrInvalid, MinPasswordLen)
	}
	h, err := a.hasher.Hash(password)
	if err != nil {
		return err
	}
	return a.tx.WithinTx(ctx, func(r Repos) error {
		if err := r.Users.Upsert(ctx, User{Username: AdminUser, PasswordHash: h, CreatedAt: a.clock.Now()}); err != nil {
			return err
		}
		return r.Sessions.DeleteForUser(ctx, AdminUser)
	})
}

// HasPassword reports whether an admin password has been set.
func (a *Auth) HasPassword(ctx context.Context) (bool, error) {
	err := a.tx.View(ctx, func(r Repos) error { _, err := r.Users.Get(ctx, AdminUser); return err })
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// Login verifies the password and returns a new session token and its expiry.
// Wrong passwords and an unset password both return ErrUnauthorized.
func (a *Auth) Login(ctx context.Context, password string) (token string, expires time.Time, err error) {
	var hash string
	err = a.tx.View(ctx, func(r Repos) error {
		u, err := r.Users.Get(ctx, AdminUser)
		if errors.Is(err, ErrNotFound) {
			hash = a.dummyHash
			return nil
		}
		hash = u.PasswordHash
		return err
	})
	if err != nil {
		return "", time.Time{}, err
	}
	ok, err := a.hasher.Verify(password, hash)
	if err != nil {
		return "", time.Time{}, err
	}
	if !ok || hash == a.dummyHash {
		return "", time.Time{}, ErrUnauthorized
	}
	token, err = newToken()
	if err != nil {
		return "", time.Time{}, err
	}
	now := a.clock.Now()
	expires = now.Add(SessionLifetime)
	err = a.tx.WithinTx(ctx, func(r Repos) error {
		_ = r.Sessions.DeleteExpired(ctx, now)
		return r.Sessions.Create(ctx, Session{
			TokenHash: HashToken(token), Username: AdminUser, CreatedAt: now, ExpiresAt: expires,
		})
	})
	return token, expires, err
}

// Authenticate resolves a cookie token to its user. Unknown or expired tokens
// return ErrUnauthorized.
func (a *Auth) Authenticate(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", ErrUnauthorized
	}
	var s Session
	err := a.tx.View(ctx, func(r Repos) (err error) { s, err = r.Sessions.Get(ctx, HashToken(token)); return })
	if errors.Is(err, ErrNotFound) {
		return "", ErrUnauthorized
	}
	if err != nil {
		return "", err
	}
	if !s.ExpiresAt.After(a.clock.Now()) {
		return "", ErrUnauthorized
	}
	return s.Username, nil
}

// Logout invalidates a session token. Unknown tokens are ignored.
func (a *Auth) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return a.tx.WithinTx(ctx, func(r Repos) error { return r.Sessions.Delete(ctx, HashToken(token)) })
}

// HashToken is how session tokens are stored.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
