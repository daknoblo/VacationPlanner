-- Optional per-leg cost for arrival/departure travel segments so flights,
-- trains, ferries etc. count toward the trip budget like items and lodging.
ALTER TABLE travel_segments ADD COLUMN cost REAL;
