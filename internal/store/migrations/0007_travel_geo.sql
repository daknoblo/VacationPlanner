-- Add geocoded endpoints and computed distance/duration to travel segments so
-- the arrival/departure editor can display travel time and distance.
ALTER TABLE travel_segments ADD COLUMN from_lat REAL;
ALTER TABLE travel_segments ADD COLUMN from_lng REAL;
ALTER TABLE travel_segments ADD COLUMN to_lat REAL;
ALTER TABLE travel_segments ADD COLUMN to_lng REAL;
ALTER TABLE travel_segments ADD COLUMN distance_m REAL;
ALTER TABLE travel_segments ADD COLUMN duration_s INTEGER;
