-- Per-gallery access control for protected galleries.

CREATE TABLE access_users (
    id         INTEGER PRIMARY KEY,
    username   TEXT NOT NULL UNIQUE,
    hash       TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE gallery_access (
    gallery_id INTEGER NOT NULL REFERENCES galleries(id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL REFERENCES access_users(id) ON DELETE CASCADE,
    PRIMARY KEY (gallery_id, user_id)
);

INSERT INTO settings (key, value) VALUES
    ('site.webserver',   '"nginx"'),
    ('site.server_root', '""');
