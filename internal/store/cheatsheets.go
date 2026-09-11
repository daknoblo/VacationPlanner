package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/google/uuid"
)

func (s *SQLite) GetCheatsheet(ctx context.Context, vacationID uuid.UUID, sourceLanguage string) (*models.Cheatsheet, error) {
	sheet := &models.Cheatsheet{VacationID: vacationID, SourceLanguage: sourceLanguage}
	var content, created string
	err := s.db.QueryRowContext(ctx, `SELECT destination_key, content, created_at FROM cheatsheets
		WHERE vacation_id = ? AND source_language = ?`, vacationID, sourceLanguage).Scan(&sheet.DestinationKey, &content, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: reading cheatsheet: %w", err)
	}
	if err := json.Unmarshal([]byte(content), sheet); err != nil {
		return nil, fmt.Errorf("store: decoding cheatsheet: %w", err)
	}
	if err := sheet.Validate(); err != nil {
		return nil, fmt.Errorf("store: invalid cheatsheet: %w", err)
	}
	sheet.CreatedAt, err = time.Parse(dbTimeLayout, created)
	if err != nil {
		return nil, fmt.Errorf("store: reading cheatsheet timestamp: %w", err)
	}
	return sheet, nil
}

func (s *SQLite) PutCheatsheet(ctx context.Context, sheet *models.Cheatsheet) error {
	if err := sheet.Validate(); err != nil {
		return fmt.Errorf("store: invalid cheatsheet: %w", err)
	}
	if sheet.VacationID == uuid.Nil || sheet.DestinationKey == "" || (sheet.SourceLanguage != "en" && sheet.SourceLanguage != "de") {
		return errors.New("store: cheatsheet requires a vacation, destination and supported source language")
	}
	content, err := json.Marshal(sheet)
	if err != nil {
		return fmt.Errorf("store: encoding cheatsheet: %w", err)
	}
	sheet.CreatedAt = time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkCheatsheetDestination(ctx, tx, sheet.VacationID, sheet.DestinationKey); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO cheatsheets
		(vacation_id, source_language, destination_key, content, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(vacation_id, source_language) DO UPDATE SET
		destination_key = excluded.destination_key, content = excluded.content, created_at = excluded.created_at`,
		sheet.VacationID, sheet.SourceLanguage, sheet.DestinationKey, string(content), dbTime(sheet.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: saving cheatsheet: %w", err)
	}
	return tx.Commit()
}

var ErrCheatsheetDestinationChanged = errors.New("store: cheatsheet destination or language changed")
var ErrCheatsheetQueueFull = errors.New("store: cheatsheet queue is full")

// Check and write within one transaction, so a destination edit cannot race the
// final cache write after a potentially slow provider response.
func checkCheatsheetDestination(ctx context.Context, tx *sql.Tx, id uuid.UUID, expected string) error {
	v := &models.Vacation{}
	if err := tx.QueryRowContext(ctx, `SELECT destination, latitude, longitude FROM vacations WHERE id = ?`, id).
		Scan(&v.Destination, &v.Latitude, &v.Longitude); err != nil {
		return err
	}
	key, err := models.CheatsheetDestinationKey(v)
	if err != nil {
		return err
	}
	if key != expected {
		return ErrCheatsheetDestinationChanged
	}
	return nil
}

func (s *SQLite) GetCheatsheetJob(ctx context.Context, job *models.CheatsheetJob) (string, error) {
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT status, created_at FROM cheatsheet_jobs
		WHERE vacation_id = ? AND source_language = ? AND destination_key = ? AND job_key = ?`,
		job.VacationID, job.SourceLanguage, job.DestinationKey, job.Key).Scan(&status, &job.Attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return status, err
}

// ReserveCheatsheetJob is the durable idempotency barrier before a paid call.
// An explicit phrase retry must name the exact failed attempt it replaces.
func (s *SQLite) ReserveCheatsheetJob(ctx context.Context, job *models.CheatsheetJob, retry bool) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var previous, attempt string
	err = tx.QueryRowContext(ctx, `SELECT status, created_at FROM cheatsheet_jobs
		WHERE vacation_id = ? AND source_language = ? AND destination_key = ? AND job_key = ?`,
		job.VacationID, job.SourceLanguage, job.DestinationKey, job.Key).Scan(&previous, &attempt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil && (!retry || previous == "queued" || previous == "running") {
		return false, nil
	}
	if retry && job.Key != "sheet" && (err != nil || previous != "failed" || job.Attempt == "" || job.Attempt != attempt) {
		return false, nil
	}
	if err := checkCheatsheetDestination(ctx, tx, job.VacationID, job.DestinationKey); err != nil {
		return false, err
	}
	full := false
	if job.Status == "queued" || job.Status == "running" {
		var pending int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cheatsheet_jobs WHERE status IN ('queued', 'running')`).Scan(&pending); err != nil {
			return false, err
		}
		full = pending >= 16
	}
	status := job.Status
	if full {
		if job.Key != "sheet" {
			// No provider call was dispatched and no reservation was made;
			// retrying later is safe when capacity becomes available.
			return false, ErrCheatsheetQueueFull
		}
		status = "failed"
	}
	nextAttempt := dbTime(time.Now().UTC())
	_, err = tx.ExecContext(ctx, `INSERT INTO cheatsheet_jobs
		(vacation_id, source_language, destination_key, job_key, status, created_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(vacation_id, source_language, destination_key, job_key) DO UPDATE SET
		status = excluded.status, created_at = excluded.created_at`,
		job.VacationID, job.SourceLanguage, job.DestinationKey, job.Key, status, nextAttempt)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if full {
		return false, ErrCheatsheetQueueFull
	}
	job.Attempt = nextAttempt
	return true, nil
}

func (s *SQLite) SetCheatsheetJobStatus(ctx context.Context, job *models.CheatsheetJob, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE cheatsheet_jobs SET status = ?
		WHERE vacation_id = ? AND source_language = ? AND destination_key = ? AND job_key = ?`,
		status, job.VacationID, job.SourceLanguage, job.DestinationKey, job.Key)
	return err
}

func (s *SQLite) ClaimCheatsheetJob(ctx context.Context, job *models.CheatsheetJob) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE cheatsheet_jobs SET status = 'running'
		WHERE vacation_id = ? AND source_language = ? AND destination_key = ? AND job_key = ? AND status = 'queued'`,
		job.VacationID, job.SourceLanguage, job.DestinationKey, job.Key)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (s *SQLite) ListQueuedCheatsheetJobs(ctx context.Context) ([]models.CheatsheetJob, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT vacation_id, source_language, destination_key, job_key, status
		FROM cheatsheet_jobs WHERE status = 'queued' AND job_key = 'sheet' ORDER BY created_at LIMIT 16`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var jobs []models.CheatsheetJob
	for rows.Next() {
		var job models.CheatsheetJob
		if err := rows.Scan(&job.VacationID, &job.SourceLanguage, &job.DestinationKey, &job.Key, &job.Status); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// A running job at restart might already have reached the provider. Never replay
// it automatically; queued jobs, which have not been sent, remain safe to resume.
func (s *SQLite) InterruptCheatsheetJobs(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE cheatsheet_jobs SET status = 'failed' WHERE status = 'running'`)
	return err
}

func (s *SQLite) ListCustomCheatsheetPhrases(ctx context.Context, profile *models.CustomTravelPhrase) ([]models.CustomTravelPhrase, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT original, translation, pronunciation FROM cheatsheet_custom_phrases
		WHERE vacation_id = ? AND source_language = ? AND destination_key = ? AND target_language = ?
		ORDER BY created_at, original`,
		profile.VacationID, profile.SourceLanguage, profile.DestinationKey, profile.TargetLanguage)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var phrases []models.CustomTravelPhrase
	for rows.Next() {
		phrase := *profile
		if err := rows.Scan(&phrase.Original, &phrase.Text, &phrase.Pronunciation); err != nil {
			return nil, err
		}
		phrases = append(phrases, phrase)
	}
	return phrases, rows.Err()
}

func (s *SQLite) PutCustomCheatsheetPhrase(ctx context.Context, phrase *models.CustomTravelPhrase) error {
	if err := phrase.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkCheatsheetDestination(ctx, tx, phrase.VacationID, phrase.DestinationKey); err != nil {
		return err
	}
	var content string
	err = tx.QueryRowContext(ctx, `SELECT content FROM cheatsheets WHERE vacation_id = ? AND source_language = ? AND destination_key = ?`,
		phrase.VacationID, phrase.SourceLanguage, phrase.DestinationKey).Scan(&content)
	if err != nil {
		return err
	}
	var sheet models.Cheatsheet
	if err := json.Unmarshal([]byte(content), &sheet); err != nil {
		return err
	}
	if sheet.Language != phrase.TargetLanguage {
		return ErrCheatsheetDestinationChanged
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO cheatsheet_custom_phrases
		(vacation_id, source_language, destination_key, target_language, original, translation, pronunciation, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
		phrase.VacationID, phrase.SourceLanguage, phrase.DestinationKey, phrase.TargetLanguage,
		phrase.Original, phrase.Text, phrase.Pronunciation, dbTime(time.Now().UTC()))
	if err != nil {
		return err
	}
	return tx.Commit()
}
