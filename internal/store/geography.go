package store

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/daknoblo/vacationplanner/internal/models"
)

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
