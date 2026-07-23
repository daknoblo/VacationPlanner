package store

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// Stats holds aggregate object counts used by the statistics view.
type Stats struct {
	Vacations int
	Days      int
	Items     int
	Travel    int
	Documents int
}

// Stats returns aggregate counts across all vacations.
func (s *SQLite) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	count := func(query string, dst *int) error {
		return s.db.QueryRowContext(ctx, query).Scan(dst)
	}
	if err := count(`SELECT COUNT(*) FROM vacations`, &st.Vacations); err != nil {
		return st, fmt.Errorf("store: counting vacations: %w", err)
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(CAST(SUM(julianday(end_date) - julianday(start_date) + 1) AS INTEGER), 0)
		 FROM vacations WHERE end_date >= start_date`).Scan(&st.Days); err != nil {
		return st, fmt.Errorf("store: summing days: %w", err)
	}
	if err := count(`SELECT COUNT(*) FROM items`, &st.Items); err != nil {
		return st, fmt.Errorf("store: counting items: %w", err)
	}
	if err := count(`SELECT COUNT(*) FROM travel_segments`, &st.Travel); err != nil {
		return st, fmt.Errorf("store: counting travel segments: %w", err)
	}
	if err := count(`SELECT COUNT(*) FROM documents`, &st.Documents); err != nil {
		return st, fmt.Errorf("store: counting documents: %w", err)
	}
	return st, nil
}

// BackupTo writes a consistent, standalone copy of the database to dest using
// SQLite's VACUUM INTO (safe with WAL, produces a single defragmented file).
func (s *SQLite) BackupTo(ctx context.Context, dest string) error {
	// dest is an application-generated file path; escape quotes for the literal.
	safe := strings.ReplaceAll(dest, "'", "''")
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO '"+safe+"'"); err != nil { //nolint:gosec // app-controlled path, quotes escaped
		return fmt.Errorf("store: backing up database: %w", err)
	}
	return nil
}

// Vacuum rebuilds the database file to reclaim unused space and defragment it,
// then refreshes the query-planner statistics. It serializes against Restore
// (which swaps the handle) via the same mutex.
func (s *SQLite) Vacuum(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("store: vacuuming database: %w", err)
	}
	// Best effort: truncate the WAL so freed pages are released to the OS, and
	// let SQLite update its internal statistics.
	if _, err := s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("store: checkpointing after vacuum: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "PRAGMA optimize"); err != nil {
		return fmt.Errorf("store: optimizing database: %w", err)
	}
	return nil
}

// Restore replaces the live database with the SQLite file at srcPath and then
// re-applies migrations. srcPath should already be validated with ValidSQLiteFile.
// This is safe for the app's single-writer, single-user model.
func (s *SQLite) Restore(ctx context.Context, srcPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.db.Close(); err != nil {
		return fmt.Errorf("store: closing before restore: %w", err)
	}
	// Remove stale WAL/SHM sidecar files from the outgoing database.
	_ = os.Remove(s.path + "-wal")
	_ = os.Remove(s.path + "-shm")

	if err := copyFile(srcPath, s.path); err != nil {
		// Best effort: reopen the (unchanged) database so the app keeps working.
		if db, rerr := openDB(ctx, s.path); rerr == nil {
			s.db = db
		}
		return fmt.Errorf("store: replacing database: %w", err)
	}

	db, err := openDB(ctx, s.path)
	if err != nil {
		return fmt.Errorf("store: reopening after restore: %w", err)
	}
	s.db = db
	if err := s.Migrate(ctx); err != nil {
		return fmt.Errorf("store: migrating restored database: %w", err)
	}
	return nil
}

// ValidSQLiteFile reports whether the file at path begins with the SQLite header.
func ValidSQLiteFile(path string) bool {
	f, err := os.Open(path) //nolint:gosec // path is a server-managed temp/backup file
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	hdr := make([]byte, 16)
	if _, err := io.ReadFull(f, hdr); err != nil {
		return false
	}
	return string(hdr) == "SQLite format 3\x00"
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // server-managed source path
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	tmp := dst + ".tmp"
	out, err := os.Create(tmp) //nolint:gosec // server-managed destination path
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
