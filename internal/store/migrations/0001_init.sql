CREATE TABLE IF NOT EXISTS vacations (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    destination TEXT NOT NULL,
    start_date  TEXT NOT NULL,
    end_date    TEXT NOT NULL,
    latitude    REAL,
    longitude   REAL,
    notes       TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    CONSTRAINT vacations_dates_ck CHECK (end_date >= start_date)
);

CREATE TABLE IF NOT EXISTS travel_segments (
    id            TEXT PRIMARY KEY,
    vacation_id   TEXT NOT NULL REFERENCES vacations (id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('arrival', 'departure')),
    mode          TEXT NOT NULL DEFAULT '',
    from_location TEXT NOT NULL DEFAULT '',
    to_location   TEXT NOT NULL DEFAULT '',
    depart_at     TEXT,
    arrive_at     TEXT,
    notes         TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_travel_segments_vacation ON travel_segments (vacation_id);

CREATE TABLE IF NOT EXISTS sights (
    id           TEXT PRIMARY KEY,
    vacation_id  TEXT NOT NULL REFERENCES vacations (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    category     TEXT NOT NULL DEFAULT '',
    description  TEXT NOT NULL DEFAULT '',
    latitude     REAL,
    longitude    REAL,
    planned_date TEXT,
    visited      INTEGER NOT NULL DEFAULT 0,
    notes        TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sights_vacation ON sights (vacation_id);
