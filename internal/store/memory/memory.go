// Package memory is an in-memory implementation of the domain ports with real
// transaction semantics (rollback on error). It backs tests and demos; the
// SQLite store is the production adapter.
package memory

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bzumhagen/idrometer/internal/domain"
)

type state struct {
	nextID   map[string]int64 // per table, like SQL autoincrement
	devices  map[int64]domain.Device
	ferms    map[int64]domain.Fermentation
	readings []domain.Reading
	settings map[string]string
	users    map[string]domain.User
	sessions map[string]domain.Session
	recipes  map[int64]domain.Recipe
	manual   map[int64]domain.ManualReading
	annots   map[int64]domain.Annotation
	atypes   map[int64]domain.AnnotationType
	calibs   map[int64]domain.Calibration
}

func (s *state) clone() *state {
	return &state{
		nextID:   maps.Clone(s.nextID),
		devices:  maps.Clone(s.devices),
		ferms:    maps.Clone(s.ferms),
		readings: slices.Clone(s.readings),
		settings: maps.Clone(s.settings),
		users:    maps.Clone(s.users),
		sessions: maps.Clone(s.sessions),
		recipes:  maps.Clone(s.recipes),
		manual:   maps.Clone(s.manual),
		annots:   maps.Clone(s.annots),
		atypes:   maps.Clone(s.atypes),
		calibs:   maps.Clone(s.calibs),
	}
}

func (s *state) id(table string) int64 { s.nextID[table]++; return s.nextID[table] }

// Store implements domain.TxManager.
type Store struct {
	mu sync.Mutex
	st *state
}

func New() *Store {
	st := &state{
		nextID:   map[string]int64{},
		devices:  map[int64]domain.Device{},
		ferms:    map[int64]domain.Fermentation{},
		settings: map[string]string{},
		users:    map[string]domain.User{},
		sessions: map[string]domain.Session{},
		recipes:  map[int64]domain.Recipe{},
		manual:   map[int64]domain.ManualReading{},
		annots:   map[int64]domain.Annotation{},
		atypes:   map[int64]domain.AnnotationType{},
		calibs:   map[int64]domain.Calibration{},
	}
	for _, n := range domain.DefaultAnnotationTypes { // the SQLite migration seeds the same list
		id := st.id("annotation_types")
		st.atypes[id] = domain.AnnotationType{ID: id, Name: n}
	}
	return &Store{st: st}
}

var _ domain.TxManager = (*Store)(nil)

func (s *Store) WithinTx(ctx context.Context, fn func(domain.Repos) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	work := s.st.clone()
	if err := fn(domain.Repos{
		Devices: (*devices)(work), Fermentations: (*ferms)(work),
		Readings: (*readings)(work), Settings: (*settings)(work),
		Users: (*users)(work), Sessions: (*sessions)(work), Recipes: (*recipes)(work),
		Manual: (*manual)(work), Annotations: (*annots)(work), AnnotationTypes: (*atypes)(work), Calibrations: (*calibs)(work),
	}); err != nil {
		return err
	}
	s.st = work
	return nil
}

// View runs fn on a throwaway copy, so accidental writes are discarded.
func (s *Store) View(ctx context.Context, fn func(domain.Repos) error) error {
	s.mu.Lock()
	work := s.st.clone()
	s.mu.Unlock()
	return fn(domain.Repos{
		Devices: (*devices)(work), Fermentations: (*ferms)(work),
		Readings: (*readings)(work), Settings: (*settings)(work),
		Users: (*users)(work), Sessions: (*sessions)(work), Recipes: (*recipes)(work),
		Manual: (*manual)(work), Annotations: (*annots)(work), AnnotationTypes: (*atypes)(work), Calibrations: (*calibs)(work),
	})
}

type devices state

func (d *devices) Get(_ context.Context, id int64) (domain.Device, error) {
	v, ok := d.devices[id]
	if !ok {
		return v, domain.ErrNotFound
	}
	return v, nil
}

func (d *devices) GetBySource(_ context.Context, source, hw string) (domain.Device, error) {
	for _, v := range d.devices {
		if v.Source == source && v.HardwareID == hw {
			return v, nil
		}
	}
	return domain.Device{}, domain.ErrNotFound
}

func (d *devices) Create(ctx context.Context, v *domain.Device) error {
	if _, err := d.GetBySource(ctx, v.Source, v.HardwareID); err == nil {
		return fmt.Errorf("%w: device exists", domain.ErrConflict)
	}
	if v.GravityUnit == "" {
		v.GravityUnit = domain.GravityAuto
	}
	v.ID = (*state)(d).id("devices")
	d.devices[v.ID] = *v
	return nil
}

func (d *devices) Update(_ context.Context, v domain.Device) error {
	if _, ok := d.devices[v.ID]; !ok {
		return domain.ErrNotFound
	}
	d.devices[v.ID] = v
	return nil
}

func (d *devices) List(context.Context) ([]domain.Device, error) {
	out := slices.Collect(maps.Values(d.devices))
	slices.SortFunc(out, func(a, b domain.Device) int { return int(a.ID - b.ID) })
	return out, nil
}

type ferms state

func (f *ferms) Get(_ context.Context, id int64) (domain.Fermentation, error) {
	v, ok := f.ferms[id]
	if !ok {
		return v, domain.ErrNotFound
	}
	return v, nil
}

// checkActive enforces one active, non-deleted fermentation per device.
func (f *ferms) checkActive(v domain.Fermentation) error {
	if v.Status != domain.StatusActive || v.DeletedAt != nil {
		return nil
	}
	for id, o := range f.ferms {
		if id != v.ID && o.DeviceID == v.DeviceID && o.Status == domain.StatusActive && o.DeletedAt == nil {
			return fmt.Errorf("%w: device %d already has an active fermentation", domain.ErrConflict, v.DeviceID)
		}
	}
	return nil
}

func (f *ferms) Create(_ context.Context, v *domain.Fermentation) error {
	if err := f.checkActive(*v); err != nil {
		return err
	}
	v.ID = (*state)(f).id("fermentations")
	f.ferms[v.ID] = *v
	return nil
}

func (f *ferms) Update(_ context.Context, v domain.Fermentation) error {
	if _, ok := f.ferms[v.ID]; !ok {
		return domain.ErrNotFound
	}
	if err := f.checkActive(v); err != nil {
		return err
	}
	f.ferms[v.ID] = v
	return nil
}

func (f *ferms) ActiveForDevice(_ context.Context, deviceID int64) (domain.Fermentation, error) {
	for _, v := range f.ferms {
		if v.DeviceID == deviceID && v.Status == domain.StatusActive && v.DeletedAt == nil {
			return v, nil
		}
	}
	return domain.Fermentation{}, domain.ErrNotFound
}

func (f *ferms) ListActive(ctx context.Context) ([]domain.Fermentation, error) {
	return f.List(ctx, domain.FermentationFilter{Status: domain.StatusActive})
}

func (f *ferms) List(_ context.Context, fl domain.FermentationFilter) ([]domain.Fermentation, error) {
	var out []domain.Fermentation
	for _, v := range f.ferms {
		switch {
		case fl.OnlyDeleted && v.DeletedAt == nil:
			continue
		case !fl.OnlyDeleted && !fl.IncludeDeleted && v.DeletedAt != nil:
			continue
		case fl.Status != "" && v.Status != fl.Status:
			continue
		case fl.DeviceID != 0 && v.DeviceID != fl.DeviceID:
			continue
		}
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b domain.Fermentation) int { return b.StartedAt.Compare(a.StartedAt) })
	return out, nil
}

type readings state

func (r *readings) Insert(_ context.Context, v *domain.Reading) error {
	v.ID = (*state)(r).id("readings")
	r.readings = append(r.readings, *v)
	return nil
}

func (r *readings) Bounds(_ context.Context, id int64) (first, last time.Time, ok bool, err error) {
	for _, v := range r.readings {
		if v.FermentationID != id {
			continue
		}
		if !ok || v.ReceivedAt.Before(first) {
			first = v.ReceivedAt
		}
		if !ok || v.ReceivedAt.After(last) {
			last = v.ReceivedAt
		}
		ok = true
	}
	return
}

func (r *readings) List(_ context.Context, id int64, includeExcluded bool) ([]domain.Reading, error) {
	var out []domain.Reading
	for _, v := range r.readings {
		if v.FermentationID == id && (includeExcluded || !v.Excluded) {
			out = append(out, v)
		}
	}
	slices.SortFunc(out, func(a, b domain.Reading) int { return a.ReceivedAt.Compare(b.ReceivedAt) })
	return out, nil
}

func (r *readings) MoveSince(_ context.Context, from, to int64, since time.Time) error {
	for i := range r.readings {
		if v := &r.readings[i]; v.FermentationID == from && !v.ReceivedAt.Before(since) {
			v.FermentationID = to
		}
	}
	return nil
}

func (r *readings) MoveAll(_ context.Context, from, to int64) error {
	for i := range r.readings {
		if r.readings[i].FermentationID == from {
			r.readings[i].FermentationID = to
		}
	}
	return nil
}

func (r *readings) SetExcluded(_ context.Context, id int64, from, to time.Time, ex bool) (int64, error) {
	var n int64
	for i := range r.readings {
		v := &r.readings[i]
		if v.FermentationID == id && !v.ReceivedAt.Before(from) && !v.ReceivedAt.After(to) && v.Excluded != ex {
			v.Excluded = ex
			n++
		}
	}
	return n, nil
}

type settings state

func (s *settings) Get(_ context.Context, k string) (string, error) {
	v, ok := s.settings[k]
	if !ok {
		return "", domain.ErrNotFound
	}
	return v, nil
}

func (s *settings) Set(_ context.Context, k, v string) error { s.settings[k] = v; return nil }

func (r *readings) Latest(_ context.Context, id int64) (domain.Reading, error) {
	var best domain.Reading
	found := false
	for _, v := range r.readings {
		if v.FermentationID == id && (!found || v.ReceivedAt.After(best.ReceivedAt) ||
			(v.ReceivedAt.Equal(best.ReceivedAt) && v.ID > best.ID)) {
			best, found = v, true
		}
	}
	if !found {
		return best, domain.ErrNotFound
	}
	return best, nil
}

type users state

func (u *users) Get(_ context.Context, name string) (domain.User, error) {
	v, ok := u.users[name]
	if !ok {
		return v, domain.ErrNotFound
	}
	return v, nil
}

func (u *users) Upsert(_ context.Context, v domain.User) error {
	if old, ok := u.users[v.Username]; ok {
		v.CreatedAt = old.CreatedAt // like SQL: only the hash changes
	}
	u.users[v.Username] = v
	return nil
}

type sessions state

func (s *sessions) Create(_ context.Context, v domain.Session) error {
	if _, dup := s.sessions[v.TokenHash]; dup {
		return domain.ErrConflict
	}
	s.sessions[v.TokenHash] = v
	return nil
}

func (s *sessions) Get(_ context.Context, h string) (domain.Session, error) {
	v, ok := s.sessions[h]
	if !ok {
		return v, domain.ErrNotFound
	}
	return v, nil
}

func (s *sessions) Delete(_ context.Context, h string) error { delete(s.sessions, h); return nil }

func (s *sessions) DeleteExpired(_ context.Context, now time.Time) error {
	for h, v := range s.sessions {
		if !v.ExpiresAt.After(now) {
			delete(s.sessions, h)
		}
	}
	return nil
}

func (s *sessions) DeleteForUser(_ context.Context, name string) error {
	for h, v := range s.sessions {
		if v.Username == name {
			delete(s.sessions, h)
		}
	}
	return nil
}

type recipes state

func (r *recipes) Get(_ context.Context, id int64) (domain.Recipe, error) {
	v, ok := r.recipes[id]
	if !ok {
		return v, domain.ErrNotFound
	}
	return v, nil
}

func (r *recipes) Create(_ context.Context, v *domain.Recipe) error {
	v.ID = (*state)(r).id("recipes")
	r.recipes[v.ID] = *v
	return nil
}

func (r *recipes) Update(_ context.Context, v domain.Recipe) error {
	old, ok := r.recipes[v.ID]
	if !ok {
		return domain.ErrNotFound
	}
	v.CreatedAt = old.CreatedAt
	r.recipes[v.ID] = v
	return nil
}

func (r *recipes) Delete(_ context.Context, id int64) error {
	if _, ok := r.recipes[id]; !ok {
		return domain.ErrNotFound
	}
	delete(r.recipes, id)
	for k, f := range r.ferms { // like ON DELETE SET NULL
		if f.RecipeID != nil && *f.RecipeID == id {
			f.RecipeID = nil
			r.ferms[k] = f
		}
	}
	return nil
}

func (r *recipes) List(context.Context) ([]domain.Recipe, error) {
	out := slices.Collect(maps.Values(r.recipes))
	slices.SortFunc(out, func(a, b domain.Recipe) int {
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return int(a.ID - b.ID)
	})
	return out, nil
}

type manual state

func (m *manual) Get(_ context.Context, id int64) (domain.ManualReading, error) {
	v, ok := m.manual[id]
	if !ok {
		return v, domain.ErrNotFound
	}
	return v, nil
}

func (m *manual) Create(_ context.Context, v *domain.ManualReading) error {
	v.ID = (*state)(m).id("manual")
	m.manual[v.ID] = *v
	return nil
}

func (m *manual) Update(_ context.Context, v domain.ManualReading) error {
	if _, ok := m.manual[v.ID]; !ok {
		return domain.ErrNotFound
	}
	m.manual[v.ID] = v
	return nil
}

func (m *manual) Delete(_ context.Context, id int64) error {
	if _, ok := m.manual[id]; !ok {
		return domain.ErrNotFound
	}
	delete(m.manual, id)
	return nil
}

func (m *manual) List(_ context.Context, fid int64) ([]domain.ManualReading, error) {
	var out []domain.ManualReading
	for _, v := range m.manual {
		if v.FermentationID == fid {
			out = append(out, v)
		}
	}
	slices.SortFunc(out, func(a, b domain.ManualReading) int {
		if c := a.TakenAt.Compare(b.TakenAt); c != 0 {
			return c
		}
		return int(a.ID - b.ID)
	})
	return out, nil
}

func (m *manual) MoveSince(_ context.Context, from, to int64, since time.Time) error {
	for k, v := range m.manual {
		if v.FermentationID == from && !v.TakenAt.Before(since) {
			v.FermentationID = to
			m.manual[k] = v
		}
	}
	return nil
}

func (m *manual) MoveAll(_ context.Context, from, to int64) error {
	for k, v := range m.manual {
		if v.FermentationID == from {
			v.FermentationID = to
			m.manual[k] = v
		}
	}
	return nil
}

type annots state

func (a *annots) Get(_ context.Context, id int64) (domain.Annotation, error) {
	v, ok := a.annots[id]
	if !ok {
		return v, domain.ErrNotFound
	}
	return v, nil
}

func (a *annots) Create(_ context.Context, v *domain.Annotation) error {
	v.ID = (*state)(a).id("annotations")
	a.annots[v.ID] = *v
	return nil
}

func (a *annots) Delete(_ context.Context, id int64) error {
	if _, ok := a.annots[id]; !ok {
		return domain.ErrNotFound
	}
	delete(a.annots, id)
	return nil
}

func (a *annots) List(_ context.Context, fid int64) ([]domain.Annotation, error) {
	var out []domain.Annotation
	for _, v := range a.annots {
		if v.FermentationID == fid {
			out = append(out, v)
		}
	}
	slices.SortFunc(out, func(x, y domain.Annotation) int {
		if c := x.At.Compare(y.At); c != 0 {
			return c
		}
		return int(x.ID - y.ID)
	})
	return out, nil
}

func (a *annots) MoveSince(_ context.Context, from, to int64, since time.Time) error {
	for k, v := range a.annots {
		if v.FermentationID == from && !v.At.Before(since) {
			v.FermentationID = to
			a.annots[k] = v
		}
	}
	return nil
}

func (a *annots) MoveAll(_ context.Context, from, to int64) error {
	for k, v := range a.annots {
		if v.FermentationID == from {
			v.FermentationID = to
			a.annots[k] = v
		}
	}
	return nil
}

type calibs state

func (c *calibs) Get(_ context.Context, id int64) (domain.Calibration, error) {
	v, ok := c.calibs[id]
	if !ok {
		return v, domain.ErrNotFound
	}
	return v, nil
}

func (c *calibs) Set(_ context.Context, v domain.Calibration) error {
	c.calibs[v.DeviceID] = v
	return nil
}

func (c *calibs) Delete(_ context.Context, id int64) error { delete(c.calibs, id); return nil }

type atypes state

func (e *atypes) List(context.Context) ([]domain.AnnotationType, error) {
	out := make([]domain.AnnotationType, 0, len(e.atypes))
	for _, v := range e.atypes {
		out = append(out, v)
	}
	slices.SortFunc(out, func(x, y domain.AnnotationType) int { return int(x.ID - y.ID) })
	return out, nil
}

func (e *atypes) taken(name string, except int64) bool {
	for _, v := range e.atypes {
		if v.ID != except && strings.EqualFold(v.Name, name) {
			return true
		}
	}
	return false
}

func (e *atypes) Create(_ context.Context, t *domain.AnnotationType) error {
	if e.taken(t.Name, 0) {
		return domain.ErrConflict
	}
	t.ID = (*state)(e).id("annotation_types")
	e.atypes[t.ID] = *t
	return nil
}

func (e *atypes) Rename(_ context.Context, id int64, name string) error {
	old, ok := e.atypes[id]
	if !ok {
		return domain.ErrNotFound
	}
	if e.taken(name, id) {
		return domain.ErrConflict
	}
	e.atypes[id] = domain.AnnotationType{ID: id, Name: name}
	for k, a := range e.annots {
		if a.Kind == old.Name {
			a.Kind = name
			e.annots[k] = a
		}
	}
	return nil
}

func (e *atypes) Delete(_ context.Context, id int64) error {
	if _, ok := e.atypes[id]; !ok {
		return domain.ErrNotFound
	}
	delete(e.atypes, id)
	return nil
}
