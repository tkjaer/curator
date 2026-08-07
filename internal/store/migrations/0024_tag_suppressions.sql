CREATE TABLE tag_suppressions (
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    source  TEXT    NOT NULL CHECK (source IN ('metadata')),
    value   TEXT    NOT NULL,
    PRIMARY KEY (item_id, source, value)
);

CREATE TRIGGER build_dirty_tag_suppressions_insert AFTER INSERT ON tag_suppressions BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_tag_suppressions_update AFTER UPDATE ON tag_suppressions BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_tag_suppressions_delete AFTER DELETE ON tag_suppressions BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;