// Package ingest exposes the device-facing HTTP endpoint. Each hydrometer
// type registers a Decoder; the handler authenticates the request, decodes it
// and hands a domain.ReadingInput to the domain.
package ingest

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/bzumhagen/idrometer/internal/domain"
)

// MaxBodyBytes caps ingest request bodies.
const MaxBodyBytes = 8 << 10

// Decoder turns one source's request body into a reading plus the token the
// device presented in the payload ("" if it has none).
type Decoder interface {
	Source() string
	Decode(body []byte) (in domain.ReadingInput, token string, err error)
}

// Ingestor is what the handler needs from the domain.
type Ingestor interface {
	Ingest(ctx context.Context, in domain.ReadingInput) (domain.Reading, error)
}

// Handler serves POST /api/v1/ingest/{source}.
type Handler struct {
	ingestor Ingestor
	decoders map[string]Decoder
	token    []byte
	log      *slog.Logger
}

func NewHandler(ing Ingestor, token string, log *slog.Logger, decoders ...Decoder) *Handler {
	if log == nil {
		log = slog.Default()
	}
	h := &Handler{ingestor: ing, decoders: map[string]Decoder{}, token: []byte(token), log: log}
	for _, d := range decoders {
		h.decoders[d.Source()] = d
	}
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	dec, ok := h.decoders[r.PathValue("source")]
	if !ok {
		http.Error(w, "unknown source", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}
	reject := func() {
		h.log.Warn("ingest rejected: bad token", "remote", r.RemoteAddr, "source", dec.Source())
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
	// With a bearer token, authenticate before decoding so unauthenticated
	// callers never see parse errors.
	bearer, hasBearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if hasBearer && !h.tokenOK(bearer) {
		reject()
		return
	}
	in, payloadToken, err := dec.Decode(body)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if !hasBearer && !h.tokenOK(payloadToken) {
		reject()
		return
	}
	if _, err := h.ingestor.Ingest(r.Context(), in); err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"status":"ok"}`)
}

func (h *Handler) tokenOK(presented string) bool {
	return len(h.token) > 0 && subtle.ConstantTimeCompare([]byte(presented), h.token) == 1
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, domain.ErrInvalid) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.log.Error("ingest failed", "err", err, "remote", r.RemoteAddr)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
