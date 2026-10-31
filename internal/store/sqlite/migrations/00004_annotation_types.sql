-- +goose Up
-- Annotation types become user-managed. An annotation stores its type's label
-- directly, so removing a type never changes events already logged.
CREATE TABLE annotation_types (
    id   INTEGER PRIMARY KEY,
    name TEXT NOT NULL COLLATE NOCASE UNIQUE
);
INSERT INTO annotation_types(name) VALUES
    ('Pitched yeast'), ('Dry hop'), ('Temperature change'), ('Cold crash'), ('Transfer'), ('Note');

CREATE TABLE annotations_new (
    id              INTEGER PRIMARY KEY,
    fermentation_id INTEGER NOT NULL REFERENCES fermentations(id) ON DELETE CASCADE,
    at              INTEGER NOT NULL,
    kind            TEXT    NOT NULL,
    note            TEXT    NOT NULL DEFAULT ''
);
INSERT INTO annotations_new(id, fermentation_id, at, kind, note)
SELECT id, fermentation_id, at,
       CASE kind WHEN 'pitch' THEN 'Pitched yeast' WHEN 'dry_hop' THEN 'Dry hop'
                 WHEN 'temp_change' THEN 'Temperature change' WHEN 'cold_crash' THEN 'Cold crash'
                 WHEN 'transfer' THEN 'Transfer' ELSE 'Note' END,
       note
FROM annotations;
DROP TABLE annotations;
ALTER TABLE annotations_new RENAME TO annotations;
CREATE INDEX annotations_fermentation ON annotations(fermentation_id, at);

-- +goose Down
CREATE TABLE annotations_old (
    id              INTEGER PRIMARY KEY,
    fermentation_id INTEGER NOT NULL REFERENCES fermentations(id) ON DELETE CASCADE,
    at              INTEGER NOT NULL,
    kind            TEXT    NOT NULL CHECK (kind IN ('pitch','dry_hop','temp_change','cold_crash','transfer','other')),
    note            TEXT    NOT NULL DEFAULT ''
);
INSERT INTO annotations_old(id, fermentation_id, at, kind, note)
SELECT id, fermentation_id, at,
       CASE kind WHEN 'Pitched yeast' THEN 'pitch' WHEN 'Dry hop' THEN 'dry_hop'
                 WHEN 'Temperature change' THEN 'temp_change' WHEN 'Cold crash' THEN 'cold_crash'
                 WHEN 'Transfer' THEN 'transfer' ELSE 'other' END,
       note
FROM annotations;
DROP TABLE annotations;
ALTER TABLE annotations_old RENAME TO annotations;
CREATE INDEX annotations_fermentation ON annotations(fermentation_id, at);
DROP TABLE annotation_types;
