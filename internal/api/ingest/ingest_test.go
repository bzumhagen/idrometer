package ingest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bzumhagen/idrometer/internal/api/ingest"
	"github.com/bzumhagen/idrometer/internal/api/ingest/ispindel"
	"github.com/bzumhagen/idrometer/internal/domain"
)

type fakeIngestor struct {
	got []domain.ReadingInput
	err error
}

func (f *fakeIngestor) Ingest(_ context.Context, in domain.ReadingInput) (domain.Reading, error) {
	f.got = append(f.got, in)
	return domain.Reading{}, f.err
}

const token = "0123456789abcdef"

func serve(t *testing.T, ing *fakeIngestor, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/ingest/{source}", ingest.NewHandler(ing, token, nil, ispindel.Decoder{}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func post(path, body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
}

func TestAccepted(t *testing.T) {
	ing := &fakeIngestor{}
	rec := serve(t, ing, post("/api/v1/ingest/ispindel", `{"ID":7,"token":"`+token+`","temperature":20}`))
	if rec.Code != 200 || len(ing.got) != 1 || ing.got[0].HardwareID != "7" {
		t.Fatalf("code=%d got=%+v", rec.Code, ing.got)
	}
}

func TestBearerTokenAccepted(t *testing.T) {
	ing := &fakeIngestor{}
	req := post("/api/v1/ingest/ispindel", `{"ID":7,"temperature":20}`)
	req.Header.Set("Authorization", "Bearer "+token)
	if rec := serve(t, ing, req); rec.Code != 200 {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestRejections(t *testing.T) {
	cases := []struct {
		name string
		req  *http.Request
		want int
	}{
		{"no token", post("/api/v1/ingest/ispindel", `{"ID":7}`), 401},
		{"wrong token", post("/api/v1/ingest/ispindel", `{"ID":7,"token":"nope"}`), 401},
		{"unknown source", post("/api/v1/ingest/tilt", `{}`), 404},
		{"bad json", post("/api/v1/ingest/ispindel", `{`), 400},
		{"missing id", post("/api/v1/ingest/ispindel", `{"token":"`+token+`"}`), 400},
		{"too large", post("/api/v1/ingest/ispindel", `{"ID":1,"pad":"`+strings.Repeat("x", ingest.MaxBodyBytes)+`"}`), 413},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ing := &fakeIngestor{}
			if rec := serve(t, ing, c.req); rec.Code != c.want {
				t.Fatalf("code = %d, want %d (%s)", rec.Code, c.want, rec.Body)
			}
			if len(ing.got) != 0 {
				t.Fatal("rejected request reached the domain")
			}
		})
	}
}

func TestDomainErrorsMapToStatus(t *testing.T) {
	body := `{"ID":7,"token":"` + token + `"}`
	if rec := serve(t, &fakeIngestor{err: domain.ErrInvalid}, post("/api/v1/ingest/ispindel", body)); rec.Code != 400 {
		t.Fatalf("invalid: %d", rec.Code)
	}
	if rec := serve(t, &fakeIngestor{err: context.DeadlineExceeded}, post("/api/v1/ingest/ispindel", body)); rec.Code != 500 {
		t.Fatalf("internal: %d", rec.Code)
	}
}

func TestBadBearerIsRejectedBeforeDecoding(t *testing.T) {
	req := post("/api/v1/ingest/ispindel", `{not json`)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := serve(t, &fakeIngestor{}, req)
	if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "JSON") {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
}
