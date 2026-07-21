-- Presentation-focused EXIF fields, extracted once at ingest.
ALTER TABLE items ADD COLUMN aperture TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN shutter  TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN iso      TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN focal    TEXT NOT NULL DEFAULT '';
