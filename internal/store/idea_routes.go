package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/models"
)

// Uncached current coordinate pairs form the durable work queue. This avoids
// materializing an unbounded cross-product or losing work when a process stops.
const ideaRouteSources = `
	FROM lodging l JOIN items i ON i.vacation_id = l.vacation_id
	LEFT JOIN idea_routes r ON r.lodging_id = l.id AND r.item_id = i.id
		AND r.from_lat = l.latitude AND r.from_lng = l.longitude
		AND r.to_lat = i.latitude AND r.to_lng = i.longitude AND r.provider = ?
	WHERE l.latitude BETWEEN -90 AND 90 AND l.longitude BETWEEN -180 AND 180
		AND i.latitude BETWEEN -90 AND 90 AND i.longitude BETWEEN -180 AND 180`

func (s *SQLite) NextIdeaRoute(ctx context.Context, provider string) (*models.IdeaRoute, error) {
	job := &models.IdeaRoute{Provider: provider}
	err := s.db.QueryRowContext(ctx, `SELECT l.vacation_id, l.id, i.id,
		l.latitude, l.longitude, i.latitude, i.longitude`+ideaRouteSources+`
		AND r.item_id IS NULL ORDER BY i.created_at, i.id, l.id LIMIT 1`, provider).
		Scan(&job.VacationID, &job.LodgingID, &job.ItemID, &job.FromLat, &job.FromLng, &job.ToLat, &job.ToLng)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return job, nil
}

func (s *SQLite) IdeaRouteProgress(ctx context.Context, provider string, vacationID uuid.UUID) (models.IdeaRouteProgress, error) {
	var progress models.IdeaRouteProgress
	query := `SELECT COUNT(*), COUNT(r.item_id), COALESCE(SUM(r.status = 'unavailable'), 0)` + ideaRouteSources
	args := []any{provider}
	if vacationID != uuid.Nil {
		query += ` AND l.vacation_id = ?`
		args = append(args, vacationID)
	}
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&progress.Total, &progress.Completed, &progress.Failed)
	return progress, err
}

func (s *SQLite) ListIdeaRoutes(ctx context.Context, provider string, vacationID, lodgingID uuid.UUID) ([]models.IdeaRoute, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT i.id, r.status, r.distance_m, r.duration_s, r.geometry`+
		ideaRouteSources+` AND l.vacation_id = ? AND l.id = ? AND r.item_id IS NOT NULL`, provider, vacationID, lodgingID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []models.IdeaRoute
	for rows.Next() {
		value := models.IdeaRoute{VacationID: vacationID, LodgingID: lodgingID, Provider: provider}
		var geometry string
		if err := rows.Scan(&value.ItemID, &value.Status, &value.DistanceM, &value.DurationS, &geometry); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(geometry), &value.Geometry); err != nil {
			return nil, fmt.Errorf("store: invalid saved route geometry: %w", err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

// Provider and coordinate comparisons prevent an in-flight route from reviving
// deleted records or overwriting results for a subsequently edited location.
func (s *SQLite) PutIdeaRoute(ctx context.Context, job *models.IdeaRoute) (bool, error) {
	if job == nil || (job.Status != "ready" && job.Status != "unavailable") {
		return false, fmt.Errorf("store: invalid idea route result")
	}
	geometry, err := json.Marshal(job.Geometry)
	if err != nil {
		return false, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO idea_routes
		(lodging_id, item_id, provider, from_lat, from_lng, to_lat, to_lng, status, distance_m, duration_s, geometry)
		SELECT l.id, i.id, ?, l.latitude, l.longitude, i.latitude, i.longitude, ?, ?, ?, ?
		FROM lodging l JOIN items i ON i.vacation_id = l.vacation_id
		WHERE l.id = ? AND i.id = ? AND l.vacation_id = ?
		AND l.latitude = ? AND l.longitude = ? AND i.latitude = ? AND i.longitude = ?
		AND COALESCE((SELECT value FROM settings WHERE key = 'route.base_url'), '') = ?
		ON CONFLICT(lodging_id, item_id) DO UPDATE SET provider = excluded.provider,
			from_lat = excluded.from_lat, from_lng = excluded.from_lng,
			to_lat = excluded.to_lat, to_lng = excluded.to_lng, status = excluded.status,
			distance_m = excluded.distance_m, duration_s = excluded.duration_s, geometry = excluded.geometry`,
		job.Provider, job.Status, job.DistanceM, job.DurationS, string(geometry),
		job.LodgingID, job.ItemID, job.VacationID, job.FromLat, job.FromLng, job.ToLat, job.ToLng, job.Provider)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count != 0, err
}

func (s *SQLite) RetryIdeaRoutes(ctx context.Context, vacationID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM idea_routes WHERE (status = 'unavailable' OR COALESCE(json_array_length(geometry), 0) < 2)
		AND lodging_id IN (SELECT id FROM lodging WHERE vacation_id = ?)`, vacationID)
	return err
}
