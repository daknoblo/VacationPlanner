package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/models"
)

const descriptionSources = ` FROM items i JOIN vacations v ON v.id = i.vacation_id
	LEFT JOIN idea_descriptions d ON d.item_id = i.id
	WHERE trim(i.description) = ''`

const descriptionChanged = `(d.item_id IS NULL OR d.title <> i.title OR d.category <> i.category
	OR d.location <> i.location OR d.destination <> v.destination)`

// Reserve before calling AI, so failures and process interruption cannot replay paid calls.
func (s *SQLite) ClaimIdeaDescription(ctx context.Context) (*models.IdeaDescription, error) {
	job := &models.IdeaDescription{Attempt: uuid.NewString(), Status: "running"}
	err := s.db.QueryRowContext(ctx, `INSERT INTO idea_descriptions
		(item_id, title, category, location, destination, attempt, status)
		SELECT i.id, i.title, i.category, i.location, v.destination, ?, 'running'`+
		descriptionSources+` AND `+descriptionChanged+` AND COALESCE(d.status, '') <> 'running'
		ORDER BY i.created_at, i.id LIMIT 1
		ON CONFLICT(item_id) DO UPDATE SET title = excluded.title, category = excluded.category,
		location = excluded.location, destination = excluded.destination, attempt = excluded.attempt,
		status = 'running', english = '', german = ''
		RETURNING item_id, title, category, location, destination`, job.Attempt).
		Scan(&job.ItemID, &job.Title, &job.Category, &job.Location, &job.Destination)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return job, nil
}

func (s *SQLite) FinishIdeaDescription(ctx context.Context, job *models.IdeaDescription) error {
	if job.Status != "ready" && job.Status != "unavailable" {
		return fmt.Errorf("store: invalid description status")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE idea_descriptions SET status = ?, english = ?, german = ?
		WHERE item_id = ? AND attempt = ? AND status = 'running'`,
		job.Status, job.English, job.German, job.ItemID, job.Attempt)
	return err
}

func (s *SQLite) InterruptIdeaDescriptions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE idea_descriptions SET status = 'unavailable' WHERE status = 'running'`)
	return err
}

func (s *SQLite) ListIdeaDescriptions(ctx context.Context, vacationID uuid.UUID) (map[uuid.UUID]models.IdeaDescription, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT i.id, d.status, d.english, d.german`+
		descriptionSources+` AND i.vacation_id = ? AND NOT `+descriptionChanged, vacationID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make(map[uuid.UUID]models.IdeaDescription)
	for rows.Next() {
		var value models.IdeaDescription
		if err := rows.Scan(&value.ItemID, &value.Status, &value.English, &value.German); err != nil {
			return nil, err
		}
		result[value.ItemID] = value
	}
	return result, rows.Err()
}

func (s *SQLite) CountPendingIdeaDescriptions(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT count(*)`+descriptionSources+
		` AND (`+descriptionChanged+` OR d.status = 'running')`).Scan(&count)
	return count, err
}
