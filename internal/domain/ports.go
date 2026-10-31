package domain

import (
	"context"
	"time"
)

// Ports: interfaces the domain needs. Adapters implement them.

type DeviceRepo interface {
	Get(ctx context.Context, id int64) (Device, error)
	GetBySource(ctx context.Context, source, hardwareID string) (Device, error)
	Create(ctx context.Context, d *Device) error
	Update(ctx context.Context, d Device) error
	List(ctx context.Context) ([]Device, error)
}

type FermentationRepo interface {
	Get(ctx context.Context, id int64) (Fermentation, error)
	Create(ctx context.Context, f *Fermentation) error
	// Update returns ErrConflict if it would leave a device with two active
	// fermentations.
	Update(ctx context.Context, f Fermentation) error
	// ActiveForDevice returns ErrNotFound when the device has none.
	ActiveForDevice(ctx context.Context, deviceID int64) (Fermentation, error)
	ListActive(ctx context.Context) ([]Fermentation, error)
	List(ctx context.Context, f FermentationFilter) ([]Fermentation, error)
}

// FermentationFilter selects fermentations for listing. Deleted ones are
// excluded unless IncludeDeleted or OnlyDeleted is set.
type FermentationFilter struct {
	Status         FermentationStatus // empty = any
	DeviceID       int64              // 0 = any
	IncludeDeleted bool
	OnlyDeleted    bool
}

type ReadingRepo interface {
	Insert(ctx context.Context, r *Reading) error
	// Bounds returns the first and last received_at of all readings
	// (excluded ones included). ok is false if there are no readings.
	Bounds(ctx context.Context, fermentationID int64) (first, last time.Time, ok bool, err error)
	List(ctx context.Context, fermentationID int64, includeExcluded bool) ([]Reading, error)
	// Latest returns the newest reading, or ErrNotFound if there are none.
	Latest(ctx context.Context, fermentationID int64) (Reading, error)
	// MoveSince moves readings with received_at >= since to another fermentation.
	MoveSince(ctx context.Context, from, to int64, since time.Time) error
	// MoveAll moves every reading to another fermentation.
	MoveAll(ctx context.Context, from, to int64) error
	// SetExcluded flags readings with from <= received_at <= to and returns
	// how many changed.
	SetExcluded(ctx context.Context, fermentationID int64, from, to time.Time, excluded bool) (int64, error)
}

type SettingsRepo interface {
	// Get returns ErrNotFound when the key is unset.
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

type RecipeRepo interface {
	Get(ctx context.Context, id int64) (Recipe, error)
	Create(ctx context.Context, r *Recipe) error
	Update(ctx context.Context, r Recipe) error
	// Delete unlinks the recipe from its fermentations.
	Delete(ctx context.Context, id int64) error
	List(ctx context.Context) ([]Recipe, error)
}

type ManualReadingRepo interface {
	Get(ctx context.Context, id int64) (ManualReading, error)
	Create(ctx context.Context, m *ManualReading) error
	Update(ctx context.Context, m ManualReading) error
	Delete(ctx context.Context, id int64) error
	List(ctx context.Context, fermentationID int64) ([]ManualReading, error)
	MoveSince(ctx context.Context, from, to int64, since time.Time) error
	MoveAll(ctx context.Context, from, to int64) error
}

// AnnotationTypeRepo holds the user-managed list of timeline event types.
type AnnotationTypeRepo interface {
	// List returns the types in the order they were created.
	List(ctx context.Context) ([]AnnotationType, error)
	// Create fails with ErrConflict when the name exists (case-insensitive).
	Create(ctx context.Context, t *AnnotationType) error
	// Rename also relabels the events already logged with the old name.
	Rename(ctx context.Context, id int64, name string) error
	// Delete leaves events already logged untouched.
	Delete(ctx context.Context, id int64) error
}

type AnnotationRepo interface {
	Get(ctx context.Context, id int64) (Annotation, error)
	Create(ctx context.Context, a *Annotation) error
	Delete(ctx context.Context, id int64) error
	List(ctx context.Context, fermentationID int64) ([]Annotation, error)
	MoveSince(ctx context.Context, from, to int64, since time.Time) error
	MoveAll(ctx context.Context, from, to int64) error
}

type CalibrationRepo interface {
	// Get returns ErrNotFound when the device has none.
	Get(ctx context.Context, deviceID int64) (Calibration, error)
	Set(ctx context.Context, c Calibration) error
	Delete(ctx context.Context, deviceID int64) error
}

// UserRepo stores login credentials. Get returns ErrNotFound for unknown users.
type UserRepo interface {
	Get(ctx context.Context, username string) (User, error)
	Upsert(ctx context.Context, u User) error
}

// SessionRepo stores login sessions, keyed by a hash of the cookie token.
type SessionRepo interface {
	Create(ctx context.Context, s Session) error
	// Get returns ErrNotFound for unknown hashes.
	Get(ctx context.Context, tokenHash string) (Session, error)
	Delete(ctx context.Context, tokenHash string) error
	DeleteExpired(ctx context.Context, now time.Time) error
	DeleteForUser(ctx context.Context, username string) error
}

// PasswordHasher hashes and verifies passwords (argon2id in production).
type PasswordHasher interface {
	Hash(password string) (string, error)
	// Verify reports whether password matches hash.
	Verify(password, hash string) (bool, error)
}

// Repos is the set of repositories available inside a transaction.
type Repos struct {
	Devices         DeviceRepo
	Fermentations   FermentationRepo
	Readings        ReadingRepo
	Settings        SettingsRepo
	Users           UserRepo
	Sessions        SessionRepo
	Recipes         RecipeRepo
	Manual          ManualReadingRepo
	Annotations     AnnotationRepo
	AnnotationTypes AnnotationTypeRepo
	Calibrations    CalibrationRepo
}

// TxManager runs fn atomically: all repo calls commit together, or none do
// when fn returns an error.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(Repos) error) error
	// View runs fn against a consistent read-only snapshot without taking the
	// write lock. Repos used inside fn must not be used to write.
	View(ctx context.Context, fn func(Repos) error) error
}

type Clock interface{ Now() time.Time }

// SystemClock is the real clock.
type SystemClock struct{}

// Now is truncated to milliseconds, the resolution stores persist.
func (SystemClock) Now() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }

type EventType string

const (
	EventReading      EventType = "reading"
	EventFermentation EventType = "fermentation" // created, closed, edited
	EventSettings     EventType = "settings"     // display units or gap threshold changed
)

// Event is published after a change is committed.
type Event struct {
	Type           EventType
	DeviceID       int64
	FermentationID int64
	Reading        *Reading // set for EventReading
}

type EventPublisher interface{ Publish(Event) }

// NopPublisher discards events.
type NopPublisher struct{}

func (NopPublisher) Publish(Event) {}
