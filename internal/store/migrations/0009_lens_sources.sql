ALTER TABLE items ADD COLUMN embedded_lens TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN xmp_lens TEXT NOT NULL DEFAULT '';

UPDATE items
SET embedded_lens = COALESCE(json_extract(exif, '$.LensModel'), '')
WHERE json_valid(exif);