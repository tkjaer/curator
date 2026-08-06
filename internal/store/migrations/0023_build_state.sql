-- Versions 21 and 22 were used by pre-merge tag migrations. Track public-output
-- inputs without scanning the full library before a build.

CREATE TABLE build_revision (
    id       INTEGER PRIMARY KEY CHECK (id = 1),
    revision INTEGER NOT NULL DEFAULT 1
);

INSERT INTO build_revision (id, revision) VALUES (1, 1);

CREATE TABLE build_state (
    output_dir       TEXT PRIMARY KEY,
    content_revision INTEGER NOT NULL,
    fingerprint      TEXT NOT NULL,
    galleries        INTEGER NOT NULL DEFAULT 0,
    photos           INTEGER NOT NULL DEFAULT 0,
    built_at         TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TRIGGER build_dirty_galleries_insert AFTER INSERT ON galleries BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_galleries_update AFTER UPDATE ON galleries BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_galleries_delete AFTER DELETE ON galleries BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;

CREATE TRIGGER build_dirty_items_insert AFTER INSERT ON items BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_items_update AFTER UPDATE ON items BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_items_delete AFTER DELETE ON items BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;

CREATE TRIGGER build_dirty_blocks_insert AFTER INSERT ON blocks BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_blocks_update AFTER UPDATE ON blocks BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_blocks_delete AFTER DELETE ON blocks BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;

CREATE TRIGGER build_dirty_block_items_insert AFTER INSERT ON block_items BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_block_items_update AFTER UPDATE ON block_items BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_block_items_delete AFTER DELETE ON block_items BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;

CREATE TRIGGER build_dirty_tags_insert AFTER INSERT ON tags BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_tags_update AFTER UPDATE ON tags BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_tags_delete AFTER DELETE ON tags BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;

CREATE TRIGGER build_dirty_tag_map_insert AFTER INSERT ON tag_map BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_tag_map_update AFTER UPDATE ON tag_map BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_tag_map_delete AFTER DELETE ON tag_map BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;

CREATE TRIGGER build_dirty_settings_insert AFTER INSERT ON settings WHEN NEW.key NOT LIKE 'publish.%' BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_settings_update AFTER UPDATE ON settings WHEN OLD.key NOT LIKE 'publish.%' OR NEW.key NOT LIKE 'publish.%' BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_settings_delete AFTER DELETE ON settings WHEN OLD.key NOT LIKE 'publish.%' BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;

CREATE TRIGGER build_dirty_facets_insert AFTER INSERT ON facet_config BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_facets_update AFTER UPDATE ON facet_config BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_facets_delete AFTER DELETE ON facet_config BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;

CREATE TRIGGER build_dirty_presets_insert AFTER INSERT ON derivative_presets BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_presets_update AFTER UPDATE ON derivative_presets BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_presets_delete AFTER DELETE ON derivative_presets BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;

CREATE TRIGGER build_dirty_access_users_insert AFTER INSERT ON access_users BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_access_users_update AFTER UPDATE ON access_users BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_access_users_delete AFTER DELETE ON access_users BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;

CREATE TRIGGER build_dirty_gallery_access_insert AFTER INSERT ON gallery_access BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_gallery_access_update AFTER UPDATE ON gallery_access BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
CREATE TRIGGER build_dirty_gallery_access_delete AFTER DELETE ON gallery_access BEGIN UPDATE build_revision SET revision = revision + 1 WHERE id = 1; END;
