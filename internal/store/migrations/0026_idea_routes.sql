CREATE TABLE idea_routes (
    lodging_id TEXT NOT NULL REFERENCES lodging(id) ON DELETE CASCADE,
    item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    from_lat REAL NOT NULL,
    from_lng REAL NOT NULL,
    to_lat REAL NOT NULL,
    to_lng REAL NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('ready', 'unavailable')),
    distance_m REAL NOT NULL DEFAULT 0,
    duration_s REAL NOT NULL DEFAULT 0,
    geometry TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (lodging_id, item_id)
);
