UPDATE galleries SET show_exif = 2 WHERE show_exif = 0;

ALTER TABLE galleries ADD COLUMN show_title INTEGER NOT NULL DEFAULT 0;
ALTER TABLE galleries ADD COLUMN show_description INTEGER NOT NULL DEFAULT 0;

INSERT INTO settings (key, value) VALUES
    ('site.default_gallery_show_title', '"true"'),
    ('site.default_gallery_show_description', '"true"');
