CREATE TABLE cheatsheet_custom_phrases (
    vacation_id TEXT NOT NULL REFERENCES vacations(id) ON DELETE CASCADE,
    source_language TEXT NOT NULL,
    destination_key TEXT NOT NULL,
    target_language TEXT NOT NULL,
    original TEXT NOT NULL,
    translation TEXT NOT NULL,
    pronunciation TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (vacation_id, source_language, destination_key, target_language, original)
);
