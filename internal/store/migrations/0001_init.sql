-- Core schema for Curator.

CREATE TABLE galleries (
    id              INTEGER PRIMARY KEY,
    parent_id       INTEGER REFERENCES galleries(id) ON DELETE CASCADE,
    slug            TEXT    NOT NULL,
    title           TEXT    NOT NULL,
    description     TEXT    NOT NULL DEFAULT '',
    type            TEXT    NOT NULL DEFAULT 'grid',
    status          TEXT    NOT NULL DEFAULT 'draft',
    -- No foreign key on cover_item_id: it would create a cycle with items,
    -- which reference galleries. Integrity is enforced in the application.
    cover_item_id   INTEGER,
    sort_mode       TEXT    NOT NULL DEFAULT 'date',
    theme           TEXT    NOT NULL DEFAULT '',
    password_realm  TEXT    NOT NULL DEFAULT '',
    sort_order      INTEGER NOT NULL DEFAULT 0,
    content_version INTEGER NOT NULL DEFAULT 0,
    published_at    TEXT,
    created_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    UNIQUE (parent_id, slug)
);

CREATE TABLE items (
    id              INTEGER PRIMARY KEY,
    gallery_id      INTEGER NOT NULL REFERENCES galleries(id) ON DELETE CASCADE,
    original_path   TEXT    NOT NULL,
    filename        TEXT    NOT NULL,
    width           INTEGER NOT NULL DEFAULT 0,
    height          INTEGER NOT NULL DEFAULT 0,
    aspect          TEXT    NOT NULL DEFAULT 'landscape',
    highlighted     INTEGER NOT NULL DEFAULT 0,
    sort_order      INTEGER NOT NULL DEFAULT 0,
    status          TEXT    NOT NULL DEFAULT 'draft',
    caption         TEXT    NOT NULL DEFAULT '',
    exif            TEXT    NOT NULL DEFAULT '',
    camera          TEXT    NOT NULL DEFAULT '',
    lens            TEXT    NOT NULL DEFAULT '',
    taken_at        TEXT,
    created_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_items_gallery  ON items (gallery_id);
CREATE INDEX idx_items_taken_at ON items (taken_at);
CREATE INDEX idx_items_camera   ON items (camera);
CREATE INDEX idx_items_lens     ON items (lens);

CREATE TABLE derivatives (
    id       INTEGER PRIMARY KEY,
    item_id  INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    preset   TEXT    NOT NULL,
    width    INTEGER NOT NULL DEFAULT 0,
    height   INTEGER NOT NULL DEFAULT 0,
    path     TEXT    NOT NULL,
    hash     TEXT    NOT NULL,
    UNIQUE (item_id, preset)
);

CREATE TABLE blocks (
    id          INTEGER PRIMARY KEY,
    gallery_id  INTEGER NOT NULL REFERENCES galleries(id) ON DELETE CASCADE,
    type        TEXT    NOT NULL,
    item_id     INTEGER REFERENCES items(id) ON DELETE SET NULL,
    content     TEXT    NOT NULL DEFAULT '',
    sort_order  INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_blocks_gallery ON blocks (gallery_id);

CREATE TABLE block_items (
    block_id   INTEGER NOT NULL REFERENCES blocks(id) ON DELETE CASCADE,
    item_id    INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    sort_order INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (block_id, item_id)
);

CREATE TABLE tags (
    id        INTEGER PRIMARY KEY,
    namespace TEXT NOT NULL,
    value     TEXT NOT NULL,
    UNIQUE (namespace, value)
);

CREATE TABLE tag_map (
    tag_id  INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    PRIMARY KEY (tag_id, item_id)
);

CREATE INDEX idx_tag_map_item ON tag_map (item_id);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE facet_config (
    namespace TEXT PRIMARY KEY,
    enabled   INTEGER NOT NULL DEFAULT 0,
    source    TEXT    NOT NULL DEFAULT '',
    label     TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE derivative_presets (
    name       TEXT PRIMARY KEY,
    kind       TEXT    NOT NULL DEFAULT 'width',
    max_width  INTEGER NOT NULL DEFAULT 0,
    max_height INTEGER NOT NULL DEFAULT 0,
    quality    INTEGER NOT NULL DEFAULT 82
);

CREATE TABLE build_ledger (
    path        TEXT PRIMARY KEY,
    source_hash TEXT NOT NULL,
    built_at    TEXT NOT NULL DEFAULT (datetime('now'))
);
