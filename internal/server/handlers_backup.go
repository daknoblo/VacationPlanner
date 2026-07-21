package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/store"
)

const (
	maxBackups            = 5
	maxRestoreUploadBytes = 256 << 20 // 256 MiB
)

// backupView is a single backup entry shown in the UI.
type backupView struct {
	Name      string
	SizeLabel string
	TimeLabel string
}

func (s *Server) backupDir() string {
	return filepath.Join(filepath.Dir(s.cfg.DBPath), "backups")
}

// dbSizeBytes returns the total on-disk size of the live database (incl. WAL/SHM).
func (s *Server) dbSizeBytes() int64 {
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if info, err := os.Stat(s.cfg.DBPath + suffix); err == nil {
			total += info.Size()
		}
	}
	return total
}

func (s *Server) listBackups() []backupView {
	entries, err := os.ReadDir(s.backupDir())
	if err != nil {
		return nil
	}
	out := make([]backupView, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !isBackupName(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, backupView{
			Name:      e.Name(),
			SizeLabel: humanBytes(info.Size()),
			TimeLabel: info.ModTime().Format("2006-01-02 15:04"),
		})
	}
	// Newest first (filenames are timestamp-sortable).
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out
}

// rotateBackups keeps only the newest maxBackups backup files.
func (s *Server) rotateBackups() {
	entries, err := os.ReadDir(s.backupDir())
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && isBackupName(e.Name()) {
			names = append(names, e.Name())
		}
	}
	if len(names) <= maxBackups {
		return
	}
	sort.Strings(names) // oldest first
	for _, name := range names[:len(names)-maxBackups] {
		_ = os.Remove(filepath.Join(s.backupDir(), name))
	}
}

// uniqueBackupPath returns a non-existing backup path in dir, disambiguating
// collisions that occur within the same second.
func uniqueBackupPath(dir string) string {
	ts := time.Now().Format("2006-01-02_150405")
	p := filepath.Join(dir, "backup-"+ts+".db")
	for i := 2; i < 1000; i++ {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("backup-%s-%d.db", ts, i))
	}
	return p
}

func isBackupName(name string) bool {
	return strings.HasPrefix(name, "backup-") && strings.HasSuffix(name, ".db") &&
		!strings.ContainsAny(name, `/\`)
}

// safeBackupPath resolves a user-supplied name to a path inside the backup
// directory, guarding against path traversal.
func (s *Server) safeBackupPath(name string) (string, bool) {
	name = filepath.Base(strings.TrimSpace(name))
	if !isBackupName(name) {
		return "", false
	}
	return filepath.Join(s.backupDir(), name), true
}

func (s *Server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	dir := s.backupDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.store.BackupTo(r.Context(), uniqueBackupPath(dir)); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.rotateBackups()
	if isHTMX(r) {
		hxTrigger(w, "saved")
	}
	s.fragment(w, r, "backup_list", map[string]any{"Backups": s.listBackups()})
}

func (s *Server) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	path, ok := s.safeBackupPath(chi.URLParam(r, "name"))
	if !ok {
		s.notFound(w, r)
		return
	}
	_ = os.Remove(path)
	s.fragment(w, r, "backup_list", map[string]any{"Backups": s.listBackups()})
}

func (s *Server) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	path, ok := s.safeBackupPath(chi.URLParam(r, "name"))
	if !ok {
		s.notFound(w, r)
		return
	}
	f, err := os.Open(path) //nolint:gosec // path validated to live inside the backup dir
	if err != nil {
		s.notFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)
}

// handleRestoreBackup restores the database from an uploaded SQLite file.
func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	loc := i18n.FromContext(r.Context())

	file, _, err := r.FormFile("backup")
	if err != nil {
		s.formError(w, r, "#restore-error", loc.T("error.restore_no_file"))
		return
	}
	defer func() { _ = file.Close() }()

	tmpPath, err := saveTemp(io.LimitReader(file, maxRestoreUploadBytes))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	defer func() { _ = os.Remove(tmpPath) }()

	if !store.ValidSQLiteFile(tmpPath) {
		s.formError(w, r, "#restore-error", loc.T("error.restore_invalid"))
		return
	}
	if err := s.applyRestore(r.Context(), tmpPath); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirectSettings(w, r)
}

// handleRestoreBackupNamed restores the database from a stored backup file.
func (s *Server) handleRestoreBackupNamed(w http.ResponseWriter, r *http.Request) {
	src, ok := s.safeBackupPath(chi.URLParam(r, "name"))
	if !ok {
		s.notFound(w, r)
		return
	}
	in, err := os.Open(src) //nolint:gosec // path validated to live inside the backup dir
	if err != nil {
		s.notFound(w, r)
		return
	}
	// Copy to a temp file so rotation during the pre-restore backup cannot delete it.
	tmpPath, err := saveTemp(in)
	_ = in.Close()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	defer func() { _ = os.Remove(tmpPath) }()

	if !store.ValidSQLiteFile(tmpPath) {
		s.notFound(w, r)
		return
	}
	if err := s.applyRestore(r.Context(), tmpPath); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirectSettings(w, r)
}

// applyRestore snapshots the current database (so a restore can be undone) and
// then replaces it with the file at srcPath.
func (s *Server) applyRestore(ctx context.Context, srcPath string) error {
	dir := s.backupDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if err := s.store.BackupTo(ctx, uniqueBackupPath(dir)); err != nil {
		return err
	}
	s.rotateBackups()
	return s.store.Restore(ctx, srcPath)
}

// saveTemp streams r into a new temporary file and returns its path.
func saveTemp(r io.Reader) (string, error) {
	tmp, err := os.CreateTemp("", "vp-restore-*.db")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
