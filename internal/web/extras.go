package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/bzumhagen/idrometer/internal/domain"
)

// ---- recipes ----

type recipesData struct{ Items []domain.RecipeSummary }

func (s *Server) recipeList(w http.ResponseWriter, r *http.Request, p *page) (string, error) {
	rs, err := s.Queries.Recipes(r.Context())
	if err != nil {
		return "", err
	}
	p.Title, p.Nav, p.Data = "Recipes", "recipes", recipesData{Items: rs}
	return "recipes", nil
}

type recipeData struct {
	D          domain.RecipeDetail
	CompareIDs string
}

func (s *Server) recipePage(w http.ResponseWriter, r *http.Request, p *page) (string, error) {
	id, err := pathID(r)
	if err != nil {
		return "", err
	}
	d, err := s.Queries.Recipe(r.Context(), id)
	if err != nil {
		return "", err
	}
	ids := make([]string, 0, len(d.Fermentations))
	for _, f := range d.Fermentations {
		ids = append(ids, strconv.FormatInt(f.ID, 10))
	}
	p.Title, p.Nav = d.Recipe.Name, "recipes"
	p.Data = recipeData{D: d, CompareIDs: strings.Join(ids, ",")}
	return "recipe", nil
}

// recipeFromForm reads recipe fields. Target gravities are entered in the
// user's display unit.
func (s *Server) recipeFromForm(r *http.Request, prefs domain.Preferences) (domain.Recipe, error) {
	rec := domain.Recipe{
		Name: r.FormValue("name"), Style: strings.TrimSpace(r.FormValue("style")),
		Yeast: strings.TrimSpace(r.FormValue("yeast")), Notes: r.FormValue("notes"),
	}
	var err error
	if rec.TargetOG, err = optionalGravity(r.FormValue("target_og"), prefs); err != nil {
		return rec, err
	}
	rec.TargetFG, err = optionalGravity(r.FormValue("target_fg"), prefs)
	return rec, err
}

func (s *Server) createRecipe(w http.ResponseWriter, r *http.Request) {
	prefs, err := s.Queries.Preferences(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rec, err := s.recipeFromForm(r, prefs)
	if err == nil {
		rec, err = s.Editor.CreateRecipe(r.Context(), rec)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.setFlash(w, "Recipe created.", false)
	http.Redirect(w, r, "/recipes/"+strconv.FormatInt(rec.ID, 10), http.StatusSeeOther)
}

func (s *Server) updateRecipe(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	var prefs domain.Preferences
	var rec domain.Recipe
	if err == nil {
		prefs, err = s.Queries.Preferences(r.Context())
	}
	if err == nil {
		rec, err = s.recipeFromForm(r, prefs)
	}
	if err == nil {
		rec.ID = id
		_, err = s.Editor.UpdateRecipe(r.Context(), rec)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Recipe saved.")
}

func (s *Server) deleteRecipe(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err == nil {
		err = s.Editor.DeleteRecipe(r.Context(), id)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.setFlash(w, "Recipe deleted. Fermentations that used it were kept.", false)
	http.Redirect(w, r, "/recipes", http.StatusSeeOther)
}

// optionalGravity parses a gravity typed in the display unit; empty = nil.
func optionalGravity(v string, prefs domain.Preferences) (*float64, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	sg, err := gravityToSG(v, prefs.Gravity)
	if err != nil {
		return nil, err
	}
	return &sg, nil
}

func gravityToSG(v string, unit domain.DisplayGravity) (float64, error) {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q is not a number", domain.ErrInvalid, v)
	}
	gu := domain.GravitySG
	if unit == domain.DisplayPlato {
		gu = domain.GravityPlato
	}
	return domain.NormalizeGravity(f, gu)
}

// ---- manual readings & annotations ----

func (s *Server) addManualReading(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	prefs, err := s.Queries.Preferences(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	when, err := formTime(r, "taken")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// The unit the user typed it in; defaults to their display unit.
	unit := prefs.Gravity
	if u := domain.DisplayGravity(r.FormValue("unit")); u == domain.DisplaySG || u == domain.DisplayPlato {
		unit = u
	}
	sg, err := gravityToSG(r.FormValue("gravity"), unit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	m := domain.ManualReading{
		FermentationID: id, TakenAt: when, GravitySG: sg,
		Method: domain.MeasureMethod(r.FormValue("method")), Note: strings.TrimSpace(r.FormValue("note")),
		IsOG: r.FormValue("mark") == "og", IsFG: r.FormValue("mark") == "fg",
	}
	if v := strings.TrimSpace(r.FormValue("temp")); v != "" {
		t, err := strconv.ParseFloat(v, 64)
		if err != nil {
			s.fail(w, r, fmt.Errorf("%w: %q is not a temperature", domain.ErrInvalid, v))
			return
		}
		if prefs.Temp == domain.TempF {
			t = domain.FToC(t)
		}
		m.TemperatureC = &t
	}
	if _, err := s.Editor.AddManualReading(r.Context(), m); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Measurement added.")
}

func (s *Server) deleteManualReading(w http.ResponseWriter, r *http.Request) {
	s.action("Measurement removed.", func(r *http.Request, id int64) error {
		return s.Editor.DeleteManualReading(r.Context(), id)
	})(w, r)
}

func (s *Server) addAnnotation(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	when, err := formTime(r, "at")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	_, err = s.Editor.AddAnnotation(r.Context(), domain.Annotation{
		FermentationID: id, At: when, Kind: r.FormValue("kind"), Note: r.FormValue("note"),
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Event added.")
}

func (s *Server) deleteAnnotation(w http.ResponseWriter, r *http.Request) {
	s.action("Event removed.", func(r *http.Request, id int64) error {
		return s.Editor.DeleteAnnotation(r.Context(), id)
	})(w, r)
}

// ---- event types ----

func (s *Server) addAnnotationType(w http.ResponseWriter, r *http.Request) {
	if _, err := s.Editor.AddAnnotationType(r.Context(), r.FormValue("name")); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Event type added.")
}

func (s *Server) renameAnnotationType(w http.ResponseWriter, r *http.Request) {
	s.action("Event type renamed.", func(r *http.Request, id int64) error {
		return s.Editor.RenameAnnotationType(r.Context(), id, r.FormValue("name"))
	})(w, r)
}

func (s *Server) deleteAnnotationType(w http.ResponseWriter, r *http.Request) {
	s.action("Event type removed.", func(r *http.Request, id int64) error {
		return s.Editor.DeleteAnnotationType(r.Context(), id)
	})(w, r)
}

// ---- calibration ----

func (s *Server) setCalibration(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	c := domain.Calibration{
		DeviceID: id, OutputUnit: domain.GravityUnit(r.FormValue("unit")), Note: strings.TrimSpace(r.FormValue("note")),
	}
	for i := range c.C {
		v := strings.TrimSpace(r.FormValue("c" + strconv.Itoa(i)))
		if v == "" {
			continue
		}
		if c.C[i], err = strconv.ParseFloat(v, 64); err != nil {
			s.fail(w, r, fmt.Errorf("%w: coefficient c%d %q is not a number", domain.ErrInvalid, i, v))
			return
		}
	}
	if err := s.Editor.SetCalibration(r.Context(), c); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Calibration saved. Past readings now use it too.")
}

func (s *Server) clearCalibration(w http.ResponseWriter, r *http.Request) {
	s.action("Calibration removed. The device's own gravity is used again.", func(r *http.Request, id int64) error {
		return s.Editor.ClearCalibration(r.Context(), id)
	})(w, r)
}

// ---- compare ----

type compareData struct {
	All      []domain.FermentationSummary
	Selected map[int64]bool
	IDs      string
}

func (s *Server) comparePage(w http.ResponseWriter, r *http.Request, p *page) (string, error) {
	all, err := s.Queries.Fermentations(r.Context(), domain.FermentationFilter{})
	if err != nil {
		return "", err
	}
	known := map[int64]bool{}
	for _, f := range all {
		known[f.ID] = true
	}
	sel := map[int64]bool{}
	var ids []string
	for _, part := range strings.Split(r.URL.Query().Get("ids"), ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err == nil && known[id] && !sel[id] && len(ids) < 8 {
			sel[id] = true
			ids = append(ids, strconv.FormatInt(id, 10))
		}
	}
	p.Title, p.Nav = "Compare", "compare"
	p.Data = compareData{All: all, Selected: sel, IDs: strings.Join(ids, ",")}
	return "compare", nil
}
