CREATE TABLE activities (
    id           TEXT PRIMARY KEY,
    vacation_id  TEXT NOT NULL REFERENCES vacations(id) ON DELETE CASCADE,
    day          TEXT NOT NULL,
    title        TEXT NOT NULL,
    category     TEXT NOT NULL DEFAULT '',
    start_min    INTEGER NOT NULL DEFAULT 540,
    end_min      INTEGER NOT NULL DEFAULT 600,
    description  TEXT NOT NULL DEFAULT '',
    location     TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE INDEX idx_activities_vacation_day ON activities(vacation_id, day);
