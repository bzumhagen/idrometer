package domain

import "time"

// Device is a hydrometer, identified by its source type and hardware id.
type Device struct {
	ID           int64
	Source       string // e.g. "ispindel"
	HardwareID   string // e.g. ESP chip id
	ReportedName string
	DisplayName  string // user-chosen; empty means use ReportedName
	// GapThreshold overrides the global gap for this device when non-nil.
	GapThreshold *time.Duration
	GravityUnit  GravityUnit
	LastSeenAt   time.Time
	LastBatteryV float64
	LastRSSI     int
	CreatedAt    time.Time
}

// Name returns the best human-readable name for the device.
func (d Device) Name() string {
	switch {
	case d.DisplayName != "":
		return d.DisplayName
	case d.ReportedName != "":
		return d.ReportedName
	}
	return d.Source + "-" + d.HardwareID
}

type FermentationStatus string

const (
	StatusActive    FermentationStatus = "active"
	StatusCompleted FermentationStatus = "completed"
)

type CloseReason string

const (
	CloseManual CloseReason = "manual"
	CloseGap    CloseReason = "gap"
	CloseSplit  CloseReason = "split"
)

// Fermentation is one batch: a run of readings from one device.
type Fermentation struct {
	ID          int64
	DeviceID    int64
	RecipeID    *int64
	Name        string
	Status      FermentationStatus
	StartedAt   time.Time
	EndedAt     *time.Time
	CloseReason CloseReason
	OGOverride  *float64
	Notes       string
	DeletedAt   *time.Time
}

// Reading is one sample from a device. GravityDevice is in SG; 0 means the
// device reported no gravity.
type Reading struct {
	ID             int64
	DeviceID       int64
	FermentationID int64
	ReceivedAt     time.Time
	Angle          float64
	TemperatureC   float64
	BatteryV       float64
	GravityDevice  float64
	// Gravity is the SG to display: calibrated when the device has a
	// calibration, else GravityDevice. It is computed on read, never stored.
	Gravity    float64
	RSSI       int
	IntervalS  int
	Excluded   bool
	RawPayload []byte
}

// ReadingInput is what an inbound adapter hands to the domain.
// Gravity is as reported by the device; its unit is resolved per device.
type ReadingInput struct {
	Source       string
	HardwareID   string
	Name         string
	Angle        float64
	TemperatureC float64
	BatteryV     float64
	Gravity      float64
	RSSI         int
	IntervalS    int
	RawPayload   []byte
}
