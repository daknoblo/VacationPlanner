-- Persist the map zoom level chosen for a vacation's destination so the overview
-- map can frame country/region destinations correctly instead of always zooming
-- to a fixed street-level zoom. Nullable: existing rows fall back to a default.
ALTER TABLE vacations ADD COLUMN map_zoom INTEGER;
