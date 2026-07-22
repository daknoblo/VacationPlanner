-- Each item may reference a starting point ("origin") for the distance/time
-- shown on the activity cards. Empty = automatic (previous located stop that
-- day, or the day's lodging); "hotel" = the day's lodging/destination; a UUID
-- = another item on the same day. The reference is soft (no foreign key): a
-- stale reference simply falls back to the automatic origin.
ALTER TABLE items ADD COLUMN origin_ref TEXT NOT NULL DEFAULT '';
