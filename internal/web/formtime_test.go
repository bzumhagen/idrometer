package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestFormTime(t *testing.T) {
	post := func(v url.Values) *http.Request {
		r := httptest.NewRequest("POST", "/", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}
	for _, c := range []struct {
		name string
		form url.Values
		want string
	}{
		{"per-field offset wins (CEST, -120)", url.Values{"at": {"2030-07-01T12:00"}, "at_tz": {"-120"}, "tz": {"-60"}}, "2030-07-01T10:00:00Z"},
		{"falls back to form tz (CET, -60)", url.Values{"at": {"2030-01-01T12:00"}, "tz": {"-60"}}, "2030-01-01T11:00:00Z"},
		{"no JS: UTC", url.Values{"at": {"2030-01-01T12:00:30"}}, "2030-01-01T12:00:30Z"},
	} {
		got, err := formTime(post(c.form), "at")
		if err != nil || got.Format(time.RFC3339) != c.want {
			t.Errorf("%s: got %v, %v; want %s", c.name, got, err, c.want)
		}
	}
	if _, err := formTime(post(url.Values{"at": {"2030-01-01T12:00"}, "at_tz": {"9999"}}), "at"); err == nil {
		t.Error("absurd offset should be rejected")
	}
	if _, err := formTime(post(url.Values{"at": {"nope"}}), "at"); err == nil {
		t.Error("bad time should be rejected")
	}
}
