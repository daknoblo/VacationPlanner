package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
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
	db   *sql.DB
	path string
	mu   sync.Mutex // serializes Restore (which swaps the db handle)
}

// Compile-time assertion that SQLite satisfies Store.
var _ Store = (*SQLite)(nil)

// openDB opens the SQLite database at path with the standard DSN pragmas and
// verifies connectivity.
func openDB(ctx context.Context, path string) (*sql.DB, error) {
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
	return db, nil
}

// NewSQLite opens (creating if needed) the database at path and verifies
// connectivity. Foreign keys, WAL mode and a busy timeout are enabled per
// connection via DSN pragmas.
func NewSQLite(ctx context.Context, path string) (*SQLite, error) {
	db, err := openDB(ctx, path)
	if err != nil {
		return nil, err
	}
	return &SQLite{db: db, path: path}, nil
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
			(id, title, destination, start_date, end_date, latitude, longitude, map_zoom, notes, budget, people, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.ID, v.Title, v.Destination, dbDate(v.StartDate), dbDate(v.EndDate),
		v.Latitude, v.Longitude, v.MapZoom, v.Notes, v.Budget, v.People, dbTime(v.CreatedAt), dbTime(v.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store: creating vacation: %w", err)
	}
	return nil
}

func (s *SQLite) GetVacation(ctx context.Context, id uuid.UUID) (*models.Vacation, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, title, destination, start_date, end_date, latitude, longitude, map_zoom, notes, budget, people, created_at, updated_at
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
		SELECT id, title, destination, start_date, end_date, latitude, longitude, map_zoom, notes, budget, people, created_at, updated_at
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
		    latitude = ?, longitude = ?, map_zoom = ?, notes = ?, budget = ?, people = ?, updated_at = ?
		WHERE id = ?`,
		v.Title, v.Destination, dbDate(v.StartDate), dbDate(v.EndDate),
		v.Latitude, v.Longitude, v.MapZoom, v.Notes, v.Budget, v.People, dbTime(v.UpdatedAt), v.ID)
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

// ---- Items ----

func (s *SQLite) CreateItem(ctx context.Context, i *models.Item) error {
	if i.ID == uuid.Nil {
		i.ID = uuid.New()
	}
	now := time.Now().UTC()
	i.CreatedAt = now
	i.UpdatedAt = now

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO items
			(id, vacation_id, category, title, description, location, latitude, longitude, day, start_min, end_min, cost, visited, notes, origin_ref, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		i.ID, i.VacationID, i.Category, i.Title, i.Description, i.Location,
		i.Latitude, i.Longitude, dbDatePtr(i.Day), i.StartMin, i.EndMin, i.Cost,
		i.Visited, i.Notes, i.OriginRef, dbTime(i.CreatedAt), dbTime(i.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store: creating item: %w", err)
	}
	return nil
}

func (s *SQLite) GetItem(ctx context.Context, id uuid.UUID) (*models.Item, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, vacation_id, category, title, description, location, latitude, longitude, day, start_min, end_min, cost, visited, notes, origin_ref, created_at, updated_at
		FROM items WHERE id = ?`, id)
	var it models.Item
	if err := scanItem(row, &it); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: getting item: %w", err)
	}
	return &it, nil
}

func (s *SQLite) ListItems(ctx context.Context, vacationID uuid.UUID) ([]models.Item, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, vacation_id, category, title, description, location, latitude, longitude, day, start_min, end_min, cost, visited, notes, origin_ref, created_at, updated_at
		FROM items WHERE vacation_id = ? ORDER BY day ASC, start_min ASC, created_at ASC`, vacationID)
	if err != nil {
		return nil, fmt.Errorf("store: listing items: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []models.Item
	for rows.Next() {
		var it models.Item
		if err := scanItem(rows, &it); err != nil {
			return nil, fmt.Errorf("store: scanning item: %w", err)
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating items: %w", err)
	}
	return out, nil
}

func (s *SQLite) UpdateItem(ctx context.Context, i *models.Item) error {
	i.UpdatedAt = time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `
		UPDATE items
		SET category = ?, title = ?, description = ?, location = ?, latitude = ?, longitude = ?,
		    day = ?, start_min = ?, end_min = ?, cost = ?, visited = ?, notes = ?, origin_ref = ?, updated_at = ?
		WHERE id = ?`,
		i.Category, i.Title, i.Description, i.Location, i.Latitude, i.Longitude,
		dbDatePtr(i.Day), i.StartMin, i.EndMin, i.Cost, i.Visited, i.Notes, i.OriginRef,
		dbTime(i.UpdatedAt), i.ID)
	if err != nil {
		return fmt.Errorf("store: updating item: %w", err)
	}
	return checkAffected(res)
}

func (s *SQLite) DeleteItem(ctx context.Context, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM items WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: deleting item: %w", err)
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
			(id, vacation_id, kind, step_order, mode, from_location, to_location, from_lat, from_lng,
			 to_lat, to_lng, depart_at, arrive_at, distance_m, duration_s, cost, notes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.VacationID, string(t.Kind), t.StepOrder, t.Mode, t.FromLocation, t.ToLocation,
		t.FromLat, t.FromLng, t.ToLat, t.ToLng, dbTimePtr(t.DepartAt), dbTimePtr(t.ArriveAt),
		t.DistanceM, t.DurationS, t.Cost, t.Notes, dbTime(t.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: creating travel segment: %w", err)
	}
	return nil
}

// UpsertTravelSegment keeps a single row per (vacation, kind, step_order): it
// updates the matching leg in place when present, otherwise inserts a new one.
// This lets a step editor auto-save without carrying a fragile row id.
func (s *SQLite) UpsertTravelSegment(ctx context.Context, t *models.TravelSegment) error {
	var existingID uuid.UUID
	row := s.db.QueryRowContext(ctx,
		`SELECT id FROM travel_segments WHERE vacation_id = ? AND kind = ? AND step_order = ? ORDER BY created_at ASC LIMIT 1`,
		t.VacationID, string(t.Kind), t.StepOrder)
	err := row.Scan(&existingID)
	switch {
	case err == nil:
		t.ID = existingID
		_, uerr := s.db.ExecContext(ctx, `
			UPDATE travel_segments SET
				mode = ?, from_location = ?, to_location = ?, from_lat = ?, from_lng = ?,
				to_lat = ?, to_lng = ?, depart_at = ?, arrive_at = ?, distance_m = ?,
				duration_s = ?, cost = ?, notes = ?
			WHERE id = ?`,
			t.Mode, t.FromLocation, t.ToLocation, t.FromLat, t.FromLng, t.ToLat, t.ToLng,
			dbTimePtr(t.DepartAt), dbTimePtr(t.ArriveAt), t.DistanceM, t.DurationS, t.Cost, t.Notes, t.ID)
		if uerr != nil {
			return fmt.Errorf("store: updating travel segment: %w", uerr)
		}
		return nil
	case errors.Is(err, sql.ErrNoRows):
		return s.CreateTravelSegment(ctx, t)
	default:
		return fmt.Errorf("store: checking travel segment: %w", err)
	}
}

func (s *SQLite) ListTravelSegments(ctx context.Context, vacationID uuid.UUID) ([]models.TravelSegment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, vacation_id, kind, step_order, mode, from_location, to_location, from_lat, from_lng,
		       to_lat, to_lng, depart_at, arrive_at, distance_m, duration_s, cost, notes, created_at
		FROM travel_segments WHERE vacation_id = ? ORDER BY kind ASC, step_order ASC, created_at ASC`, vacationID)
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

// ---- Categories ----

func (s *SQLite) ListCategories(ctx context.Context) ([]models.Category, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, icon, sort_order, created_at
		FROM categories ORDER BY sort_order ASC, name ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: listing categories: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []models.Category
	for rows.Next() {
		var c models.Category
		if err := scanCategory(rows, &c); err != nil {
			return nil, fmt.Errorf("store: scanning category: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating categories: %w", err)
	}
	return out, nil
}

func (s *SQLite) CreateCategory(ctx context.Context, c *models.Category) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	c.CreatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO categories (id, name, icon, sort_order, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		c.ID, c.Name, c.Icon, c.SortOrder, dbTime(c.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: creating category: %w", err)
	}
	return nil
}

func (s *SQLite) DeleteCategory(ctx context.Context, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM categories WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: deleting category: %w", err)
	}
	return checkAffected(res)
}

func scanCategory(sc rowScanner, c *models.Category) error {
	var created string
	if err := sc.Scan(&c.ID, &c.Name, &c.Icon, &c.SortOrder, &created); err != nil {
		return err
	}
	var err error
	if c.CreatedAt, err = time.Parse(dbTimeLayout, created); err != nil {
		return fmt.Errorf("store: parsing category created_at: %w", err)
	}
	return nil
}

// ---- Lodging ----

func (s *SQLite) CreateLodging(ctx context.Context, l *models.Lodging) error {
	if l.ID == uuid.Nil {
		l.ID = uuid.New()
	}
	now := time.Now().UTC()
	l.CreatedAt, l.UpdatedAt = now, now

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO lodging (id, vacation_id, name, location, latitude, longitude, check_in, check_out, cost, notes, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.ID, l.VacationID, l.Name, l.Location, l.Latitude, l.Longitude, dbTime(l.CheckIn), dbTime(l.CheckOut),
		l.Cost, l.Notes, dbTime(l.CreatedAt), dbTime(l.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store: creating lodging: %w", err)
	}
	return nil
}

func (s *SQLite) UpdateLodging(ctx context.Context, l *models.Lodging) error {
	l.UpdatedAt = time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `
		UPDATE lodging
		SET name = ?, location = ?, latitude = ?, longitude = ?, check_in = ?, check_out = ?, cost = ?, notes = ?, updated_at = ?
		WHERE id = ?`,
		l.Name, l.Location, l.Latitude, l.Longitude, dbTime(l.CheckIn), dbTime(l.CheckOut),
		l.Cost, l.Notes, dbTime(l.UpdatedAt), l.ID)
	if err != nil {
		return fmt.Errorf("store: updating lodging: %w", err)
	}
	return checkAffected(res)
}

func (s *SQLite) GetLodging(ctx context.Context, id uuid.UUID) (*models.Lodging, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, vacation_id, name, location, latitude, longitude, check_in, check_out, cost, notes, created_at, updated_at
		FROM lodging WHERE id = ?`, id)
	var l models.Lodging
	if err := scanLodging(row, &l); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: getting lodging: %w", err)
	}
	return &l, nil
}

func (s *SQLite) ListLodgings(ctx context.Context, vacationID uuid.UUID) ([]models.Lodging, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, vacation_id, name, location, latitude, longitude, check_in, check_out, cost, notes, created_at, updated_at
		FROM lodging WHERE vacation_id = ? ORDER BY check_in ASC, created_at ASC`, vacationID)
	if err != nil {
		return nil, fmt.Errorf("store: listing lodging: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []models.Lodging
	for rows.Next() {
		var l models.Lodging
		if err := scanLodging(rows, &l); err != nil {
			return nil, fmt.Errorf("store: scanning lodging: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating lodging: %w", err)
	}
	return out, nil
}

func (s *SQLite) DeleteLodging(ctx context.Context, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM lodging WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: deleting lodging: %w", err)
	}
	return checkAffected(res)
}

func scanLodging(sc rowScanner, l *models.Lodging) error {
	var checkIn, checkOut, created, updated string
	if err := sc.Scan(&l.ID, &l.VacationID, &l.Name, &l.Location, &l.Latitude, &l.Longitude,
		&checkIn, &checkOut, &l.Cost, &l.Notes, &created, &updated); err != nil {
		return err
	}
	var err error
	if l.CheckIn, err = time.Parse(dbTimeLayout, checkIn); err != nil {
		return fmt.Errorf("store: parsing lodging check_in: %w", err)
	}
	if l.CheckOut, err = time.Parse(dbTimeLayout, checkOut); err != nil {
		return fmt.Errorf("store: parsing lodging check_out: %w", err)
	}
	if l.CreatedAt, err = time.Parse(dbTimeLayout, created); err != nil {
		return fmt.Errorf("store: parsing lodging created_at: %w", err)
	}
	if l.UpdatedAt, err = time.Parse(dbTimeLayout, updated); err != nil {
		return fmt.Errorf("store: parsing lodging updated_at: %w", err)
	}
	return nil
}

// ---- Documents ----

// documentMetaCols lists the document columns loaded when only metadata (not the
// file bytes) is needed.
const documentMetaCols = `id, item_id, vacation_id, travel_kind, travel_step, lodging_id, filename, content_type, size, created_at`

func (s *SQLite) CreateDocument(ctx context.Context, d *models.Document) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	d.CreatedAt = time.Now().UTC()

	var kind any
	var step any
	if d.VacationID != nil {
		kind = string(d.TravelKind)
		step = d.TravelStep
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO documents
			(id, item_id, vacation_id, travel_kind, travel_step, lodging_id, filename, content_type, size, data, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, dbUUIDPtr(d.ItemID), dbUUIDPtr(d.VacationID), kind, step, dbUUIDPtr(d.LodgingID),
		d.Filename, d.ContentType, d.Size, d.Data, dbTime(d.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: creating document: %w", err)
	}
	return nil
}

func (s *SQLite) GetDocument(ctx context.Context, id uuid.UUID) (*models.Document, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+documentMetaCols+` FROM documents WHERE id = ?`, id)
	var d models.Document
	if err := scanDocument(row, &d); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: getting document: %w", err)
	}
	return &d, nil
}

func (s *SQLite) ReadDocument(ctx context.Context, id uuid.UUID) (*models.Document, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+documentMetaCols+`, data FROM documents WHERE id = ?`, id)
	var d models.Document
	if err := scanDocumentWithData(row, &d); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: reading document: %w", err)
	}
	return &d, nil
}

func (s *SQLite) ListItemDocuments(ctx context.Context, itemID uuid.UUID) ([]models.Document, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+documentMetaCols+` FROM documents WHERE item_id = ? ORDER BY created_at ASC`, itemID)
	if err != nil {
		return nil, fmt.Errorf("store: listing item documents: %w", err)
	}
	return scanDocumentList(rows)
}

func (s *SQLite) ListTravelDocuments(ctx context.Context, vacationID uuid.UUID, kind models.TravelKind, step int) ([]models.Document, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+documentMetaCols+` FROM documents
		 WHERE vacation_id = ? AND travel_kind = ? AND travel_step = ? ORDER BY created_at ASC`,
		vacationID, string(kind), step)
	if err != nil {
		return nil, fmt.Errorf("store: listing travel documents: %w", err)
	}
	return scanDocumentList(rows)
}

func (s *SQLite) ListLodgingDocuments(ctx context.Context, lodgingID uuid.UUID) ([]models.Document, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+documentMetaCols+` FROM documents WHERE lodging_id = ? ORDER BY created_at ASC`, lodgingID)
	if err != nil {
		return nil, fmt.Errorf("store: listing lodging documents: %w", err)
	}
	return scanDocumentList(rows)
}

func (s *SQLite) DeleteDocument(ctx context.Context, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM documents WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: deleting document: %w", err)
	}
	return checkAffected(res)
}

// DeleteTravelStepDocuments removes all documents attached to a specific travel
// leg. It is used when a leg is removed so its documents cannot resurface on a
// later leg that happens to reuse the same step order.
func (s *SQLite) DeleteTravelStepDocuments(ctx context.Context, vacationID uuid.UUID, kind models.TravelKind, step int) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM documents WHERE vacation_id = ? AND travel_kind = ? AND travel_step = ?`,
		vacationID, string(kind), step)
	if err != nil {
		return fmt.Errorf("store: deleting travel step documents: %w", err)
	}
	return nil
}

func scanDocumentList(rows *sql.Rows) ([]models.Document, error) {
	defer func() { _ = rows.Close() }()
	var out []models.Document
	for rows.Next() {
		var d models.Document
		if err := scanDocument(rows, &d); err != nil {
			return nil, fmt.Errorf("store: scanning document: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating documents: %w", err)
	}
	return out, nil
}

func scanDocument(sc rowScanner, d *models.Document) error {
	return scanDocumentInto(sc, d, nil)
}

func scanDocumentWithData(sc rowScanner, d *models.Document) error {
	return scanDocumentInto(sc, d, &d.Data)
}

func scanDocumentInto(sc rowScanner, d *models.Document, data *[]byte) error {
	var itemID, vacID, lodgingID uuid.NullUUID
	var kind sql.NullString
	var step sql.NullInt64
	var created string
	dest := []any{&d.ID, &itemID, &vacID, &kind, &step, &lodgingID, &d.Filename, &d.ContentType, &d.Size, &created}
	if data != nil {
		dest = append(dest, data)
	}
	if err := sc.Scan(dest...); err != nil {
		return err
	}
	if itemID.Valid {
		id := itemID.UUID
		d.ItemID = &id
	}
	if vacID.Valid {
		id := vacID.UUID
		d.VacationID = &id
	}
	if lodgingID.Valid {
		id := lodgingID.UUID
		d.LodgingID = &id
	}
	d.TravelKind = models.TravelKind(kind.String)
	d.TravelStep = int(step.Int64)
	var err error
	if d.CreatedAt, err = time.Parse(dbTimeLayout, created); err != nil {
		return fmt.Errorf("store: parsing document created_at: %w", err)
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

func dbUUIDPtr(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return *id
}

func scanVacation(sc rowScanner, v *models.Vacation) error {
	var start, end, created, updated string
	if err := sc.Scan(&v.ID, &v.Title, &v.Destination, &start, &end,
		&v.Latitude, &v.Longitude, &v.MapZoom, &v.Notes, &v.Budget, &v.People, &created, &updated); err != nil {
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

func scanItem(sc rowScanner, it *models.Item) error {
	var day sql.NullString
	var created, updated string
	if err := sc.Scan(&it.ID, &it.VacationID, &it.Category, &it.Title, &it.Description,
		&it.Location, &it.Latitude, &it.Longitude, &day, &it.StartMin, &it.EndMin,
		&it.Cost, &it.Visited, &it.Notes, &it.OriginRef, &created, &updated); err != nil {
		return err
	}
	if day.Valid {
		t, err := time.Parse(dbDateLayout, day.String)
		if err != nil {
			return fmt.Errorf("store: parsing item day: %w", err)
		}
		it.Day = &t
	}
	var err error
	if it.CreatedAt, err = time.Parse(dbTimeLayout, created); err != nil {
		return fmt.Errorf("store: parsing created_at: %w", err)
	}
	if it.UpdatedAt, err = time.Parse(dbTimeLayout, updated); err != nil {
		return fmt.Errorf("store: parsing updated_at: %w", err)
	}
	return nil
}

func scanTravel(sc rowScanner, t *models.TravelSegment) error {
	var kind, created string
	var depart, arrive sql.NullString
	if err := sc.Scan(&t.ID, &t.VacationID, &kind, &t.StepOrder, &t.Mode, &t.FromLocation,
		&t.ToLocation, &t.FromLat, &t.FromLng, &t.ToLat, &t.ToLng, &depart, &arrive,
		&t.DistanceM, &t.DurationS, &t.Cost, &t.Notes, &created); err != nil {
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
