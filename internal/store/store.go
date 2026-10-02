// Package store defines the persistence layer for the vacation planner.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/models"
)

// ErrNotFound is returned when a requested entity does not exist.
var ErrNotFound = errors.New("store: not found")
var ErrVacationNotEnded = errors.New("store: vacation has not ended")

// Store is the persistence contract used by the HTTP handlers.
type Store interface {
	Ping(ctx context.Context) error
	Close()

	CreateVacation(ctx context.Context, v *models.Vacation) error
	GetVacation(ctx context.Context, id uuid.UUID) (*models.Vacation, error)
	ListVacations(ctx context.Context) ([]models.Vacation, error)
	UpdateVacation(ctx context.Context, v *models.Vacation) error
	ArchiveVacation(ctx context.Context, id uuid.UUID, today time.Time) error
	DeleteVacation(ctx context.Context, id uuid.UUID) error
	SpendByVacation(ctx context.Context) (map[uuid.UUID]float64, error)

	CreateItem(ctx context.Context, i *models.Item) error
	GetItem(ctx context.Context, id uuid.UUID) (*models.Item, error)
	ListItems(ctx context.Context, vacationID uuid.UUID) ([]models.Item, error)
	UpdateItem(ctx context.Context, i *models.Item) error
	ScheduleItemDay(ctx context.Context, id uuid.UUID, day time.Time) error
	ScheduleItemRange(ctx context.Context, id uuid.UUID, day time.Time, start, end int) error
	DeleteItem(ctx context.Context, id uuid.UUID) error
	NextIdeaRoute(ctx context.Context, provider string) (*models.IdeaRoute, error)
	UnlocatedRouteVacations(ctx context.Context) ([]uuid.UUID, error)
	IdeaRouteProgress(ctx context.Context, provider string, vacationID uuid.UUID) (models.IdeaRouteProgress, error)
	ListIdeaRoutes(ctx context.Context, provider string, vacationID, lodgingID uuid.UUID) ([]models.IdeaRoute, error)
	PutIdeaRoute(ctx context.Context, job *models.IdeaRoute) (bool, error)
	RetryIdeaRoutes(ctx context.Context, vacationID uuid.UUID) error
	ClaimIdeaDescription(ctx context.Context) (*models.IdeaDescription, error)
	FinishIdeaDescription(ctx context.Context, job *models.IdeaDescription) error
	InterruptIdeaDescriptions(ctx context.Context) error
	ListIdeaDescriptions(ctx context.Context, vacationID uuid.UUID) (map[uuid.UUID]models.IdeaDescription, error)
	CountPendingIdeaDescriptions(ctx context.Context) (int, error)

	CreateTravelSegment(ctx context.Context, t *models.TravelSegment) error
	UpsertTravelSegment(ctx context.Context, t *models.TravelSegment) error
	ListTravelSegments(ctx context.Context, vacationID uuid.UUID) ([]models.TravelSegment, error)
	DeleteTravelSegment(ctx context.Context, id uuid.UUID) error

	ListCategories(ctx context.Context) ([]models.Category, error)
	CreateCategory(ctx context.Context, c *models.Category) error
	DeleteCategory(ctx context.Context, id uuid.UUID) error

	ListPeople(ctx context.Context) ([]models.Person, error)
	CreatePerson(ctx context.Context, p *models.Person) error
	DeletePerson(ctx context.Context, id uuid.UUID) error
	ListVacationParticipants(ctx context.Context, vacationID uuid.UUID) ([]models.Person, error)
	SetVacationParticipants(ctx context.Context, vacationID uuid.UUID, personIDs []uuid.UUID) error

	CreateLodging(ctx context.Context, l *models.Lodging) error
	UpdateLodging(ctx context.Context, l *models.Lodging) error
	UpdateLodgingCoordinates(ctx context.Context, original *models.Lodging, lat, lng float64) (bool, error)
	UpdateLodgingRegion(ctx context.Context, original *models.Lodging, region string) (bool, error)
	UpdateItemRegion(ctx context.Context, original *models.Item, region string) (bool, error)
	UpdateItemGeography(ctx context.Context, original *models.Item, vacation *models.Vacation, lat, lng float64, location, region string) (bool, error)
	ListLocationSuggestions(ctx context.Context, vacationID uuid.UUID) (map[uuid.UUID]models.LocationSuggestion, error)
	SaveLocationSuggestion(ctx context.Context, item *models.Item, vacation *models.Vacation, candidate models.LocationSuggestion) (bool, error)
	RejectLocationSuggestion(ctx context.Context, itemID, suggestionID uuid.UUID) error
	GetLodging(ctx context.Context, id uuid.UUID) (*models.Lodging, error)
	ListLodgings(ctx context.Context, vacationID uuid.UUID) ([]models.Lodging, error)
	DeleteLodging(ctx context.Context, id uuid.UUID) error

	CreateDocument(ctx context.Context, d *models.Document) error
	GetDocument(ctx context.Context, id uuid.UUID) (*models.Document, error)
	ReadDocument(ctx context.Context, id uuid.UUID) (*models.Document, error)
	ListItemDocuments(ctx context.Context, itemID uuid.UUID) ([]models.Document, error)
	ListTravelDocuments(ctx context.Context, vacationID uuid.UUID, kind models.TravelKind, step int) ([]models.Document, error)
	ListLodgingDocuments(ctx context.Context, lodgingID uuid.UUID) ([]models.Document, error)
	DeleteDocument(ctx context.Context, id uuid.UUID) error
	DeleteTravelStepDocuments(ctx context.Context, vacationID uuid.UUID, kind models.TravelKind, step int) error

	GetSettings(ctx context.Context) (map[string]string, error)
	PutSetting(ctx context.Context, key, value string) error
	ListWeather(ctx context.Context) (map[string]models.WeatherCache, error)
	QueueWeather(ctx context.Context, cache models.WeatherCache, before, now time.Time) (bool, error)
	ClaimWeather(ctx context.Context) (string, error)
	FinishWeather(ctx context.Context, cache models.WeatherCache) error
	InterruptWeather(ctx context.Context) error
	GetCheatsheet(ctx context.Context, vacationID uuid.UUID, sourceLanguage string) (*models.Cheatsheet, error)
	PutCheatsheet(ctx context.Context, sheet *models.Cheatsheet) error
	PutCheatsheetIntroduction(ctx context.Context, profile *models.CustomTravelPhrase, phrase *models.IntroductionPhrase) error
	ListCheatsheetsForIntroductions(ctx context.Context, after string) ([]models.Cheatsheet, error)
	GetCheatsheetJob(ctx context.Context, job *models.CheatsheetJob) (string, error)
	ReserveCheatsheetJob(ctx context.Context, job *models.CheatsheetJob, retry bool) (bool, error)
	SetCheatsheetJobStatus(ctx context.Context, job *models.CheatsheetJob, status string) error
	ClaimCheatsheetJob(ctx context.Context, job *models.CheatsheetJob) (bool, error)
	ListQueuedCheatsheetJobs(ctx context.Context) ([]models.CheatsheetJob, error)
	CountPendingCheatsheetJobs(ctx context.Context) (int, error)
	InterruptCheatsheetJobs(ctx context.Context) error
	ListCustomCheatsheetPhrases(ctx context.Context, profile *models.CustomTravelPhrase) ([]models.CustomTravelPhrase, error)
	ListCheatsheetPhraseJobs(ctx context.Context, profile *models.CustomTravelPhrase) ([]models.CheatsheetJob, error)
	PutCustomCheatsheetPhrase(ctx context.Context, phrase *models.CustomTravelPhrase) error

	Stats(ctx context.Context) (Stats, error)
	BackupTo(ctx context.Context, dest string) error
	Restore(ctx context.Context, srcPath string) error
	Vacuum(ctx context.Context) error
}
