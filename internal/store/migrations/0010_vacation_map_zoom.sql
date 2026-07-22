-- Persist the preferred initial zoom for the overview map, captured from the
-- geocoder result type when a destination is picked (e.g. a country zooms out
-- further than a city). NULL falls back to a sensible default in the client.
ALTER TABLE vacations ADD COLUMN map_zoom INTEGER;
