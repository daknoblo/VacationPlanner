-- Add lodging (accommodation) as a third document owner so booking PDFs and the
-- like can be attached to a stay, mirroring item and travel-leg documents.
-- SQLite cannot alter a table-level CHECK constraint in place, so the documents
-- table is rebuilt. No other table references documents, so this is safe with
-- foreign_keys enabled.
ALTER TABLE documents RENAME TO documents_old;

CREATE TABLE documents (
    id            TEXT PRIMARY KEY,
    item_id       TEXT REFERENCES items(id) ON DELETE CASCADE,
    vacation_id   TEXT REFERENCES vacations(id) ON DELETE CASCADE,
    travel_kind   TEXT,
    travel_step   INTEGER,
    lodging_id    TEXT REFERENCES lodging(id) ON DELETE CASCADE,
    filename      TEXT NOT NULL,
    content_type  TEXT NOT NULL,
    size          INTEGER NOT NULL,
    data          BLOB NOT NULL,
    created_at    TEXT NOT NULL,
    CHECK (
        (item_id IS NOT NULL AND vacation_id IS NULL AND travel_kind IS NULL AND travel_step IS NULL AND lodging_id IS NULL)
        OR
        (item_id IS NULL AND vacation_id IS NOT NULL AND travel_kind IS NOT NULL AND travel_step IS NOT NULL AND lodging_id IS NULL)
        OR
        (item_id IS NULL AND vacation_id IS NULL AND travel_kind IS NULL AND travel_step IS NULL AND lodging_id IS NOT NULL)
    )
);

INSERT INTO documents
    (id, item_id, vacation_id, travel_kind, travel_step, lodging_id, filename, content_type, size, data, created_at)
SELECT
    id, item_id, vacation_id, travel_kind, travel_step, NULL, filename, content_type, size, data, created_at
FROM documents_old;

DROP TABLE documents_old;

CREATE INDEX idx_documents_item ON documents(item_id);
CREATE INDEX idx_documents_travel ON documents(vacation_id, travel_kind, travel_step);
CREATE INDEX idx_documents_lodging ON documents(lodging_id);
