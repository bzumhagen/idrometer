// Package web serves the HTML UI. Every page can be viewed without logging
// in; anything that changes data requires the admin session.
package web

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bzumhagen/idrometer/internal/domain"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

// Subscriber supplies live events (satisfied by *events.Broker).
type Subscriber interface {
	Subscribe() (<-chan domain.Event, func())
}

// Services are the domain services the UI uses.
type Services struct {
	Queries *domain.Queries
	Editor  *domain.Editor
	Auth    *domain.Auth
	Prefs   *domain.PreferenceService
	Events  Subscriber
	Clock   domain.Clock
	Log     *slog.Logger
	// SecureCookies sets the Secure flag on cookies (when served over HTTPS).
	SecureCookies bool
	// IngestToken is shown to the signed-in admin on the Settings page, with IngestListen (the
	// configured listen address) so they can fill in the iSpindel. Both may be empty.
	IngestToken  string
	IngestListen string
	// Done is closed on server shutdown so long-lived streams can end.
	Done <-chan struct{}
}

type Server struct {
	Services
	pages   map[string]*template.Template
	limiter *loginLimiter
	assets  map[string]string // static file -> fingerprinted URL
}

const (
	sessionCookie = "idrometer_session"
	flashCookie   = "idrometer_flash"
)

func New(s Services) (*Server, error) {
	if s.Log == nil {
		s.Log = slog.Default()
	}
	if s.Clock == nil {
		s.Clock = domain.SystemClock{}
	}
	srv := &Server{Services: s, pages: map[string]*template.Template{}, limiter: newLoginLimiter(s.Clock), assets: fingerprintAssets()}
	funcs := srv.funcs()
	for _, name := range []string{"dashboard", "fermentations", "fermentation", "devices", "settings", "login",
		"recipes", "recipe", "compare"} {
		t, err := template.New("").Funcs(funcs).ParseFS(templatesFS,
			"templates/layout.html", "templates/_partials.html", "templates/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", name, err)
		}
		srv.pages[name] = t
	}
	return srv, nil
}

// Mount registers all UI routes.
func (s *Server) Mount(mux *http.ServeMux) {
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServerFS(static))))

	mux.HandleFunc("GET /{$}", s.view(s.dashboard))
	mux.HandleFunc("GET /partials/devices", s.view(s.devicesPartial))
	mux.HandleFunc("GET /fermentations", s.view(s.fermentationList))
	mux.HandleFunc("GET /fermentations/{id}", s.view(s.fermentationPage))
	mux.HandleFunc("GET /devices", s.view(s.devicesPage))
	mux.HandleFunc("GET /settings", s.view(s.settingsPage))
	mux.HandleFunc("GET /recipes", s.view(s.recipeList))
	mux.HandleFunc("GET /recipes/{id}", s.view(s.recipePage))
	mux.HandleFunc("GET /compare", s.view(s.comparePage))
	mux.HandleFunc("GET /login", s.view(s.loginPage))
	mux.HandleFunc("GET /stream", s.streamDashboard)
	mux.HandleFunc("GET /fermentations/{id}/stream", s.streamFermentation)

	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.logout)

	edit := func(pattern string, h http.HandlerFunc) { mux.HandleFunc("POST "+pattern, s.admin(h)) }
	edit("/fermentations/{id}/update", s.updateFermentation)
	edit("/fermentations/{id}/close", s.closeFermentation)
	edit("/fermentations/{id}/reopen", s.reopenFermentation)
	edit("/fermentations/{id}/delete", s.deleteFermentation)
	edit("/fermentations/{id}/restore", s.restoreFermentation)
	edit("/fermentations/{id}/split", s.splitFermentation)
	edit("/fermentations/{id}/merge", s.mergeFermentation)
	edit("/fermentations/{id}/exclude", s.excludeReadings)
	edit("/fermentations/{id}/manual", s.addManualReading)
	edit("/fermentations/{id}/annotations", s.addAnnotation)
	edit("/manual/{id}/delete", s.deleteManualReading)
	edit("/annotations/{id}/delete", s.deleteAnnotation)
	edit("/annotation-types", s.addAnnotationType)
	edit("/annotation-types/{id}", s.renameAnnotationType)
	edit("/annotation-types/{id}/delete", s.deleteAnnotationType)
	edit("/recipes", s.createRecipe)
	edit("/recipes/{id}", s.updateRecipe)
	edit("/recipes/{id}/delete", s.deleteRecipe)
	edit("/devices/{id}/calibration", s.setCalibration)
	edit("/devices/{id}/calibration/clear", s.clearCalibration)
	edit("/devices/{id}", s.updateDevice)
	edit("/settings", s.updateSettings)
}

func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable") // URLs are fingerprinted
		next.ServeHTTP(w, r)
	})
}

// ---- request context & auth ----

type ctxKey struct{}

// isAdmin reports whether the request carries a valid admin session.
func (s *Server) isAdmin(r *http.Request) bool {
	if v, ok := r.Context().Value(ctxKey{}).(bool); ok {
		return v
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	_, err = s.Auth.Authenticate(r.Context(), c.Value)
	if err != nil && !errors.Is(err, domain.ErrUnauthorized) {
		s.Log.Error("authenticate", "err", err)
	}
	return err == nil
}

// admin wraps a state-changing handler: anonymous visitors are sent to log in.
func (s *Server) admin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isAdmin(r) {
			s.setFlash(w, "Sign in to make changes.", true)
			http.Redirect(w, r, "/login?next="+url.QueryEscape(safeNext(r.Header.Get("Referer"), r.Host)), http.StatusSeeOther)
			return
		}
		h(w, r)
	}
}

// safeNext reduces a Referer or ?next= value to a same-site path, to avoid
// open redirects.
func safeNext(raw, host string) string {
	if raw == "" {
		return "/"
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Host != "" && u.Host != host) {
		return "/"
	}
	p := u.EscapedPath()
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.Contains(p, "\\") {
		return "/"
	}
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return p
}

func (s *Server) setSession(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode,
	})
}

// ---- flash messages (one-shot, shown on the next page) ----

func (s *Server) setFlash(w http.ResponseWriter, msg string, isErr bool) {
	kind := "ok"
	if isErr {
		kind = "err"
	}
	http.SetCookie(w, &http.Cookie{
		Name: flashCookie, Value: url.QueryEscape(kind + ":" + msg), Path: "/", MaxAge: 60,
		HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode,
	})
}

type flash struct {
	Text  string
	Error bool
}

func (s *Server) takeFlash(w http.ResponseWriter, r *http.Request) *flash {
	c, err := r.Cookie(flashCookie)
	if err != nil {
		return nil
	}
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: "/", MaxAge: -1})
	v, err := url.QueryUnescape(c.Value)
	if err != nil {
		return nil
	}
	kind, text, ok := strings.Cut(v, ":")
	if !ok {
		return nil
	}
	return &flash{Text: text, Error: kind == "err"}
}

// ---- rendering ----

// page is the data every template receives.
type page struct {
	Title string
	Nav   string
	Admin bool
	Prefs domain.Preferences
	Flash *flash
	Data  any
}

type viewFunc func(w http.ResponseWriter, r *http.Request, p *page) (name string, err error)

// view builds the common page data, runs the handler and renders its template.
func (s *Server) view(fn viewFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		admin := s.isAdmin(r)
		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, admin))
		prefs, err := s.Queries.Preferences(r.Context())
		if err != nil {
			s.serverError(w, err)
			return
		}
		p := &page{Admin: admin, Prefs: prefs, Flash: s.takeFlash(w, r)}
		name, err := fn(w, r, p)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if name == "" {
			return // handler wrote the response itself
		}
		s.render(w, name, p)
	}
}

func (s *Server) render(w http.ResponseWriter, name string, p *page) {
	t, ok := s.pages[name]
	if !ok {
		s.serverError(w, fmt.Errorf("no template %q", name))
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", p); err != nil {
		s.serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = buf.WriteTo(w)
}

func (s *Server) serverError(w http.ResponseWriter, err error) {
	s.Log.Error("web error", "err", err)
	http.Error(w, "Something went wrong.", http.StatusInternalServerError)
}

// fail turns a domain error into a response. Expected problems (bad input,
// conflicts) go back to the user as a flash message on the previous page.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		http.Error(w, "Not found.", http.StatusNotFound)
	case errors.Is(err, domain.ErrInvalid), errors.Is(err, domain.ErrConflict):
		if r.Method == http.MethodPost {
			s.setFlash(w, cleanMessage(err), true)
			http.Redirect(w, r, backURL(r), http.StatusSeeOther)
			return
		}
		http.Error(w, cleanMessage(err), http.StatusBadRequest)
	default:
		s.serverError(w, err)
	}
}

// cleanMessage drops the sentinel prefix ("invalid input: ...").
func cleanMessage(err error) string {
	msg := err.Error()
	for _, p := range []string{domain.ErrInvalid.Error() + ": ", domain.ErrConflict.Error() + ": "} {
		msg = strings.TrimPrefix(msg, p)
	}
	return msg
}

// done redirects back after a successful POST with a confirmation flash.
func (s *Server) done(w http.ResponseWriter, r *http.Request, msg string) {
	s.setFlash(w, msg, false)
	http.Redirect(w, r, backURL(r), http.StatusSeeOther)
}

// backURL is where a POST returns to: the referring page, plus an anchor when
// the form named one in return_to (so the user lands back where they were).
func backURL(r *http.Request) string {
	u := safeNext(r.Header.Get("Referer"), r.Host)
	switch r.FormValue("return_to") {
	case "chart", "log":
		return u + "#" + r.FormValue("return_to")
	}
	return u
}

func urlEscape(s string) string { return url.QueryEscape(s) }
