-- Accommodations gain a cost (counted toward the trip budget) and coordinates
-- so they can be shown as a marker on the overview map (a base for day trips).
ALTER TABLE lodging ADD COLUMN latitude REAL;
ALTER TABLE lodging ADD COLUMN longitude REAL;
ALTER TABLE lodging ADD COLUMN cost REAL;
