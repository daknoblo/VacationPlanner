-- Accommodations for a trip. Each lodging spans a check-in to a check-out
-- date/time (stored as UTC timestamps) and is shown as a narrow strip on the
-- day/week planner over the hours it covers.
CREATE TABLE lodging (
    id           TEXT PRIMARY KEY,
    vacation_id  TEXT NOT NULL REFERENCES vacations(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    location     TEXT NOT NULL DEFAULT '',
    check_in     TEXT NOT NULL,
    check_out    TEXT NOT NULL,
    notes        TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE INDEX idx_lodging_vacation ON lodging(vacation_id, check_in);
