// Package api assembles the HTTP surface: device ingest, health, and (via
// Mount) the JSON API and web UI.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// Deps are the pieces the router needs.
type Deps struct {
	Ingest http.Handler                // POST /api/v1/ingest/{source}
	Health func(context.Context) error // nil error = healthy
	Log    *slog.Logger
	// Mount registers additional routes (web UI, JSON API).
	Mount []func(*http.ServeMux)
}

// NewRouter returns the root handler with recovery, logging and security
// headers applied.
func NewRouter(d Deps) http.Handler {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/ingest/{source}", d.Ingest)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if d.Health != nil {
			if err := d.Health(ctx); err != nil {
				log.Error("health check failed", "err", err)
				http.Error(w, "unhealthy", http.StatusServiceUnavailable)
				return
			}
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	for _, m := range d.Mount {
		m(mux)
	}
	// Browsers' cross-site POSTs are refused (CSRF). Device requests carry no
	// Origin or Sec-Fetch-Site header and pass through.
	csrf := http.NewCrossOriginProtection()
	return recoverer(log, logRequests(log, securityHeaders(csrf.Handler(mux))))
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }

// Unwrap lets http.ResponseController reach the real writer (flush, deadlines).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		level := slog.LevelInfo
		if r.URL.Path == "/healthz" {
			level = slog.LevelDebug
		}
		log.Log(r.Context(), level, "request", "method", r.Method, "path", r.URL.Path,
			"status", sw.status, "dur", time.Since(start).Round(time.Millisecond))
	})
}

func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil && !isAbort(v) {
				log.Error("panic in handler", "panic", v, "path", r.URL.Path)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; "+
				"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

// isAbort reports whether a recovered value is net/http's deliberate abort signal.
func isAbort(v any) bool {
	err, ok := v.(error)
	return ok && errors.Is(err, http.ErrAbortHandler)
}
