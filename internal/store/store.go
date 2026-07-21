// Package store defines the persistence layer for the vacation planner.
package store

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/models"
)

// ErrNotFound is returned when a requested entity does not exist.
var ErrNotFound = errors.New("store: not found")

// Store is the persistence contract used by the HTTP handlers.
type Store interface {
	Ping(ctx context.Context) error
	Close()

	CreateVacation(ctx context.Context, v *models.Vacation) error
	GetVacation(ctx context.Context, id uuid.UUID) (*models.Vacation, error)
	ListVacations(ctx context.Context) ([]models.Vacation, error)
	UpdateVacation(ctx context.Context, v *models.Vacation) error
	DeleteVacation(ctx context.Context, id uuid.UUID) error

	CreateSight(ctx context.Context, s *models.Sight) error
	GetSight(ctx context.Context, id uuid.UUID) (*models.Sight, error)
	ListSights(ctx context.Context, vacationID uuid.UUID) ([]models.Sight, error)
	UpdateSight(ctx context.Context, s *models.Sight) error
	DeleteSight(ctx context.Context, id uuid.UUID) error

	CreateActivity(ctx context.Context, a *models.Activity) error
	GetActivity(ctx context.Context, id uuid.UUID) (*models.Activity, error)
	ListActivities(ctx context.Context, vacationID uuid.UUID) ([]models.Activity, error)
	UpdateActivity(ctx context.Context, a *models.Activity) error
	DeleteActivity(ctx context.Context, id uuid.UUID) error

	CreateTravelSegment(ctx context.Context, t *models.TravelSegment) error
	ListTravelSegments(ctx context.Context, vacationID uuid.UUID) ([]models.TravelSegment, error)
	DeleteTravelSegment(ctx context.Context, id uuid.UUID) error

	GetSettings(ctx context.Context) (map[string]string, error)
	PutSetting(ctx context.Context, key, value string) error

	Stats(ctx context.Context) (Stats, error)
	BackupTo(ctx context.Context, dest string) error
	Restore(ctx context.Context, srcPath string) error
}
