INSERT INTO settings (key, value) VALUES
    ('metadata.tag_visibility', '"show_all"'),
    ('metadata.tag_selection',  '""');

INSERT INTO facet_config (namespace, enabled, source, label)
VALUES ('tag', 0, 'user', 'Tags');

INSERT OR IGNORE INTO tag_map (tag_id, item_id)
SELECT canonical.id, tag_map.item_id
    FROM tags AS duplicate
    JOIN tag_map ON tag_map.tag_id = duplicate.id
    JOIN tags AS canonical
        ON canonical.namespace = 'user'
     AND lower(trim(replace(replace(replace(replace(canonical.value, '-', ' '), '  ', ' '), '  ', ' '), '  ', ' '))) =
             lower(trim(replace(replace(replace(replace(duplicate.value, '-', ' '), '  ', ' '), '  ', ' '), '  ', ' ')))
     AND canonical.id = (
             SELECT min(candidate.id)
                 FROM tags AS candidate
                WHERE candidate.namespace = 'user'
                    AND lower(trim(replace(replace(replace(replace(candidate.value, '-', ' '), '  ', ' '), '  ', ' '), '  ', ' '))) =
                            lower(trim(replace(replace(replace(replace(duplicate.value, '-', ' '), '  ', ' '), '  ', ' '), '  ', ' ')))
     )
 WHERE duplicate.namespace = 'user';

DELETE FROM tag_map
 WHERE tag_id IN (
         SELECT duplicate.id
             FROM tags AS duplicate
            WHERE duplicate.namespace = 'user'
                AND duplicate.id != (
                        SELECT min(candidate.id)
                            FROM tags AS candidate
                         WHERE candidate.namespace = 'user'
                             AND lower(trim(replace(replace(replace(replace(candidate.value, '-', ' '), '  ', ' '), '  ', ' '), '  ', ' '))) =
                                     lower(trim(replace(replace(replace(replace(duplicate.value, '-', ' '), '  ', ' '), '  ', ' '), '  ', ' ')))
                )
 );

DELETE FROM tags
 WHERE namespace = 'user'
     AND id != (
             SELECT min(candidate.id)
                 FROM tags AS candidate
                WHERE candidate.namespace = 'user'
                    AND lower(trim(replace(replace(replace(replace(candidate.value, '-', ' '), '  ', ' '), '  ', ' '), '  ', ' '))) =
                            lower(trim(replace(replace(replace(replace(tags.value, '-', ' '), '  ', ' '), '  ', ' '), '  ', ' ')))
     );

UPDATE tags
     SET value = lower(trim(replace(replace(replace(replace(value, '-', ' '), '  ', ' '), '  ', ' '), '  ', ' ')))
 WHERE namespace = 'user';

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