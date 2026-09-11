package store

import (
	"context"
	"fmt"
)

func (s *SQLite) CountPendingCheatsheetJobs(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM cheatsheet_jobs WHERE status IN ('queued', 'running')`).Scan(&count); err != nil {
		return 0, fmt.Errorf("store: counting pending translations: %w", err)
	}
	return count, nil
}
