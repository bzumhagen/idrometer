package web_test

import (
	"net/url"
	"strings"
	"testing"
)

func TestFermentationPageStructure(t *testing.T) {
	a := newApp(t)
	id := a.readings("A1", 12)
	p := "/fermentations/" + fid(id)

	_, body, _ := a.do("GET", p, nil)
	// Hero tier, detail tier, device link, hint bubbles; signed out sees no edit actions.
	contains(t, body, "Current gravity", "Attenuation", "ABV (estimate)", "Trend",
		`class="stats detail"`, "Original gravity", "Avg temperature", "Readings",
		`href="/devices#dev-1"`, `popovertarget="hint-s-cur"`, "Download CSV", "Nothing logged yet")
	for _, bad := range []string{"Add measurement", "Add event", "Edit details", "Complete fermentation"} {
		if strings.Contains(body, bad) {
			t.Errorf("signed-out page contains %q", bad)
		}
	}

	a.login()
	_, body, _ = a.do("GET", p, nil)
	contains(t, body, `popovertarget="sheet-measure"`, `popovertarget="sheet-event"`, `id="sheet-edit"`, `id="range-bar"`,
		`name="return_to" value="log"`)
}

func TestLogMergesMeasurementsAndEvents(t *testing.T) {
	a := newApp(t)
	id := a.readings("A1", 4)
	a.login()
	p := "/fermentations/" + fid(id)
	a.do("POST", p+"/manual", url.Values{"taken": {at(1, 0)}, "gravity": {"1.040"}, "method": {"hydrometer"}, "note": {"first check"}})
	a.do("POST", p+"/annotations", url.Values{"at": {at(0, 30)}, "kind": {"Dry hop"}, "note": {"Citra"}})
	_, body, _ := a.do("GET", p, nil)
	contains(t, body, `id="log"`, "first check", "Citra", "Hydrometer")
	// Chronological: the 12:30 event precedes the 13:00 measurement.
	if strings.Index(body, "Citra") > strings.Index(body, "first check") {
		t.Error("log is not in time order")
	}
}

func TestPostsReturnToAnchor(t *testing.T) {
	a := newApp(t)
	id := a.readings("A1", 2)
	a.login()
	p := "/fermentations/" + fid(id)
	for anchor, want := range map[string]string{"chart": "#chart", "log": "#log", "evil": "", "": ""} {
		req := url.Values{"at": {at(0, 30)}, "kind": {"Dry hop"}, "return_to": {anchor}}
		code, _, h := a.doRef("POST", p+"/annotations", req, a.srv.URL+p)
		if code != 303 || h.Get("Location") != p+want {
			t.Errorf("return_to=%q: %d %q, want %q", anchor, code, h.Get("Location"), p+want)
		}
	}
}

func TestDevicePageHasCalibrationInsideCard(t *testing.T) {
	a := newApp(t)
	a.readings("A1", 3)
	a.login()
	_, body, _ := a.do("GET", "/devices", nil)
	card := body[strings.Index(body, `<article class="card device" id="dev-1">`):]
	card = card[:strings.Index(card, "</article>")]
	contains(t, card, "Calibration", `action="/devices/1/calibration"`, `data-cal`, "Latest angle 30.0°", "Settings", "Signal")
	// Collapsed until the user opens it.
	if !strings.Contains(card, `<details class="dev-cal"`) || strings.Contains(card, `<details class="dev-cal" open`) {
		t.Error("calibration should be a closed <details>")
	}
}

func TestDashboardCardShowsSparklineAndTrend(t *testing.T) {
	a := newApp(t)
	a.readings("A1", 30)
	_, body, _ := a.do("GET", "/", nil)
	contains(t, body, `<svg class="spark"`, "Latest gravity", `class="dot online"`)
}

func TestFermentationListHidesDeviceColumnForOneDevice(t *testing.T) {
	a := newApp(t)
	a.readings("A1", 6)
	_, body, _ := a.do("GET", "/fermentations", nil)
	contains(t, body, "OG → now", "ABV", `data-href="/fermentations/`)
	if strings.Contains(body, "<th>Device</th>") {
		t.Error("device column should be hidden with one device")
	}
	a.readings("B2", 2)
	_, body, _ = a.do("GET", "/fermentations", nil)
	contains(t, body, "<th>Device</th>")
}

func TestAnnotationTypesCanBeManaged(t *testing.T) {
	a := newApp(t)
	id := a.readings("A1", 3)
	p := "/fermentations/" + fid(id)

	// Signed out: the list is visible but read-only.
	_, body, _ := a.do("GET", "/settings", nil)
	contains(t, body, "Event types", "Dry hop")
	if strings.Contains(body, "<form") {
		t.Error("signed-out settings must not offer forms")
	}

	a.login()
	_, body, _ = a.do("GET", "/settings", nil)
	contains(t, body, `action="/annotation-types"`, `action="/annotation-types/1"`, `action="/annotation-types/1/delete"`)

	if code, _, _ := a.do("POST", "/annotation-types", url.Values{"name": {"Added fruit"}}); code != 303 {
		t.Fatalf("add: %d", code)
	}
	_, body, _ = a.do("GET", p, nil)
	contains(t, body, "<option>Added fruit</option>", `href="/settings#annotation-types"`)

	a.do("POST", p+"/annotations", url.Values{"at": {at(0, 30)}, "kind": {"Added fruit"}, "note": {"500 g mango"}})
	if code, _, _ := a.do("POST", "/annotation-types/7", url.Values{"name": {"Fruit addition"}}); code != 303 {
		t.Fatalf("rename: %d", code)
	}
	_, body, _ = a.do("GET", p, nil)
	contains(t, body, "Fruit addition", "500 g mango")

	// A duplicate comes back as a message, not an error page.
	a.do("POST", "/annotation-types", url.Values{"name": {"dry hop"}})
	_, body, _ = a.do("GET", "/settings", nil)
	contains(t, body, "already an event type")

	if code, _, _ := a.do("POST", "/annotation-types/7/delete", url.Values{}); code != 303 {
		t.Fatalf("delete: %d", code)
	}
	_, body, _ = a.do("GET", p, nil)
	contains(t, body, "Fruit addition") // the logged event survives
	if strings.Contains(body, "<option>Fruit addition</option>") {
		t.Error("deleted type is still offered")
	}
}

func TestRecipePageKeepsEditFormInASheet(t *testing.T) {
	a := newApp(t)
	a.login()
	_, _, h := a.do("POST", "/recipes", url.Values{"name": {"Hefeweizen"}, "style": {"Weissbier"}})
	_, body, _ := a.do("GET", h.Get("Location"), nil)
	// The page opens on the recipe itself; the form lives in a popover opened by Edit.
	contains(t, body, `popovertarget="sheet-recipe-edit"`, `id="sheet-recipe-edit" popover`, "No batches yet")
	if strings.Contains(body, `class="admin`) {
		t.Error("edit form must not be inline")
	}
}
