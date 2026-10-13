package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func (s *SQLite) GetActivityRoute(ctx context.Context, source *models.ActivityRoute) (*models.ActivityRoute, error) {
	result := *source
	err := s.db.QueryRowContext(ctx, `WITH current_item AS (
		SELECT id FROM items WHERE id = ? AND vacation_id = ? AND day IS NOT NULL
		AND latitude = ? AND longitude = ?
	), saved AS (
		SELECT r.status, r.distance_m, r.duration_s FROM activity_routes r
		JOIN current_item i ON i.id = r.item_id
		WHERE r.provider = ? AND r.from_lat = ? AND r.from_lng = ? AND r.to_lat = ? AND r.to_lng = ?
		UNION ALL
		SELECT r.status, r.distance_m, r.duration_s FROM idea_routes r
		JOIN current_item i ON i.id = r.item_id JOIN lodging l ON l.id = r.lodging_id
		WHERE r.provider = ? AND r.from_lat = ? AND r.from_lng = ? AND r.to_lat = ? AND r.to_lng = ?
		AND l.latitude = r.from_lat AND l.longitude = r.from_lng
	) SELECT status, distance_m, duration_s FROM saved ORDER BY status = 'ready' DESC LIMIT 1`,
		source.ItemID, source.VacationID, source.ToLat, source.ToLng,
		source.Provider, source.FromLat, source.FromLng, source.ToLat, source.ToLng,
		source.Provider, source.FromLat, source.FromLng, source.ToLat, source.ToLng).
		Scan(&result.Status, &result.DistanceM, &result.DurationS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *SQLite) PutActivityRoute(ctx context.Context, job *models.ActivityRoute) (bool, error) {
	if job == nil || (job.Status != "ready" && job.Status != "unavailable") {
		return false, fmt.Errorf("store: invalid activity route result")
	}
	for _, value := range []float64{job.DistanceM, job.DurationS} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return false, fmt.Errorf("store: invalid activity route metrics")
		}
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO activity_routes
		(item_id, provider, from_lat, from_lng, to_lat, to_lng, status, distance_m, duration_s)
		SELECT id, ?, ?, ?, latitude, longitude, ?, ?, ? FROM items
		WHERE id = ? AND vacation_id = ? AND day IS NOT NULL AND latitude = ? AND longitude = ?
		AND COALESCE((SELECT value FROM settings WHERE key = 'route.base_url'), '') = ?
		ON CONFLICT(item_id) DO UPDATE SET provider = excluded.provider,
			from_lat = excluded.from_lat, from_lng = excluded.from_lng,
			to_lat = excluded.to_lat, to_lng = excluded.to_lng, status = excluded.status,
			distance_m = excluded.distance_m, duration_s = excluded.duration_s`,
		job.Provider, job.FromLat, job.FromLng, job.Status, job.DistanceM, job.DurationS,
		job.ItemID, job.VacationID, job.ToLat, job.ToLng, job.Provider)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count != 0, err
}
