package store

import (
	"context"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/daknoblo/vacationplanner/internal/models"
)

// UpdateItemGeography fills an unchanged unlocated idea without rewriting its
// title, references, scheduling or financial fields. Manual regions stay intact.
func (s *SQLite) UpdateItemGeography(ctx context.Context, original *models.Item, vacation *models.Vacation, lat, lng float64, location, region string) (bool, error) {
	if original == nil || vacation == nil || original.VacationID != vacation.ID {
		return false, fmt.Errorf("store: missing or inconsistent item geography source")
	}
	if original.Latitude != nil || original.Longitude != nil {
		return false, nil
	}
	location, region = strings.TrimSpace(location), strings.TrimSpace(region)
	if math.IsNaN(lat) || math.IsNaN(lng) || math.IsInf(lat, 0) || math.IsInf(lng, 0) ||
		lat < -90 || lat > 90 || lng < -180 || lng > 180 ||
		!utf8.ValidString(location) || !utf8.ValidString(region) ||
		utf8.RuneCountInString(location) > 200 || utf8.RuneCountInString(region) > 200 {
		return false, fmt.Errorf("store: invalid item geography")
	}
	links, err := encodeItemLinks(original.Links)
	if err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE items SET latitude = ?, longitude = ?,
			location = CASE WHEN location = '' THEN ? ELSE location END,
			region = CASE WHEN region = '' AND region_manual = 0 THEN ? ELSE region END
		WHERE id = ? AND vacation_id = ? AND title = ? AND location = ?
		AND latitude IS NULL AND longitude IS NULL AND region = ? AND region_manual = ? AND links = ?
		AND EXISTS (SELECT 1 FROM vacations v WHERE v.id = items.vacation_id
			AND v.destination = ? AND v.latitude IS ? AND v.longitude IS ?)`,
		lat, lng, location, region, original.ID, original.VacationID, original.Title,
		original.Location, original.Region, original.RegionManual, links,
		vacation.Destination, vacation.Latitude, vacation.Longitude)
	if err != nil {
		return false, fmt.Errorf("store: enriching item geography: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// UpdateLodgingCoordinates enriches only an unchanged, unlocated lodging.
// Financial and booking fields deliberately do not participate in this update.
func (s *SQLite) UpdateLodgingCoordinates(ctx context.Context, original *models.Lodging, lat, lng float64) (bool, error) {
	if original == nil || original.Latitude != nil || original.Longitude != nil {
		return false, nil
	}
	if math.IsNaN(lat) || math.IsNaN(lng) || math.IsInf(lat, 0) || math.IsInf(lng, 0) || lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return false, fmt.Errorf("store: invalid lodging coordinates")
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE lodging SET latitude = ?, longitude = ?
		WHERE id = ? AND vacation_id = ? AND name = ? AND location = ?
		AND latitude IS NULL AND longitude IS NULL`,
		lat, lng, original.ID, original.VacationID, original.Name, original.Location)
	if err != nil {
		return false, fmt.Errorf("store: enriching lodging coordinates: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// UpdateLodgingRegion enriches only the unchanged geographic source, without
// writing booking fields that may have been edited while the lookup was running.
func (s *SQLite) UpdateLodgingRegion(ctx context.Context, original *models.Lodging, region string) (bool, error) {
	region = strings.TrimSpace(region)
	if original == nil || !original.HasCoords() || original.Region != "" || region == "" {
		return false, nil
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE lodging SET region = ?
		WHERE id = ? AND vacation_id = ? AND name = ? AND location = ?
		AND latitude IS ? AND longitude IS ? AND region = ?`,
		region, original.ID, original.VacationID, original.Name, original.Location,
		original.Latitude, original.Longitude, original.Region)
	if err != nil {
		return false, fmt.Errorf("store: enriching lodging region: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// UpdateItemRegion uses compare-and-swap so manual edits and moved items win.
func (s *SQLite) UpdateItemRegion(ctx context.Context, original *models.Item, region string) (bool, error) {
	region = strings.TrimSpace(region)
	if original == nil || !original.HasCoords() || original.RegionManual || original.Region != "" || region == "" {
		return false, nil
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE items SET region = ?
		WHERE id = ? AND vacation_id = ? AND title = ? AND location = ?
		AND latitude IS ? AND longitude IS ? AND region = ? AND region_manual = 0`,
		region, original.ID, original.VacationID, original.Title, original.Location,
		original.Latitude, original.Longitude, original.Region)
	if err != nil {
		return false, fmt.Errorf("store: enriching item region: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
