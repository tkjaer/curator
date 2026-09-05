ALTER TABLE galleries
ADD COLUMN hero_gallery_id INTEGER REFERENCES galleries(id) ON DELETE SET NULL;
