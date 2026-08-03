ALTER TABLE galleries ADD COLUMN sort_direction TEXT NOT NULL DEFAULT 'default';

INSERT INTO settings (key, value) VALUES
    ('site.default_gallery_sort_direction', '"asc"');
