CREATE TABLE item_location_suggestions (
    item_id TEXT PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    source_key TEXT NOT NULL,
    title TEXT NOT NULL,
    category TEXT NOT NULL,
    location TEXT NOT NULL,
    region TEXT NOT NULL,
    region_manual INTEGER NOT NULL,
    links TEXT NOT NULL,
    destination TEXT NOT NULL,
    destination_lat REAL,
    destination_lng REAL,
    label TEXT NOT NULL,
    name TEXT NOT NULL,
    latitude REAL NOT NULL,
    longitude REAL NOT NULL,
    rejected INTEGER NOT NULL DEFAULT 0 CHECK (rejected IN (0, 1))
);
