INSERT INTO settings (key, value) VALUES
    ('metadata.tag_visibility', '"show_all"'),
    ('metadata.tag_selection',  '""');

INSERT INTO facet_config (namespace, enabled, source, label)
VALUES ('tag', 0, 'user', 'Tags');