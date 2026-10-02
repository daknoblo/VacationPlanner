package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func (s *SQLite) ListWeather(ctx context.Context) (map[string]models.WeatherCache, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, latitude, longitude, status, attempt,
		attempted_at, updated_at, error_code, samples FROM weather_cache`)
	if err != nil {
		return nil, fmt.Errorf("store: reading weather: %w", err)
	}
	defer func() { _ = rows.Close() }()
	results := make(map[string]models.WeatherCache)
	for rows.Next() {
		var c models.WeatherCache
		var attempted, updated, samples string
		if err := rows.Scan(&c.Key, &c.Latitude, &c.Longitude, &c.Status, &c.Attempt,
			&attempted, &updated, &c.ErrorCode, &samples); err != nil {
			return nil, err
		}
		c.AttemptedAt, err = time.Parse(dbTimeLayout, attempted)
		if err != nil {
			return nil, fmt.Errorf("store: invalid weather attempt time: %w", err)
		}
		if updated != "" {
			c.UpdatedAt, err = time.Parse(dbTimeLayout, updated)
			if err != nil {
				return nil, fmt.Errorf("store: invalid weather update time: %w", err)
			}
		}
		if err := json.Unmarshal([]byte(samples), &c.Samples); err != nil {
			return nil, fmt.Errorf("store: invalid weather samples: %w", err)
		}
		results[c.Key] = c
	}
	return results, rows.Err()
}

// QueueWeather retains successful samples, throttles duplicate clicks and bounds
// the durable queue. before is the last permitted attempt for this refresh.
func (s *SQLite) QueueWeather(ctx context.Context, c models.WeatherCache, before, now time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO weather_cache
		(key, latitude, longitude, status, attempt, attempted_at)
		SELECT ?, ?, ?, 'queued', ?, ?
		WHERE (SELECT COUNT(*) FROM weather_cache WHERE status IN ('queued','running')) < 100
		ON CONFLICT(key) DO UPDATE SET status='queued', attempt=excluded.attempt,
			attempted_at=excluded.attempted_at, error_code=''
		WHERE weather_cache.status NOT IN ('queued','running') AND weather_cache.attempted_at <= ?`,
		c.Key, c.Latitude, c.Longitude, uuid.NewString(), dbTime(now), dbTime(before))
	if err != nil {
		return false, fmt.Errorf("store: queueing weather: %w", err)
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (s *SQLite) ClaimWeather(ctx context.Context) (string, error) {
	var key string
	err := s.db.QueryRowContext(ctx, `UPDATE weather_cache SET status='running'
		WHERE key=(SELECT key FROM weather_cache WHERE status='queued' ORDER BY attempted_at, key LIMIT 1)
		RETURNING key`).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return key, err
}

func (s *SQLite) FinishWeather(ctx context.Context, c models.WeatherCache) error {
	samples, err := json.Marshal(c.Samples)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE weather_cache SET status=?, error_code=?,
		samples=CASE WHEN ?='ready' THEN ? ELSE samples END,
		updated_at=CASE WHEN ?='ready' THEN ? ELSE updated_at END
		WHERE key=? AND attempt=? AND status='running'`,
		c.Status, c.ErrorCode, c.Status, string(samples), c.Status, dbTime(c.UpdatedAt), c.Key, c.Attempt)
	return err
}

func (s *SQLite) InterruptWeather(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE weather_cache SET status='error', error_code='interrupted' WHERE status='running'`)
	return err
}
