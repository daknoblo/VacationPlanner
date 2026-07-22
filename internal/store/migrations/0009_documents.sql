-- Documents attached to a planned item (activity) or to a single travel leg
-- (arrival/departure step). The file bytes live in the database as a BLOB so
-- they are covered by the existing VACUUM INTO backups and the distroless image
-- stays self-contained. Exactly one owner is set per row (enforced by CHECK):
--   * item docs    -> item_id set
--   * travel docs  -> vacation_id + travel_kind + travel_step set
-- Travel docs are keyed by (vacation, kind, step_order) — matching the
-- step-order-based travel editor — rather than a fragile segment row id, so a
-- document can be attached to a leg that has not been saved yet.
CREATE TABLE documents (
    id            TEXT PRIMARY KEY,
    item_id       TEXT REFERENCES items(id) ON DELETE CASCADE,
    vacation_id   TEXT REFERENCES vacations(id) ON DELETE CASCADE,
    travel_kind   TEXT,
    travel_step   INTEGER,
    filename      TEXT NOT NULL,
    content_type  TEXT NOT NULL,
    size          INTEGER NOT NULL,
    data          BLOB NOT NULL,
    created_at    TEXT NOT NULL,
    CHECK (
        (item_id IS NOT NULL AND vacation_id IS NULL AND travel_kind IS NULL AND travel_step IS NULL)
        OR
        (item_id IS NULL AND vacation_id IS NOT NULL AND travel_kind IS NOT NULL AND travel_step IS NOT NULL)
    )
);

CREATE INDEX idx_documents_item ON documents(item_id);
CREATE INDEX idx_documents_travel ON documents(vacation_id, travel_kind, travel_step);
