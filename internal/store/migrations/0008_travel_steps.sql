-- Multi-stop arrival/departure: each kind (arrival/departure) can now hold an
-- ordered list of legs (e.g. car → ferry → car) instead of a single segment.
-- step_order keeps the legs in sequence; existing single segments default to 0.
ALTER TABLE travel_segments ADD COLUMN step_order INTEGER NOT NULL DEFAULT 0;
