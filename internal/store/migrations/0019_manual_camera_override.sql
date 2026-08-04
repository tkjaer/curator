ALTER TABLE items ADD COLUMN embedded_camera TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN manual_camera TEXT NOT NULL DEFAULT '';

UPDATE items SET embedded_camera = camera WHERE trim(embedded_camera) = '';