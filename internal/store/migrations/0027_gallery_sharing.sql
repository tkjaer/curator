ALTER TABLE galleries ADD COLUMN show_sharing INTEGER NOT NULL DEFAULT 0;

INSERT INTO settings (key, value) VALUES
    ('site.default_gallery_show_sharing', '"false"');