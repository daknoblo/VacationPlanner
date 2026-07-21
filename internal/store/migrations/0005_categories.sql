CREATE TABLE categories (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    icon       TEXT NOT NULL DEFAULT '',
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);

CREATE UNIQUE INDEX idx_categories_name ON categories(name COLLATE NOCASE);

INSERT INTO categories (id, name, icon, sort_order, created_at) VALUES
    ('11111111-1111-4111-8111-111111111101', 'Activity', '🎯', 1, '2026-07-21T00:00:00.000000000Z'),
    ('11111111-1111-4111-8111-111111111102', 'Food', '🍽️', 2, '2026-07-21T00:00:00.000000000Z'),
    ('11111111-1111-4111-8111-111111111103', 'Point of Interest', '📍', 3, '2026-07-21T00:00:00.000000000Z');
