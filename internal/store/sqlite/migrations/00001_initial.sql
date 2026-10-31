-- +goose Up
-- Timestamps are INTEGER unix milliseconds (UTC).

CREATE TABLE devices (
    id              INTEGER PRIMARY KEY,
    source          TEXT    NOT NULL,
    hardware_id     TEXT    NOT NULL,
    reported_name   TEXT    NOT NULL DEFAULT '',
    display_name    TEXT    NOT NULL DEFAULT '',
    gap_threshold_s INTEGER,
    gravity_unit    TEXT    NOT NULL DEFAULT 'auto' CHECK (gravity_unit IN ('auto','sg','plato')),
    last_seen_at    INTEGER NOT NULL DEFAULT 0,
    last_battery_v  REAL    NOT NULL DEFAULT 0,
    last_rssi       INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    UNIQUE (source, hardware_id)
);

CREATE TABLE recipes (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL,
    style      TEXT    NOT NULL DEFAULT '',
    target_og  REAL,
    target_fg  REAL,
    yeast      TEXT    NOT NULL DEFAULT '',
    notes      TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE fermentations (
    id           INTEGER PRIMARY KEY,
    device_id    INTEGER NOT NULL REFERENCES devices(id),
    recipe_id    INTEGER REFERENCES recipes(id) ON DELETE SET NULL,
    name         TEXT    NOT NULL,
    status       TEXT    NOT NULL CHECK (status IN ('active','completed')),
    started_at   INTEGER NOT NULL,
    ended_at     INTEGER,
    close_reason TEXT    NOT NULL DEFAULT '',
    og_override  REAL,
    notes        TEXT    NOT NULL DEFAULT '',
    deleted_at   INTEGER
);

-- A device has at most one open fermentation.
CREATE UNIQUE INDEX fermentations_one_active_per_device
    ON fermentations(device_id)
    WHERE status = 'active' AND deleted_at IS NULL;
CREATE INDEX fermentations_started_at ON fermentations(started_at);

CREATE TABLE readings (
    id              INTEGER PRIMARY KEY,
    device_id       INTEGER NOT NULL REFERENCES devices(id),
    fermentation_id INTEGER NOT NULL REFERENCES fermentations(id),
    received_at     INTEGER NOT NULL,
    angle           REAL    NOT NULL,
    temperature_c   REAL    NOT NULL,
    battery_v       REAL    NOT NULL,
    gravity_device  REAL    NOT NULL DEFAULT 0,
    rssi            INTEGER NOT NULL DEFAULT 0,
    interval_s      INTEGER NOT NULL DEFAULT 0,
    excluded        INTEGER NOT NULL DEFAULT 0 CHECK (excluded IN (0,1)),
    raw_payload     BLOB
);
CREATE INDEX readings_fermentation_time ON readings(fermentation_id, received_at);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- +goose Down
DROP TABLE settings;
DROP TABLE readings;
DROP TABLE fermentations;
DROP TABLE recipes;
DROP TABLE devices;
