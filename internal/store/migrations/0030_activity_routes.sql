CREATE TABLE activity_routes (
    item_id TEXT PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    from_lat REAL NOT NULL,
    from_lng REAL NOT NULL,
    to_lat REAL NOT NULL,
    to_lng REAL NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('ready', 'unavailable')),
    distance_m REAL NOT NULL DEFAULT 0 CHECK (distance_m >= 0),
    duration_s REAL NOT NULL DEFAULT 0 CHECK (duration_s >= 0)
);