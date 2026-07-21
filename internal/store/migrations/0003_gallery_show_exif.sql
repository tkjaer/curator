-- Per-gallery toggle for showing camera metadata on photos.
ALTER TABLE galleries ADD COLUMN show_exif INTEGER NOT NULL DEFAULT 0;
