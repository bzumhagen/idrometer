package web_test

import (
	"net/url"
	"strings"
	"testing"
)

func TestRecipesWorkflow(t *testing.T) {
	a := newApp(t)
	id := a.readings("A1", 3)
	_, body, _ := a.do("GET", "/recipes", nil)
	contains(t, body, "No recipes yet", "Sign in")

	a.login()
	code, _, h := a.do("POST", "/recipes", url.Values{"name": {"Hefeweizen"}, "style": {"Weissbier"}, "target_og": {"1.052"}, "target_fg": {"1.012"}})
	if code != 303 || !strings.HasPrefix(h.Get("Location"), "/recipes/") {
		t.Fatalf("create: %d %q", code, h.Get("Location"))
	}
	rpath := h.Get("Location")
	_, body, _ = a.do("GET", rpath, nil)
	contains(t, body, "Hefeweizen", "1.052 SG", "No batches yet")

	// Link a fermentation to it from the fermentation page.
	p := "/fermentations/" + fid(id)
	_, body, _ = a.do("GET", p, nil)
	contains(t, body, `<option value="1"`)
	a.do("POST", p+"/update", url.Values{"name": {"Batch 1"}, "recipe": {"1"}})
	_, body, _ = a.do("GET", p, nil)
	contains(t, body, `href="/recipes/1"`, "selected")
	_, body, _ = a.do("GET", rpath, nil)
	contains(t, body, "Batch 1", "/compare?ids="+fid(id))

	// Invalid input is reported.
	a.do("POST", "/recipes", url.Values{"name": {"Bad"}, "target_og": {"9"}})
	_, body, _ = a.do("GET", "/recipes", nil)
	contains(t, body, "target gravity")

	// Deleting keeps the fermentation.
	if code, _, h := a.do("POST", rpath+"/delete", url.Values{}); code != 303 || h.Get("Location") != "/recipes" {
		t.Fatalf("delete: %d %q", code, h.Get("Location"))
	}
	if code, _, _ := a.do("GET", p, nil); code != 200 {
		t.Fatalf("fermentation after recipe delete: %d", code)
	}
}

func TestMeasurementsAndEvents(t *testing.T) {
	a := newApp(t)
	id := a.readings("A1", 4)
	a.login()
	p := "/fermentations/" + fid(id)

	code, _, _ := a.do("POST", p+"/manual", url.Values{
		"taken": {at(0, 0)}, "gravity": {"1.060"}, "method": {"refractometer"}, "mark": {"og"}, "note": {"pre-pitch"},
	})
	if code != 303 {
		t.Fatal(code)
	}
	a.do("POST", p+"/manual", url.Values{"taken": {at(1, 0)}, "gravity": {"1.012"}, "mark": {"fg"}, "temp": {"20"}})
	_, body, _ := a.do("GET", p, nil)
	contains(t, body, "Measurement added", "Refractometer", "pre-pitch", "1.060 SG", "measured FG", "OG", "FG")

	// The series API carries them for the graph.
	_, series, _ := a.do("GET", "/api/v1/fermentations/"+fid(id)+"/series", nil)
	contains(t, series, `"kind":"OG"`, `"kind":"FG"`)

	if code, _, _ := a.do("POST", p+"/annotations", url.Values{"at": {at(0, 30)}, "kind": {"Dry hop"}, "note": {"50 g Citra"}}); code != 303 {
		t.Fatal(code)
	}
	_, body, _ = a.do("GET", p, nil)
	contains(t, body, "Dry hop", "50 g Citra")
	_, series, _ = a.do("GET", "/api/v1/fermentations/"+fid(id)+"/series", nil)
	contains(t, series, "Dry hop: 50 g Citra")

	// Bad input is a message, not an error page.
	a.do("POST", p+"/manual", url.Values{"taken": {at(0, 0)}, "gravity": {"abc"}})
	_, body, _ = a.do("GET", p, nil)
	contains(t, body, "is not a number")
	a.do("POST", p+"/annotations", url.Values{"at": {at(0, 30)}, "kind": {"party"}})
	_, body, _ = a.do("GET", p, nil)
	contains(t, body, "unknown event type")

	// Entering Plato converts to SG.
	a.do("POST", "/settings", url.Values{"gravity": {"plato"}, "temp": {"c"}, "gap_hours": {"72"}})
	a.do("POST", p+"/manual", url.Values{"taken": {at(2, 0)}, "gravity": {"10"}})
	_, series, _ = a.do("GET", "/api/v1/fermentations/"+fid(id)+"/series", nil)
	contains(t, series, `"gravity":10`)

	// Removing works.
	if code, _, _ := a.do("POST", "/manual/1/delete", url.Values{}); code != 303 {
		t.Fatal(code)
	}
	if code, _, _ := a.do("POST", "/annotations/1/delete", url.Values{}); code != 303 {
		t.Fatal(code)
	}
}

func TestCalibrationFromDevicesPage(t *testing.T) {
	a := newApp(t)
	id := a.readings("A1", 3) // angle 30, device gravity ~1.050
	a.login()
	_, body, _ := a.do("GET", "/devices", nil)
	contains(t, body, "Calibration", "Save calibration")

	code, _, _ := a.do("POST", "/devices/1/calibration", url.Values{"c0": {"1"}, "c1": {"0.001"}, "unit": {"sg"}, "note": {"water + sugar test"}})
	if code != 303 {
		t.Fatal(code)
	}
	_, body, _ = a.do("GET", "/devices", nil)
	contains(t, body, "Calibration saved", "water &#43; sugar test", `value="0.001"`, "Remove calibration")
	// angle 30 -> 1.030 on the graph data, whatever the device reported.
	_, series, _ := a.do("GET", "/api/v1/fermentations/"+fid(id)+"/series", nil)
	contains(t, series, `"gravity":[1.03,1.03,1.03]`)
	_, body, _ = a.do("GET", "/", nil)
	contains(t, body, "1.030 SG")

	a.do("POST", "/devices/1/calibration", url.Values{"c0": {"x"}, "unit": {"sg"}})
	_, body, _ = a.do("GET", "/devices", nil)
	contains(t, body, "not a number")
	a.do("POST", "/devices/1/calibration", url.Values{"unit": {"sg"}}) // all zero
	_, body, _ = a.do("GET", "/devices", nil)
	contains(t, body, "all coefficients are zero")

	a.do("POST", "/devices/1/calibration/clear", url.Values{})
	_, series, _ = a.do("GET", "/api/v1/fermentations/"+fid(id)+"/series", nil)
	contains(t, series, `"gravity":[1.05,`)
}

func TestComparePage(t *testing.T) {
	a := newApp(t)
	id1 := a.readings("A1", 3)
	id2 := a.readings("B2", 3)
	_, body, _ := a.do("GET", "/compare", nil)
	contains(t, body, "Show as attenuation", "Spindle A1", "Spindle B2")
	_, body, _ = a.do("GET", "/compare?ids="+fid(id1)+","+fid(id2)+",999,abc", nil)
	contains(t, body, `data-ids="`+fid(id1)+","+fid(id2)+`"`, "checked")
	if strings.Contains(body, "999") {
		t.Error("unknown ids must be dropped")
	}
}

func TestNewPagesRequireLoginToEdit(t *testing.T) {
	a := newApp(t)
	id := a.readings("A1", 2)
	for _, p := range []string{
		"/recipes", "/recipes/1", "/recipes/1/delete", "/fermentations/" + fid(id) + "/manual",
		"/fermentations/" + fid(id) + "/annotations", "/manual/1/delete", "/annotations/1/delete",
		"/devices/1/calibration", "/devices/1/calibration/clear",
	} {
		code, _, h := a.do("POST", p, url.Values{})
		if code != 303 || !strings.HasPrefix(h.Get("Location"), "/login") {
			t.Errorf("%s: %d %q", p, code, h.Get("Location"))
		}
	}
}

func TestIngestTokenOnlyForAdmin(t *testing.T) {
	a := newApp(t)
	_, body, _ := a.do("GET", "/settings", nil)
	if strings.Contains(body, "test-ingest-token-123") || strings.Contains(body, "iSpindel setup") {
		t.Fatal("signed-out settings page leaks the ingest token")
	}
	a.login()
	_, body, _ = a.do("GET", "/settings", nil)
	contains(t, body, "iSpindel setup", "test-ingest-token-123", "/api/v1/ingest/ispindel", "<dd>8080</dd>")
}
