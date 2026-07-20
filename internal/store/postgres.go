package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/daknoblo/vacationplanner/internal/models"
)

// Postgres is a PostgreSQL-backed implementation of Store.
type Postgres struct {
	pool *pgxpool.Pool
}

// Compile-time assertion that Postgres satisfies Store.
var _ Store = (*Postgres)(nil)

// NewPostgres opens a connection pool and verifies connectivity.
func NewPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("store: parsing DATABASE_URL: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute
	cfg.HealthCheckPeriod = time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: creating pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: pinging database: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

// Ping verifies the database connection is alive.
func (p *Postgres) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

// Close releases all pooled connections.
func (p *Postgres) Close() {
	p.pool.Close()
}

// ---- Vacations ----

func (p *Postgres) CreateVacation(ctx context.Context, v *models.Vacation) error {
	if v.ID == uuid.Nil {
		v.ID = uuid.New()
	}
	now := time.Now().UTC()
	v.CreatedAt, v.UpdatedAt = now, now

	_, err := p.pool.Exec(ctx, `
		INSERT INTO vacations
			(id, title, destination, start_date, end_date, latitude, longitude, notes, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		v.ID, v.Title, v.Destination, v.StartDate, v.EndDate,
		v.Latitude, v.Longitude, v.Notes, v.CreatedAt, v.UpdatedAt)
	if err != nil {
		return fmt.Errorf("store: creating vacation: %w", err)
	}
	return nil
}

func (p *Postgres) GetVacation(ctx context.Context, id uuid.UUID) (*models.Vacation, error) {
	row := p.pool.QueryRow(ctx, `
		SELECT id, title, destination, start_date, end_date, latitude, longitude, notes, created_at, updated_at
		FROM vacations WHERE id = $1`, id)

	var v models.Vacation
	if err := scanVacation(row, &v); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: getting vacation: %w", err)
	}
	return &v, nil
}

func (p *Postgres) ListVacations(ctx context.Context) ([]models.Vacation, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, title, destination, start_date, end_date, latitude, longitude, notes, created_at, updated_at
		FROM vacations ORDER BY start_date ASC, created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: listing vacations: %w", err)
	}
	defer rows.Close()

	var out []models.Vacation
	for rows.Next() {
		var v models.Vacation
		if err := scanVacation(rows, &v); err != nil {
			return nil, fmt.Errorf("store: scanning vacation: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating vacations: %w", err)
	}
	return out, nil
}

func (p *Postgres) UpdateVacation(ctx context.Context, v *models.Vacation) error {
	v.UpdatedAt = time.Now().UTC()
	tag, err := p.pool.Exec(ctx, `
		UPDATE vacations
		SET title = $2, destination = $3, start_date = $4, end_date = $5,
		    latitude = $6, longitude = $7, notes = $8, updated_at = $9
		WHERE id = $1`,
		v.ID, v.Title, v.Destination, v.StartDate, v.EndDate,
		v.Latitude, v.Longitude, v.Notes, v.UpdatedAt)
	if err != nil {
		return fmt.Errorf("store: updating vacation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) DeleteVacation(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM vacations WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("store: deleting vacation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Sights ----

func (p *Postgres) CreateSight(ctx context.Context, s *models.Sight) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	s.CreatedAt = time.Now().UTC()

	_, err := p.pool.Exec(ctx, `
		INSERT INTO sights
			(id, vacation_id, name, category, description, latitude, longitude, planned_date, visited, notes, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		s.ID, s.VacationID, s.Name, s.Category, s.Description,
		s.Latitude, s.Longitude, s.PlannedDate, s.Visited, s.Notes, s.CreatedAt)
	if err != nil {
		return fmt.Errorf("store: creating sight: %w", err)
	}
	return nil
}

func (p *Postgres) GetSight(ctx context.Context, id uuid.UUID) (*models.Sight, error) {
	row := p.pool.QueryRow(ctx, `
		SELECT id, vacation_id, name, category, description, latitude, longitude, planned_date, visited, notes, created_at
		FROM sights WHERE id = $1`, id)

	var s models.Sight
	if err := scanSight(row, &s); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: getting sight: %w", err)
	}
	return &s, nil
}

func (p *Postgres) ListSights(ctx context.Context, vacationID uuid.UUID) ([]models.Sight, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, vacation_id, name, category, description, latitude, longitude, planned_date, visited, notes, created_at
		FROM sights WHERE vacation_id = $1 ORDER BY created_at ASC`, vacationID)
	if err != nil {
		return nil, fmt.Errorf("store: listing sights: %w", err)
	}
	defer rows.Close()

	var out []models.Sight
	for rows.Next() {
		var s models.Sight
		if err := scanSight(rows, &s); err != nil {
			return nil, fmt.Errorf("store: scanning sight: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating sights: %w", err)
	}
	return out, nil
}

func (p *Postgres) UpdateSight(ctx context.Context, s *models.Sight) error {
	tag, err := p.pool.Exec(ctx, `
		UPDATE sights
		SET name = $2, category = $3, description = $4, latitude = $5, longitude = $6,
		    planned_date = $7, visited = $8, notes = $9
		WHERE id = $1`,
		s.ID, s.Name, s.Category, s.Description, s.Latitude, s.Longitude,
		s.PlannedDate, s.Visited, s.Notes)
	if err != nil {
		return fmt.Errorf("store: updating sight: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) DeleteSight(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM sights WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("store: deleting sight: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Travel segments ----

func (p *Postgres) CreateTravelSegment(ctx context.Context, t *models.TravelSegment) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	t.CreatedAt = time.Now().UTC()

	_, err := p.pool.Exec(ctx, `
		INSERT INTO travel_segments
			(id, vacation_id, kind, mode, from_location, to_location, depart_at, arrive_at, notes, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		t.ID, t.VacationID, string(t.Kind), t.Mode, t.FromLocation, t.ToLocation,
		t.DepartAt, t.ArriveAt, t.Notes, t.CreatedAt)
	if err != nil {
		return fmt.Errorf("store: creating travel segment: %w", err)
	}
	return nil
}

func (p *Postgres) ListTravelSegments(ctx context.Context, vacationID uuid.UUID) ([]models.TravelSegment, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, vacation_id, kind, mode, from_location, to_location, depart_at, arrive_at, notes, created_at
		FROM travel_segments WHERE vacation_id = $1 ORDER BY kind ASC, created_at ASC`, vacationID)
	if err != nil {
		return nil, fmt.Errorf("store: listing travel segments: %w", err)
	}
	defer rows.Close()

	var out []models.TravelSegment
	for rows.Next() {
		var t models.TravelSegment
		var kind string
		if err := rows.Scan(&t.ID, &t.VacationID, &kind, &t.Mode, &t.FromLocation,
			&t.ToLocation, &t.DepartAt, &t.ArriveAt, &t.Notes, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scanning travel segment: %w", err)
		}
		t.Kind = models.TravelKind(kind)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating travel segments: %w", err)
	}
	return out, nil
}

func (p *Postgres) DeleteTravelSegment(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM travel_segments WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("store: deleting travel segment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- scan helpers ----

func scanVacation(row pgx.Row, v *models.Vacation) error {
	return row.Scan(&v.ID, &v.Title, &v.Destination, &v.StartDate, &v.EndDate,
		&v.Latitude, &v.Longitude, &v.Notes, &v.CreatedAt, &v.UpdatedAt)
}

func scanSight(row pgx.Row, s *models.Sight) error {
	return row.Scan(&s.ID, &s.VacationID, &s.Name, &s.Category, &s.Description,
		&s.Latitude, &s.Longitude, &s.PlannedDate, &s.Visited, &s.Notes, &s.CreatedAt)
}
