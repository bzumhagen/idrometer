package web

import (
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/bzumhagen/idrometer/internal/domain"
)

const (
	maxLoginFailures = 5
	loginWindow      = 15 * time.Minute
)

// maxTrackedClients bounds the limiter map before expired entries are swept.
const maxTrackedClients = 1024

// loginLimiter blocks an address after repeated failed logins. State is in
// memory only; a restart clears it. Behind a reverse proxy every visitor
// shares the proxy's address, so the limit then applies to everyone.
type loginLimiter struct {
	clock domain.Clock
	mu    sync.Mutex
	fails map[string][]time.Time
}

func newLoginLimiter(c domain.Clock) *loginLimiter {
	return &loginLimiter{clock: c, fails: map[string][]time.Time{}}
}

func (l *loginLimiter) recent(key string) []time.Time {
	cutoff := l.clock.Now().Add(-loginWindow)
	kept := l.fails[key][:0]
	for _, t := range l.fails[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.fails, key)
	} else {
		l.fails[key] = kept
	}
	return kept
}

func (l *loginLimiter) blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(key)) >= maxLoginFailures
}

func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.fails) > maxTrackedClients {
		for k := range l.fails { // a spray of addresses must not grow the map forever
			l.recent(k)
		}
	}
	l.recent(key)
	l.fails[key] = append(l.fails[key], l.clock.Now())
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}

func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type loginData struct {
	Next       string
	NeedsSetup bool
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request, p *page) (string, error) {
	has, err := s.Auth.HasPassword(r.Context())
	if err != nil {
		return "", err
	}
	p.Title, p.Nav = "Sign in", "login"
	p.Data = loginData{Next: safeNext(r.URL.Query().Get("next"), r.Host), NeedsSetup: !has}
	return "login", nil
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	key := clientKey(r)
	next := safeNext(r.FormValue("next"), r.Host)
	back := "/login?next=" + urlEscape(next)
	if s.limiter.blocked(key) {
		s.Log.Warn("login blocked", "remote", key)
		s.setFlash(w, "Too many failed attempts. Try again in a few minutes.", true)
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	token, expires, err := s.Auth.Login(r.Context(), r.FormValue("password"))
	switch {
	case errors.Is(err, domain.ErrUnauthorized):
		s.limiter.fail(key)
		s.setFlash(w, "Wrong password.", true)
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	case err != nil:
		s.serverError(w, err)
		return
	}
	s.limiter.reset(key)
	s.setSession(w, token, expires)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.Auth.Logout(r.Context(), c.Value); err != nil {
			s.Log.Error("logout", "err", err)
		}
	}
	s.clearSession(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
