-- Default settings and presets. Everything here is editable later in the admin.

INSERT INTO settings (key, value) VALUES
    ('site.title',       '"My Photos"'),
    ('site.theme',       '"default"'),
    ('site.base_url',    '""');

INSERT INTO derivative_presets (name, kind, max_width, max_height, quality) VALUES
    ('thumb',   'cover', 400,  400, 80),
    ('display', 'width', 1600, 0,   85),
    ('w800',    'width', 800,  0,   82),
    ('w1600',   'width', 1600, 0,   82),
    ('w2400',   'width', 2400, 0,   82);

INSERT INTO facet_config (namespace, enabled, source, label) VALUES
    ('camera', 0, 'Model',     'Camera'),
    ('lens',   0, 'LensModel', 'Lens');
