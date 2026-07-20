CREATE TABLE IF NOT EXISTS vacations (
    id          UUID PRIMARY KEY,
    title       TEXT        NOT NULL,
    destination TEXT        NOT NULL,
    start_date  DATE        NOT NULL,
    end_date    DATE        NOT NULL,
    latitude    DOUBLE PRECISION,
    longitude   DOUBLE PRECISION,
    notes       TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT vacations_dates_ck CHECK (end_date >= start_date)
);

CREATE TABLE IF NOT EXISTS travel_segments (
    id            UUID PRIMARY KEY,
    vacation_id   UUID        NOT NULL REFERENCES vacations (id) ON DELETE CASCADE,
    kind          TEXT        NOT NULL CHECK (kind IN ('arrival', 'departure')),
    mode          TEXT        NOT NULL DEFAULT '',
    from_location TEXT        NOT NULL DEFAULT '',
    to_location   TEXT        NOT NULL DEFAULT '',
    depart_at     TIMESTAMPTZ,
    arrive_at     TIMESTAMPTZ,
    notes         TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_travel_segments_vacation ON travel_segments (vacation_id);

CREATE TABLE IF NOT EXISTS sights (
    id           UUID PRIMARY KEY,
    vacation_id  UUID        NOT NULL REFERENCES vacations (id) ON DELETE CASCADE,
    name         TEXT        NOT NULL,
    category     TEXT        NOT NULL DEFAULT '',
    description  TEXT        NOT NULL DEFAULT '',
    latitude     DOUBLE PRECISION,
    longitude    DOUBLE PRECISION,
    planned_date DATE,
    visited      BOOLEAN     NOT NULL DEFAULT FALSE,
    notes        TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_sights_vacation ON sights (vacation_id);
