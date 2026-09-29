CREATE TABLE idea_descriptions (
    item_id TEXT PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    category TEXT NOT NULL,
    location TEXT NOT NULL,
    destination TEXT NOT NULL,
    attempt TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('running', 'ready', 'unavailable')),
    english TEXT NOT NULL DEFAULT '',
    german TEXT NOT NULL DEFAULT ''
);
