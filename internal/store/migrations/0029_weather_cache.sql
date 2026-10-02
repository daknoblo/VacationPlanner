CREATE TABLE weather_cache (
    key TEXT PRIMARY KEY,
    latitude REAL NOT NULL,
    longitude REAL NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'ready', 'error')),
    attempt TEXT NOT NULL,
    attempted_at TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    samples TEXT NOT NULL DEFAULT '[]'
);
