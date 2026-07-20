package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO), registers "sqlite"

	"github.com/daknoblo/vacationplanner/internal/models"
)

// Layouts used to store dates and timestamps as sortable TEXT in SQLite.
const (
	dbDateLayout = "2006-01-02"
	dbTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"
)

// SQLite is a SQLite-backed implementation of Store using the pure-Go
// modernc.org/sqlite driver (works with CGO_ENABLED=0 / distroless).
type SQLite struct {
	db *sql.DB
}

// Compile-time assertion that SQLite satisfies Store.
var _ Store = (*SQLite)(nil)

// NewSQLite opens (creating if needed) the database at path and verifies
// connectivity. Foreign keys, WAL mode and a busy timeout are enabled per
// connection via DSN pragmas.
func NewSQLite(ctx context.Context, path string) (*SQLite, error) {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: opening sqlite: %w", err)
	}
	// SQLite permits a single writer; serialize access to avoid lock errors.
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: pinging sqlite: %w", err)
	}
	return &SQLite{db: db}, nil
}

// Ping verifies the database connection is alive.
func (s *SQLite) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Close closes the underlying database handle.
func (s *SQLite) Close() { _ = s.db.Close() }

// ---- Vacations ----

func (s *SQLite) CreateVacation(ctx context.Context, v *models.Vacation) error {
	if v.ID == uuid.Nil {
		v.ID = uuid.New()
	}
	now := time.Now().UTC()
	v.CreatedAt, v.UpdatedAt = now, now

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO vacations
			(id, title, destination, start_date, end_date, latitude, longitude, notes, budget, people, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.ID, v.Title, v.Destination, dbDate(v.StartDate), dbDate(v.EndDate),
		v.Latitude, v.Longitude, v.Notes, v.Budget, v.People, dbTime(v.CreatedAt), dbTime(v.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store: creating vacation: %w", err)
	}
	return nil
}

func (s *SQLite) GetVacation(ctx context.Context, id uuid.UUID) (*models.Vacation, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, title, destination, start_date, end_date, latitude, longitude, notes, budget, people, created_at, updated_at
		FROM vacations WHERE id = ?`, id)

	var v models.Vacation
	if err := scanVacation(row, &v); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: getting vacation: %w", err)
	}
	return &v, nil
}

func (s *SQLite) ListVacations(ctx context.Context) ([]models.Vacation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title, destination, start_date, end_date, latitude, longitude, notes, budget, people, created_at, updated_at
		FROM vacations ORDER BY start_date ASC, created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: listing vacations: %w", err)
	}
	defer func() { _ = rows.Close() }()

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

func (s *SQLite) UpdateVacation(ctx context.Context, v *models.Vacation) error {
	v.UpdatedAt = time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `
		UPDATE vacations
		SET title = ?, destination = ?, start_date = ?, end_date = ?,
		    latitude = ?, longitude = ?, notes = ?, budget = ?, people = ?, updated_at = ?
		WHERE id = ?`,
		v.Title, v.Destination, dbDate(v.StartDate), dbDate(v.EndDate),
		v.Latitude, v.Longitude, v.Notes, v.Budget, v.People, dbTime(v.UpdatedAt), v.ID)
	if err != nil {
		return fmt.Errorf("store: updating vacation: %w", err)
	}
	return checkAffected(res)
}

func (s *SQLite) DeleteVacation(ctx context.Context, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM vacations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: deleting vacation: %w", err)
	}
	return checkAffected(res)
}

// ---- Sights ----

func (s *SQLite) CreateSight(ctx context.Context, sight *models.Sight) error {
	if sight.ID == uuid.Nil {
		sight.ID = uuid.New()
	}
	sight.CreatedAt = time.Now().UTC()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sights
			(id, vacation_id, name, category, description, latitude, longitude, planned_date, visited, notes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sight.ID, sight.VacationID, sight.Name, sight.Category, sight.Description,
		sight.Latitude, sight.Longitude, dbDatePtr(sight.PlannedDate), sight.Visited,
		sight.Notes, dbTime(sight.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: creating sight: %w", err)
	}
	return nil
}

func (s *SQLite) GetSight(ctx context.Context, id uuid.UUID) (*models.Sight, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, vacation_id, name, category, description, latitude, longitude, planned_date, visited, notes, created_at
		FROM sights WHERE id = ?`, id)

	var sight models.Sight
	if err := scanSight(row, &sight); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: getting sight: %w", err)
	}
	return &sight, nil
}

func (s *SQLite) ListSights(ctx context.Context, vacationID uuid.UUID) ([]models.Sight, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, vacation_id, name, category, description, latitude, longitude, planned_date, visited, notes, created_at
		FROM sights WHERE vacation_id = ? ORDER BY created_at ASC`, vacationID)
	if err != nil {
		return nil, fmt.Errorf("store: listing sights: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []models.Sight
	for rows.Next() {
		var sight models.Sight
		if err := scanSight(rows, &sight); err != nil {
			return nil, fmt.Errorf("store: scanning sight: %w", err)
		}
		out = append(out, sight)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating sights: %w", err)
	}
	return out, nil
}

func (s *SQLite) UpdateSight(ctx context.Context, sight *models.Sight) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE sights
		SET name = ?, category = ?, description = ?, latitude = ?, longitude = ?,
		    planned_date = ?, visited = ?, notes = ?
		WHERE id = ?`,
		sight.Name, sight.Category, sight.Description, sight.Latitude, sight.Longitude,
		dbDatePtr(sight.PlannedDate), sight.Visited, sight.Notes, sight.ID)
	if err != nil {
		return fmt.Errorf("store: updating sight: %w", err)
	}
	return checkAffected(res)
}

func (s *SQLite) DeleteSight(ctx context.Context, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sights WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: deleting sight: %w", err)
	}
	return checkAffected(res)
}

// ---- Activities ----

func (s *SQLite) CreateActivity(ctx context.Context, a *models.Activity) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	now := time.Now().UTC()
	a.CreatedAt = now
	a.UpdatedAt = now

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO activities
			(id, vacation_id, day, title, category, start_min, end_min, description, location, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.VacationID, dbDate(a.Day), a.Title, a.Category, a.StartMin, a.EndMin,
		a.Description, a.Location, dbTime(a.CreatedAt), dbTime(a.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store: creating activity: %w", err)
	}
	return nil
}

func (s *SQLite) GetActivity(ctx context.Context, id uuid.UUID) (*models.Activity, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, vacation_id, day, title, category, start_min, end_min, description, location, created_at, updated_at
		FROM activities WHERE id = ?`, id)
	var a models.Activity
	if err := scanActivity(row, &a); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: getting activity: %w", err)
	}
	return &a, nil
}

func (s *SQLite) ListActivities(ctx context.Context, vacationID uuid.UUID) ([]models.Activity, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, vacation_id, day, title, category, start_min, end_min, description, location, created_at, updated_at
		FROM activities WHERE vacation_id = ? ORDER BY day ASC, start_min ASC, created_at ASC`, vacationID)
	if err != nil {
		return nil, fmt.Errorf("store: listing activities: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []models.Activity
	for rows.Next() {
		var a models.Activity
		if err := scanActivity(rows, &a); err != nil {
			return nil, fmt.Errorf("store: scanning activity: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating activities: %w", err)
	}
	return out, nil
}

func (s *SQLite) UpdateActivity(ctx context.Context, a *models.Activity) error {
	a.UpdatedAt = time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `
		UPDATE activities
		SET day = ?, title = ?, category = ?, start_min = ?, end_min = ?, description = ?, location = ?, updated_at = ?
		WHERE id = ?`,
		dbDate(a.Day), a.Title, a.Category, a.StartMin, a.EndMin, a.Description, a.Location,
		dbTime(a.UpdatedAt), a.ID)
	if err != nil {
		return fmt.Errorf("store: updating activity: %w", err)
	}
	return checkAffected(res)
}

func (s *SQLite) DeleteActivity(ctx context.Context, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM activities WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: deleting activity: %w", err)
	}
	return checkAffected(res)
}

// ---- Travel segments ----

func (s *SQLite) CreateTravelSegment(ctx context.Context, t *models.TravelSegment) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	t.CreatedAt = time.Now().UTC()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO travel_segments
			(id, vacation_id, kind, mode, from_location, to_location, depart_at, arrive_at, notes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.VacationID, string(t.Kind), t.Mode, t.FromLocation, t.ToLocation,
		dbTimePtr(t.DepartAt), dbTimePtr(t.ArriveAt), t.Notes, dbTime(t.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: creating travel segment: %w", err)
	}
	return nil
}

func (s *SQLite) ListTravelSegments(ctx context.Context, vacationID uuid.UUID) ([]models.TravelSegment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, vacation_id, kind, mode, from_location, to_location, depart_at, arrive_at, notes, created_at
		FROM travel_segments WHERE vacation_id = ? ORDER BY kind ASC, created_at ASC`, vacationID)
	if err != nil {
		return nil, fmt.Errorf("store: listing travel segments: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []models.TravelSegment
	for rows.Next() {
		var t models.TravelSegment
		if err := scanTravel(rows, &t); err != nil {
			return nil, fmt.Errorf("store: scanning travel segment: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating travel segments: %w", err)
	}
	return out, nil
}

func (s *SQLite) DeleteTravelSegment(ctx context.Context, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM travel_segments WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: deleting travel segment: %w", err)
	}
	return checkAffected(res)
}

// ---- Settings ----

func (s *SQLite) GetSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("store: listing settings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("store: scanning setting: %w", err)
		}
		out[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating settings: %w", err)
	}
	return out, nil
}

func (s *SQLite) PutSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("store: putting setting: %w", err)
	}
	return nil
}

// ---- helpers ----

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func checkAffected(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func dbDate(t time.Time) string { return t.Format(dbDateLayout) }
func dbTime(t time.Time) string { return t.UTC().Format(dbTimeLayout) }

func dbDatePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format(dbDateLayout)
}

func dbTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(dbTimeLayout)
}

func scanVacation(sc rowScanner, v *models.Vacation) error {
	var start, end, created, updated string
	if err := sc.Scan(&v.ID, &v.Title, &v.Destination, &start, &end,
		&v.Latitude, &v.Longitude, &v.Notes, &v.Budget, &v.People, &created, &updated); err != nil {
		return err
	}
	var err error
	if v.StartDate, err = time.Parse(dbDateLayout, start); err != nil {
		return fmt.Errorf("store: parsing start_date: %w", err)
	}
	if v.EndDate, err = time.Parse(dbDateLayout, end); err != nil {
		return fmt.Errorf("store: parsing end_date: %w", err)
	}
	if v.CreatedAt, err = time.Parse(dbTimeLayout, created); err != nil {
		return fmt.Errorf("store: parsing created_at: %w", err)
	}
	if v.UpdatedAt, err = time.Parse(dbTimeLayout, updated); err != nil {
		return fmt.Errorf("store: parsing updated_at: %w", err)
	}
	return nil
}

func scanSight(sc rowScanner, sight *models.Sight) error {
	var planned sql.NullString
	var created string
	if err := sc.Scan(&sight.ID, &sight.VacationID, &sight.Name, &sight.Category,
		&sight.Description, &sight.Latitude, &sight.Longitude, &planned,
		&sight.Visited, &sight.Notes, &created); err != nil {
		return err
	}
	if planned.Valid {
		t, err := time.Parse(dbDateLayout, planned.String)
		if err != nil {
			return fmt.Errorf("store: parsing planned_date: %w", err)
		}
		sight.PlannedDate = &t
	}
	var err error
	if sight.CreatedAt, err = time.Parse(dbTimeLayout, created); err != nil {
		return fmt.Errorf("store: parsing created_at: %w", err)
	}
	return nil
}

func scanActivity(sc rowScanner, a *models.Activity) error {
	var day, created, updated string
	if err := sc.Scan(&a.ID, &a.VacationID, &day, &a.Title, &a.Category,
		&a.StartMin, &a.EndMin, &a.Description, &a.Location, &created, &updated); err != nil {
		return err
	}
	var err error
	if a.Day, err = time.Parse(dbDateLayout, day); err != nil {
		return fmt.Errorf("store: parsing activity day: %w", err)
	}
	if a.CreatedAt, err = time.Parse(dbTimeLayout, created); err != nil {
		return fmt.Errorf("store: parsing created_at: %w", err)
	}
	if a.UpdatedAt, err = time.Parse(dbTimeLayout, updated); err != nil {
		return fmt.Errorf("store: parsing updated_at: %w", err)
	}
	return nil
}

func scanTravel(sc rowScanner, t *models.TravelSegment) error {
	var kind, created string
	var depart, arrive sql.NullString
	if err := sc.Scan(&t.ID, &t.VacationID, &kind, &t.Mode, &t.FromLocation,
		&t.ToLocation, &depart, &arrive, &t.Notes, &created); err != nil {
		return err
	}
	t.Kind = models.TravelKind(kind)
	if depart.Valid {
		v, err := time.Parse(dbTimeLayout, depart.String)
		if err != nil {
			return fmt.Errorf("store: parsing depart_at: %w", err)
		}
		t.DepartAt = &v
	}
	if arrive.Valid {
		v, err := time.Parse(dbTimeLayout, arrive.String)
		if err != nil {
			return fmt.Errorf("store: parsing arrive_at: %w", err)
		}
		t.ArriveAt = &v
	}
	var err error
	if t.CreatedAt, err = time.Parse(dbTimeLayout, created); err != nil {
		return fmt.Errorf("store: parsing created_at: %w", err)
	}
	return nil
}
