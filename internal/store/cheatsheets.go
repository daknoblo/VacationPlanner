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
	_, err = s.db.ExecContext(ctx, `INSERT INTO cheatsheets
		(vacation_id, source_language, destination_key, content, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(vacation_id, source_language) DO UPDATE SET
		destination_key = excluded.destination_key, content = excluded.content, created_at = excluded.created_at`,
		sheet.VacationID, sheet.SourceLanguage, sheet.DestinationKey, string(content), dbTime(sheet.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: saving cheatsheet: %w", err)
	}
	return nil
}
