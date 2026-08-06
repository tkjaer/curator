CREATE TABLE tag_map_new (
    tag_id  INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    source  TEXT    NOT NULL DEFAULT 'manual',
    PRIMARY KEY (tag_id, item_id, source)
);

INSERT INTO tag_map_new (tag_id, item_id, source)
SELECT tag_id, item_id, 'manual' FROM tag_map;

DROP TABLE tag_map;
ALTER TABLE tag_map_new RENAME TO tag_map;
CREATE INDEX idx_tag_map_item ON tag_map (item_id);