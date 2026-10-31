package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/bzumhagen/idrometer/internal/domain"
)

const heartbeat = 20 * time.Second

// serveStream pushes Server-Sent Events until the client leaves or the server
// shuts down. pick maps a domain event to an SSE event name and JSON payload;
// it returns ok=false to skip the event.
func (s *Server) serveStream(w http.ResponseWriter, r *http.Request, pick func(domain.Event) (name string, payload any, ok bool)) {
	rc := http.NewResponseController(w)
	// Streams are long-lived: clear the server-wide deadlines. A lingering read
	// deadline would cancel the request context mid-stream (the request has no body).
	_ = rc.SetWriteDeadline(time.Time{})
	_ = rc.SetReadDeadline(time.Time{})
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no") // keep nginx-style proxies from buffering
	w.WriteHeader(http.StatusOK)

	events, cancel := s.Events.Subscribe()
	defer cancel()
	// Tell the client we're live; EventSource reconnects on its own after drops.
	fmt.Fprint(w, "retry: 3000\n: connected\n\n")
	if rc.Flush() != nil {
		return
	}
	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.Done:
			return
		case <-tick.C:
			fmt.Fprint(w, ": ping\n\n")
		case e, open := <-events:
			if !open {
				return // subscriber lagged; the client reconnects and reloads
			}
			name, payload, ok := pick(e)
			if !ok {
				continue
			}
			b, err := json.Marshal(payload)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
		}
		if rc.Flush() != nil {
			return
		}
	}
}

func (s *Server) streamDashboard(w http.ResponseWriter, r *http.Request) {
	s.serveStream(w, r, func(e domain.Event) (string, any, bool) { return "changed", struct{}{}, true })
}

func (s *Server) streamFermentation(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	prefs, err := s.Queries.Preferences(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.serveStream(w, r, func(e domain.Event) (string, any, bool) {
		if e.Type == domain.EventSettings {
			return "changed", struct{}{}, true // units changed; the page reloads
		}
		if e.FermentationID != id {
			return "", nil, false
		}
		if e.Type == domain.EventReading && e.Reading != nil {
			return "reading", domain.PointOf(*e.Reading, prefs), true
		}
		return "changed", struct{}{}, true
	})
}
