package web_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bzumhagen/idrometer/internal/api"
	"github.com/bzumhagen/idrometer/internal/domain"
	"github.com/bzumhagen/idrometer/internal/events"
	"github.com/bzumhagen/idrometer/internal/store/memory"
	"github.com/bzumhagen/idrometer/internal/web"
)

type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }

type plainHasher struct{}

func (plainHasher) Hash(p string) (string, error)    { return "h:" + p, nil }
func (plainHasher) Verify(p, h string) (bool, error) { return h == "h:"+p, nil }

const password = "correct horse battery"

type app struct {
	t      *testing.T
	srv    *httptest.Server
	client *http.Client
	store  *memory.Store
	clock  *clock
	ing    *domain.Ingestor
	broker *events.Broker
	done   chan struct{}
}

// epoch is today at 12:00 UTC. Session cookies expire on the fake clock but the
// test cookie jar checks real time, so the fake clock must track the real date.
var epoch = time.Now().UTC().Truncate(24 * time.Hour).Add(12 * time.Hour)

// at is a datetime-local form value h hours and m minutes after epoch.
func at(h, m int) string {
	return epoch.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute).Format("2006-01-02T15:04:05")
}

func newApp(t *testing.T) *app {
	t.Helper()
	store := memory.New()
	clk := &clock{epoch}
	broker := events.NewBroker()
	auth, err := domain.NewAuth(store, clk, plainHasher{})
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.SetPassword(context.Background(), password); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	q := domain.NewQueries(store, clk)
	ui, err := web.New(web.Services{
		Queries: q, Editor: domain.NewEditor(store, clk, broker), Auth: auth,
		Prefs: domain.NewPreferenceService(store, broker), Events: broker, Clock: clk, Done: done,
		IngestToken: "test-ingest-token-123", IngestListen: ":8080",
	})
	if err != nil {
		t.Fatal(err)
	}
	h := api.NewRouter(api.Deps{
		Ingest: http.NotFoundHandler(),
		Mount:  []func(*http.ServeMux){api.MountFermentations(q, nil), ui.Mount},
	})
	srv := httptest.NewUnstartedServer(h)
	// Mirrors the production ReadTimeout; streams must outlive it.
	srv.Config.ReadTimeout = 200 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &app{
		t: t, srv: srv, store: store, clock: clk, broker: broker, done: done,
		ing:    domain.NewIngestor(store, clk, broker),
		client: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

func (a *app) readings(hw string, n int) int64 {
	a.t.Helper()
	var id int64
	for i := 0; i < n; i++ {
		r, err := a.ing.Ingest(context.Background(), domain.ReadingInput{
			Source: "ispindel", HardwareID: hw, Name: "Spindle " + hw, TemperatureC: 20, Gravity: 1.050 - float64(i)*0.001,
			Angle: 30, BatteryV: 4, RSSI: -60,
		})
		if err != nil {
			a.t.Fatal(err)
		}
		id = r.FermentationID
		a.clock.t = a.clock.t.Add(15 * time.Minute)
	}
	return id
}

func (a *app) do(method, path string, form url.Values) (int, string, http.Header) {
	a.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, a.srv.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

// doRef is do with a Referer header, as a browser form post would send.
func (a *app) doRef(method, path string, form url.Values, referer string) (int, string, http.Header) {
	a.t.Helper()
	req, _ := http.NewRequest(method, a.srv.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", referer)
	resp, err := a.client.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func (a *app) login() {
	a.t.Helper()
	code, _, _ := a.do("POST", "/login", url.Values{"password": {password}, "next": {"/"}})
	if code != http.StatusSeeOther {
		a.t.Fatalf("login status %d", code)
	}
}

func contains(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("response missing %q", w)
		}
	}
}

func fid(id int64) string { return strconv.FormatInt(id, 10) }

func TestPagesRenderForAnonymousVisitors(t *testing.T) {
	a := newApp(t)
	id := a.readings("A1", 4)
	for _, c := range []struct {
		path string
		want []string
	}{
		{"/", []string{"Spindle A1", "1.047 SG", "active", "Sign in"}},
		{"/partials/devices", []string{"Spindle A1", "1.047 SG"}},
		{"/fermentations", []string{"Spindle A1 ·", "/fermentations/" + fid(id)}},
		{"/fermentations?status=deleted", []string{"Nothing here yet"}},
		{"/fermentations/" + fid(id), []string{"Original gravity", "Attenuation", "/export.csv", `id="chart"`, "Download CSV"}},
		{"/devices", []string{"Spindle A1", `id="dev-1"`, "Using the device"}},
		{"/settings", []string{"Specific gravity", "New fermentation after"}},
		{"/login", []string{"Password"}},
		{"/static/app.css", []string{"--accent"}},
		{"/static/chart.js", []string{"uPlot"}},
		{"/healthz", nil},
	} {
		code, body, _ := a.do("GET", c.path, nil)
		if c.path == "/healthz" {
			continue // healthz is registered by the full server wiring, not here
		}
		if code != 200 {
			t.Errorf("%s: status %d", c.path, code)
			continue
		}
		contains(t, body, c.want...)
	}
	// Signed-out pages show values as text, never disabled or hidden forms.
	for _, path := range []string{"/devices", "/settings"} {
		_, body, _ := a.do("GET", path, nil)
		for _, bad := range []string{"<form", "disabled", "Sign in to"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s (signed out) contains %q", path, bad)
			}
		}
	}
	// Anonymous pages must not offer edit controls.
	_, body, _ := a.do("GET", "/fermentations/"+fid(id), nil)
	for _, bad := range []string{`action="/fermentations/` + fid(id) + `/close"`, "Delete"} {
		if strings.Contains(body, bad) {
			t.Errorf("anonymous page contains edit control %q", bad)
		}
	}
}

func TestEmptyDashboardExplainsSetup(t *testing.T) {
	a := newApp(t)
	_, body, _ := a.do("GET", "/", nil)
	contains(t, body, "No devices yet", "/api/v1/ingest/ispindel")
}

func TestNotFound(t *testing.T) {
	a := newApp(t)
	for _, p := range []string{"/fermentations/999", "/fermentations/abc", "/fermentations/0"} {
		if code, _, _ := a.do("GET", p, nil); code != 404 {
			t.Errorf("%s: %d", p, code)
		}
	}
}

func TestEditsRequireLogin(t *testing.T) {
	a := newApp(t)
	id := a.readings("A1", 2)
	for _, p := range []string{"close", "reopen", "delete", "restore", "update", "split", "merge", "exclude"} {
		code, _, h := a.do("POST", "/fermentations/"+fid(id)+"/"+p, url.Values{})
		if code != http.StatusSeeOther || !strings.HasPrefix(h.Get("Location"), "/login") {
			t.Errorf("%s: status %d location %q", p, code, h.Get("Location"))
		}
	}
	for _, p := range []string{"/settings", "/devices/1"} {
		if code, _, h := a.do("POST", p, url.Values{}); code != http.StatusSeeOther || !strings.HasPrefix(h.Get("Location"), "/login") {
			t.Errorf("%s: %d %q", p, code, h.Get("Location"))
		}
	}
	// Nothing changed.
	q := domain.NewQueries(a.store, a.clock)
	d, _ := q.Fermentation(context.Background(), id)
	if d.Fermentation.Status != domain.StatusActive {
		t.Fatal("anonymous POST changed data")
	}
}

func TestLoginLogout(t *testing.T) {
	a := newApp(t)
	code, _, h := a.do("POST", "/login", url.Values{"password": {"nope-nope-nope"}, "next": {"/devices"}})
	if code != 303 || !strings.HasPrefix(h.Get("Location"), "/login") {
		t.Fatalf("bad login: %d %q", code, h.Get("Location"))
	}
	_, body, _ := a.do("GET", "/login", nil)
	contains(t, body, "Wrong password")

	code, _, h = a.do("POST", "/login", url.Values{"password": {password}, "next": {"/devices"}})
	if code != 303 || h.Get("Location") != "/devices" {
		t.Fatalf("good login: %d %q", code, h.Get("Location"))
	}
	var sess *http.Cookie
	for _, c := range h.Values("Set-Cookie") {
		if strings.HasPrefix(c, "idrometer_session=") {
			r := http.Response{Header: http.Header{"Set-Cookie": {c}}}
			sess = r.Cookies()[0]
		}
	}
	if sess == nil || !sess.HttpOnly || sess.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %+v", sess)
	}
	_, body, _ = a.do("GET", "/devices", nil)
	contains(t, body, "Sign out")

	a.do("POST", "/logout", url.Values{})
	_, body, _ = a.do("GET", "/devices", nil)
	contains(t, body, "Sign in")
}

func TestLoginRedirectCannotLeaveTheSite(t *testing.T) {
	a := newApp(t)
	for _, next := range []string{"https://evil.example/x", "//evil.example", "/\\evil.example", "javascript:alert(1)"} {
		code, _, h := a.do("POST", "/login", url.Values{"password": {password}, "next": {next}})
		loc := h.Get("Location")
		// Must stay on this site: a single-slash path, never //host, /\host or a scheme.
		if code != 303 || !strings.HasPrefix(loc, "/") || strings.HasPrefix(loc, "//") || strings.HasPrefix(loc, "/\\") || strings.Contains(loc, "://") {
			t.Errorf("next=%q -> %d %q", next, code, loc)
		}
	}
}

func TestLoginRateLimit(t *testing.T) {
	a := newApp(t)
	for i := 0; i < 5; i++ {
		a.do("POST", "/login", url.Values{"password": {"wrong-password-" + strconv.Itoa(i)}})
	}
	// Even the right password is refused now.
	code, _, h := a.do("POST", "/login", url.Values{"password": {password}})
	if code != 303 || !strings.HasPrefix(h.Get("Location"), "/login") {
		t.Fatalf("blocked login: %d %q", code, h.Get("Location"))
	}
	_, body, _ := a.do("GET", "/login", nil)
	contains(t, body, "Too many failed attempts")
	// After the window passes it works again.
	a.clock.t = a.clock.t.Add(16 * time.Minute)
	if code, _, h := a.do("POST", "/login", url.Values{"password": {password}, "next": {"/"}}); code != 303 || h.Get("Location") != "/" {
		t.Fatalf("after window: %d %q", code, h.Get("Location"))
	}
}

func TestAdminWorkflow(t *testing.T) {
	a := newApp(t)
	id := a.readings("A1", 8)
	a.login()
	p := "/fermentations/" + fid(id)

	// Edit controls are shown to the admin.
	_, body, _ := a.do("GET", p, nil)
	contains(t, body, "Complete fermentation", "Exclude readings", "Split…", `name="tz"`)

	// Rename + notes + OG override.
	code, _, _ := a.do("POST", p+"/update", url.Values{"name": {"Hefeweizen"}, "notes": {"pitched warm"}, "og": {"1.052"}})
	if code != 303 {
		t.Fatalf("update: %d", code)
	}
	_, body, _ = a.do("GET", p, nil)
	contains(t, body, "Hefeweizen", "pitched warm", "1.052", "Saved.")

	// Invalid OG comes back as a flash, not a 500.
	if code, _, _ := a.do("POST", p+"/update", url.Values{"name": {"x"}, "og": {"banana"}}); code != 303 {
		t.Fatalf("bad og: %d", code)
	}
	_, body, _ = a.do("GET", p, nil)
	contains(t, body, "original gravity must be")

	// Exclude a range (UTC, no tz field).
	code, _, _ = a.do("POST", p+"/exclude", url.Values{"from": {at(0, 15)}, "to": {at(0, 30)}, "mode": {"exclude"}})
	if code != 303 {
		t.Fatalf("exclude: %d", code)
	}
	_, body, _ = a.do("GET", p, nil)
	contains(t, body, "Excluded 2 reading", "Show 2 excluded")
	_, series, _ := a.do("GET", "/api/v1/fermentations/"+fid(id)+"/series", nil)
	if strings.Count(series, ",") < 5 { // 6 visible points
		t.Errorf("series = %s", series)
	}

	// Split at 13:00 UTC, expressed in a UTC+2 browser (offset -120 min).
	code, _, h := a.do("POST", p+"/split", url.Values{"at": {at(3, 0)}, "tz": {"-120"}})
	if code != 303 || !strings.HasPrefix(h.Get("Location"), "/fermentations/") || h.Get("Location") == p {
		t.Fatalf("split: %d %q", code, h.Get("Location"))
	}
	later := h.Get("Location")
	_, body, _ = a.do("GET", later, nil)
	contains(t, body, "Complete fermentation") // the later half is still active

	// The earlier half can't be reopened while the later one is recording.
	a.do("POST", p+"/reopen", url.Values{})
	_, body, _ = a.do("GET", p, nil)
	contains(t, body, "already recording", "complete it first, or merge")
	if strings.Contains(body, "constraint") {
		t.Error("raw database error leaked into the page")
	}

	// Merge them back.
	code, _, _ = a.do("POST", p+"/merge", url.Values{"other": {strings.TrimPrefix(later, "/fermentations/")}})
	if code != 303 {
		t.Fatalf("merge: %d", code)
	}
	_, body, _ = a.do("GET", "/fermentations?status=deleted", nil)
	contains(t, body, "deleted")

	// Close, then delete and restore.
	a.do("POST", p+"/close", url.Values{})
	_, body, _ = a.do("GET", p, nil)
	contains(t, body, "Reopen", "completed")
	if code, _, h := a.do("POST", p+"/delete", url.Values{}); code != 303 || h.Get("Location") != "/fermentations" {
		t.Fatalf("delete: %d %q", code, h.Get("Location"))
	}
	a.do("POST", p+"/restore", url.Values{})
	_, body, _ = a.do("GET", p, nil)
	contains(t, body, "Reopen")
}

func TestSettingsAndDevices(t *testing.T) {
	a := newApp(t)
	a.readings("A1", 2)
	a.login()

	code, _, _ := a.do("POST", "/settings", url.Values{"gravity": {"plato"}, "temp": {"f"}, "gap_hours": {"48"}})
	if code != 303 {
		t.Fatal(code)
	}
	_, body, _ := a.do("GET", "/", nil)
	contains(t, body, "°P", "°F")
	if strings.Contains(body, " SG") {
		t.Error("dashboard still shows SG after switching to Plato")
	}
	// Bad values are reported, not crashed on.
	a.do("POST", "/settings", url.Values{"gravity": {"brix"}, "temp": {"c"}, "gap_hours": {"48"}})
	_, body, _ = a.do("GET", "/settings", nil)
	contains(t, body, "gravity unit")
	a.do("POST", "/settings", url.Values{"gravity": {"sg"}, "temp": {"c"}, "gap_hours": {"soon"}})
	_, body, _ = a.do("GET", "/settings", nil)
	contains(t, body, "number of hours")

	code, _, _ = a.do("POST", "/devices/1", url.Values{"name": {"Big Fermenter"}, "gap_hours": {"36"}, "gravity_unit": {"sg"}})
	if code != 303 {
		t.Fatal(code)
	}
	_, body, _ = a.do("GET", "/devices", nil)
	contains(t, body, "Big Fermenter", `value="36"`)
}

func TestCrossSitePostsAreBlocked(t *testing.T) {
	a := newApp(t)
	a.readings("A1", 1)
	a.login()
	req, _ := http.NewRequest("POST", a.srv.URL+"/fermentations/1/close", strings.NewReader(""))
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := a.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site POST: %d", resp.StatusCode)
	}
}

func TestSecurityHeaders(t *testing.T) {
	a := newApp(t)
	_, _, h := a.do("GET", "/", nil)
	for _, k := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options"} {
		if h.Get(k) == "" {
			t.Errorf("missing %s", k)
		}
	}
	if !strings.Contains(h.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Error("CSP should restrict scripts")
	}
}

func TestStreamDeliversLiveReadings(t *testing.T) {
	a := newApp(t)
	id := a.readings("A1", 1)
	req, _ := http.NewRequest("GET", a.srv.URL+"/fermentations/"+fid(id)+"/stream", nil)
	resp, err := a.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" || resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("headers: %v", resp.Header)
	}
	// Wait for the subscription to register, then ingest a reading.
	deadline := time.Now().Add(2 * time.Second)
	for a.broker.Subscribers() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	a.readings("A1", 1)

	buf := make([]byte, 4096)
	var got string
	for !strings.Contains(got, "event: reading") {
		n, err := resp.Body.Read(buf)
		got += string(buf[:n])
		if err != nil {
			t.Fatalf("stream ended early: %v\n%s", err, got)
		}
	}
	contains(t, got, `"gravity":1.05`, `"temp":20`)

	// Shutdown ends the stream.
	close(a.done)
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("stream did not end cleanly: %v", err)
	}
}

func TestStreamOutlivesReadTimeout(t *testing.T) {
	a := newApp(t)
	id := a.readings("A1", 1)
	resp, err := a.client.Get(a.srv.URL + "/fermentations/" + fid(id) + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	deadline := time.Now().Add(2 * time.Second)
	for a.broker.Subscribers() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond) // well past the server ReadTimeout
	a.readings("A1", 1)
	buf := make([]byte, 4096)
	var got string
	for !strings.Contains(got, "event: reading") {
		n, err := resp.Body.Read(buf)
		got += string(buf[:n])
		if err != nil {
			t.Fatalf("stream dropped after ReadTimeout: %v\n%s", err, got)
		}
	}
}

func TestAssetsAreFingerprinted(t *testing.T) {
	a := newApp(t)
	_, body, _ := a.do("GET", "/", nil)
	i := strings.Index(body, "/static/app.js?v=")
	if i < 0 {
		t.Fatalf("page does not reference a fingerprinted app.js:\n%s", body)
	}
	ref := body[i : i+len("/static/app.js?v=")+10]
	code, js, h := a.do("GET", ref, nil)
	if code != 200 || !strings.Contains(js, "use strict") || !strings.Contains(h.Get("Cache-Control"), "immutable") {
		t.Fatalf("%s: %d %q", ref, code, h.Get("Cache-Control"))
	}
}
