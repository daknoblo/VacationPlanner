CREATE TABLE cheatsheets (
    vacation_id TEXT NOT NULL REFERENCES vacations(id) ON DELETE CASCADE,
    source_language TEXT NOT NULL,
    destination_key TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (vacation_id, source_language)
);
