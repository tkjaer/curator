CREATE TABLE external_galleries (
    source TEXT NOT NULL,
    external_id TEXT NOT NULL,
    gallery_id INTEGER NOT NULL REFERENCES galleries(id) ON DELETE CASCADE,
    PRIMARY KEY (source, external_id),
    UNIQUE (source, gallery_id)
);

CREATE TABLE external_items (
    source TEXT NOT NULL,
    external_id TEXT NOT NULL,
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    PRIMARY KEY (source, external_id),
    UNIQUE (source, item_id)
);