-- +goose Up
CREATE TABLE manual_readings (
    id              INTEGER PRIMARY KEY,
    fermentation_id INTEGER NOT NULL REFERENCES fermentations(id) ON DELETE CASCADE,
    taken_at        INTEGER NOT NULL,
    gravity_sg      REAL    NOT NULL,
    temperature_c   REAL,
    method          TEXT    NOT NULL CHECK (method IN ('hydrometer','refractometer','other')),
    is_og           INTEGER NOT NULL DEFAULT 0 CHECK (is_og IN (0,1)),
    is_fg           INTEGER NOT NULL DEFAULT 0 CHECK (is_fg IN (0,1)),
    note            TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX manual_readings_fermentation ON manual_readings(fermentation_id, taken_at);

CREATE TABLE annotations (
    id              INTEGER PRIMARY KEY,
    fermentation_id INTEGER NOT NULL REFERENCES fermentations(id) ON DELETE CASCADE,
    at              INTEGER NOT NULL,
    kind            TEXT    NOT NULL CHECK (kind IN ('pitch','dry_hop','temp_change','cold_crash','transfer','other')),
    note            TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX annotations_fermentation ON annotations(fermentation_id, at);

-- gravity = c0 + c1*angle + c2*angle^2 + c3*angle^3, in output_unit
CREATE TABLE device_calibrations (
    device_id   INTEGER PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    c0          REAL NOT NULL,
    c1          REAL NOT NULL,
    c2          REAL NOT NULL,
    c3          REAL NOT NULL,
    output_unit TEXT NOT NULL CHECK (output_unit IN ('sg','plato')),
    note        TEXT NOT NULL DEFAULT '',
    updated_at  INTEGER NOT NULL
);

-- +goose Down
DROP TABLE device_calibrations;
DROP TABLE annotations;
DROP TABLE manual_readings;
