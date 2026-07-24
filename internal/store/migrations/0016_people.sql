-- People are trip companions that costs can be attributed to. They are managed
-- globally (like categories) and selected per trip as participants.
CREATE TABLE people (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    color      TEXT NOT NULL DEFAULT '',
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);

CREATE UNIQUE INDEX idx_people_name ON people(name COLLATE NOCASE);

-- Which people take part in a given trip.
CREATE TABLE vacation_people (
    vacation_id TEXT NOT NULL REFERENCES vacations(id) ON DELETE CASCADE,
    person_id   TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    PRIMARY KEY (vacation_id, person_id)
);

CREATE INDEX idx_vacation_people_person ON vacation_people(person_id);

-- Who paid for each cost-bearing entity (NULL = unassigned). Deleting a person
-- clears the attribution instead of removing the expense.
ALTER TABLE items ADD COLUMN paid_by TEXT REFERENCES people(id) ON DELETE SET NULL;
ALTER TABLE lodging ADD COLUMN paid_by TEXT REFERENCES people(id) ON DELETE SET NULL;
ALTER TABLE travel_segments ADD COLUMN paid_by TEXT REFERENCES people(id) ON DELETE SET NULL;
