CREATE TABLE items (
    id           TEXT PRIMARY KEY,
    vacation_id  TEXT NOT NULL REFERENCES vacations(id) ON DELETE CASCADE,
    category     TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    location     TEXT NOT NULL DEFAULT '',
    latitude     REAL,
    longitude    REAL,
    day          TEXT,
    start_min    INTEGER NOT NULL DEFAULT 0,
    end_min      INTEGER NOT NULL DEFAULT 0,
    cost         REAL,
    visited      INTEGER NOT NULL DEFAULT 0,
    notes        TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE INDEX idx_items_vacation_day ON items(vacation_id, day, start_min);

-- Migrate existing sights (points of interest; no time range) into items.
INSERT INTO items
    (id, vacation_id, category, title, description, location, latitude, longitude,
     day, start_min, end_min, cost, visited, notes, created_at, updated_at)
SELECT id, vacation_id, category, name, description, '', latitude, longitude,
       planned_date, 0, 0, NULL, visited, notes, created_at, created_at
FROM sights;

-- Migrate existing activities (timed entries) into items.
INSERT INTO items
    (id, vacation_id, category, title, description, location, latitude, longitude,
     day, start_min, end_min, cost, visited, notes, created_at, updated_at)
SELECT id, vacation_id, category, title, description, location, NULL, NULL,
       day, start_min, end_min, NULL, 0, '', created_at, updated_at
FROM activities;

DROP TABLE activities;
DROP TABLE sights;
