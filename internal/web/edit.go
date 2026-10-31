package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bzumhagen/idrometer/internal/domain"
)

func (s *Server) updateFermentation(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	name, notes := r.FormValue("name"), r.FormValue("notes")
	patch := domain.FermentationPatch{Name: &name, Notes: &notes}
	// An empty OG field clears the override.
	var og *float64
	if v := strings.TrimSpace(r.FormValue("og")); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0.99 || f > 1.2 {
			s.fail(w, r, fmt.Errorf("%w: original gravity must be an SG between 0.990 and 1.200", domain.ErrInvalid))
			return
		}
		og = &f
	}
	patch.OGOverride = &og
	// recipe: empty = none
	var recipe *int64
	if v := strings.TrimSpace(r.FormValue("recipe")); v != "" {
		rid, err := strconv.ParseInt(v, 10, 64)
		if err != nil || rid <= 0 {
			s.fail(w, r, fmt.Errorf("%w: bad recipe", domain.ErrInvalid))
			return
		}
		recipe = &rid
	}
	patch.RecipeID = &recipe
	if _, err := s.Editor.Update(r.Context(), id, patch); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Saved.")
}

// action builds a handler for simple one-id operations.
func (s *Server) action(msg string, fn func(r *http.Request, id int64) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err == nil {
			err = fn(r, id)
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.done(w, r, msg)
	}
}

func (s *Server) closeFermentation(w http.ResponseWriter, r *http.Request) {
	s.action("Fermentation completed. The next reading starts a new one.", func(r *http.Request, id int64) error {
		_, err := s.Editor.Close(r.Context(), id)
		return err
	})(w, r)
}

func (s *Server) reopenFermentation(w http.ResponseWriter, r *http.Request) {
	s.action("Fermentation reopened.", func(r *http.Request, id int64) error {
		_, err := s.Editor.Reopen(r.Context(), id)
		return err
	})(w, r)
}

func (s *Server) deleteFermentation(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err == nil {
		err = s.Editor.Delete(r.Context(), id)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.setFlash(w, "Fermentation deleted. You can restore it from the Deleted list.", false)
	http.Redirect(w, r, "/fermentations", http.StatusSeeOther)
}

func (s *Server) restoreFermentation(w http.ResponseWriter, r *http.Request) {
	s.action("Fermentation restored (as completed).", func(r *http.Request, id int64) error {
		return s.Editor.Restore(r.Context(), id)
	})(w, r)
}

func (s *Server) splitFermentation(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	at, err := formTime(r, "at")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	_, later, err := s.Editor.Split(r.Context(), id, at)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.setFlash(w, "Split. You are now viewing the later part.", false)
	http.Redirect(w, r, "/fermentations/"+strconv.FormatInt(later.ID, 10), http.StatusSeeOther)
}

func (s *Server) mergeFermentation(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// The page's fermentation is the one that is kept; the chosen one is merged into it.
	other, err := strconv.ParseInt(r.FormValue("other"), 10, 64)
	if err != nil || other <= 0 {
		s.fail(w, r, fmt.Errorf("%w: pick a fermentation to merge", domain.ErrInvalid))
		return
	}
	if _, err := s.Editor.Merge(r.Context(), other, id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Merged. The other fermentation is in the Deleted list.")
}

func (s *Server) excludeReadings(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	from, err := formTime(r, "from")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	to, err := formTime(r, "to")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	to = to.Add(time.Second - time.Millisecond) // inclusive of the whole last second
	exclude := r.FormValue("mode") != "include"
	n, err := s.Editor.SetExcluded(r.Context(), id, from, to, exclude)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	verb := "Excluded"
	if !exclude {
		verb = "Restored"
	}
	s.done(w, r, fmt.Sprintf("%s %d reading(s).", verb, n))
}

func (s *Server) updateDevice(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	name := r.FormValue("name")
	unit := domain.GravityUnit(r.FormValue("gravity_unit"))
	var gap *time.Duration // empty field = use the global gap
	if v := strings.TrimSpace(r.FormValue("gap_hours")); v != "" {
		h, err := strconv.ParseFloat(v, 64)
		if err != nil || h <= 0 {
			s.fail(w, r, fmt.Errorf("%w: gap must be a number of hours", domain.ErrInvalid))
			return
		}
		d := time.Duration(h * float64(time.Hour))
		gap = &d
	}
	if _, err := s.Editor.UpdateDevice(r.Context(), id, domain.DevicePatch{
		DisplayName: &name, GapThreshold: &gap, GravityUnit: &unit,
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Device saved.")
}

func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	hours, err := strconv.ParseFloat(strings.TrimSpace(r.FormValue("gap_hours")), 64)
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: gap must be a number of hours", domain.ErrInvalid))
		return
	}
	err = s.Prefs.Update(r.Context(), domain.Preferences{
		Gravity: domain.DisplayGravity(r.FormValue("gravity")),
		Temp:    domain.TempUnit(r.FormValue("temp")),
		Gap:     time.Duration(hours * float64(time.Hour)),
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.done(w, r, "Settings saved.")
}

// parseLocalTime reads a <input type="datetime-local"> value. tz is the
// browser's Date.getTimezoneOffset() in minutes (UTC minus local); without
// JavaScript it is empty and the value is taken as UTC.
func parseLocalTime(value, tz string) (time.Time, error) {
	value = strings.TrimSpace(value)
	var t time.Time
	var err error
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if t, err = time.ParseInLocation(layout, value, time.UTC); err == nil {
			break
		}
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: invalid date/time %q", domain.ErrInvalid, value)
	}
	if tz = strings.TrimSpace(tz); tz != "" {
		min, err := strconv.Atoi(tz)
		if err != nil || min < -14*60 || min > 14*60 {
			return time.Time{}, fmt.Errorf("%w: invalid timezone offset", domain.ErrInvalid)
		}
		t = t.Add(time.Duration(min) * time.Minute)
	}
	return t, nil
}

// formTime reads a datetime-local form field. The offset comes from the
// field's own <field>_tz value (exact across DST changes), falling back to the
// form-wide tz.
func formTime(r *http.Request, field string) (time.Time, error) {
	tz := r.FormValue(field + "_tz")
	if strings.TrimSpace(tz) == "" {
		tz = r.FormValue("tz")
	}
	return parseLocalTime(r.FormValue(field), tz)
}
