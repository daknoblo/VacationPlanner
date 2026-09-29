package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/google/uuid"
)

const locationSuggestionSources = ` FROM item_location_suggestions p
	JOIN items i ON i.id = p.item_id JOIN vacations v ON v.id = i.vacation_id
	WHERE i.latitude IS NULL AND i.longitude IS NULL
	AND i.title = p.title AND i.category = p.category AND i.location = p.location AND i.region = p.region
	AND i.region_manual = p.region_manual AND i.links = p.links
	AND v.destination = p.destination AND v.latitude IS p.destination_lat AND v.longitude IS p.destination_lng`

func (s *SQLite) ListLocationSuggestions(ctx context.Context, vacationID uuid.UUID) (map[uuid.UUID]models.LocationSuggestion, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id, p.item_id, p.label, p.name, p.latitude, p.longitude, p.rejected`+
		locationSuggestionSources+` AND i.vacation_id = ?`, vacationID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[uuid.UUID]models.LocationSuggestion)
	for rows.Next() {
		var p models.LocationSuggestion
		if err := rows.Scan(&p.ID, &p.ItemID, &p.Label, &p.Name, &p.Latitude, &p.Longitude, &p.Rejected); err != nil {
			return nil, err
		}
		out[p.ItemID] = p
	}
	return out, rows.Err()
}

func (s *SQLite) SaveLocationSuggestion(ctx context.Context, item *models.Item, vacation *models.Vacation, candidate models.LocationSuggestion) (bool, error) {
	if item == nil || vacation == nil || item.VacationID != vacation.ID {
		return false, fmt.Errorf("store: inconsistent location suggestion source")
	}
	if item.Latitude != nil || item.Longitude != nil {
		return false, nil
	}
	if strings.TrimSpace(candidate.Label) == "" || !utf8.ValidString(candidate.Label) || !utf8.ValidString(candidate.Name) ||
		utf8.RuneCountInString(candidate.Label) > 200 || utf8.RuneCountInString(candidate.Name) > 200 ||
		math.IsNaN(candidate.Latitude) || math.IsNaN(candidate.Longitude) ||
		math.IsInf(candidate.Latitude, 0) || math.IsInf(candidate.Longitude, 0) ||
		math.Abs(candidate.Latitude) > 90 || math.Abs(candidate.Longitude) > 180 {
		return false, fmt.Errorf("store: invalid location suggestion")
	}
	links, err := encodeItemLinks(item.Links)
	if err != nil {
		return false, err
	}
	source, err := json.Marshal([]any{item.Title, item.Category, item.Location, item.Region, item.RegionManual, links,
		vacation.Destination, vacation.Latitude, vacation.Longitude})
	if err != nil {
		return false, err
	}
	key := fmt.Sprintf("%x", sha256.Sum256(source))
	result, err := s.db.ExecContext(ctx, `INSERT INTO item_location_suggestions
		(item_id,id,source_key,title,category,location,region,region_manual,links,destination,destination_lat,destination_lng,
		 label,name,latitude,longitude)
		SELECT i.id, ?, ?, i.title,i.category,i.location,i.region,i.region_manual,i.links,v.destination,v.latitude,v.longitude,?,?,?,?
		FROM items i JOIN vacations v ON v.id = i.vacation_id
		WHERE i.id = ? AND i.vacation_id = ? AND i.title = ? AND i.category = ? AND i.location = ?
		AND i.latitude IS NULL AND i.longitude IS NULL AND i.region = ? AND i.region_manual = ? AND i.links = ?
		AND v.destination = ? AND v.latitude IS ? AND v.longitude IS ?
		ON CONFLICT(item_id) DO UPDATE SET
		id=excluded.id,source_key=excluded.source_key,title=excluded.title,category=excluded.category,location=excluded.location,
		region=excluded.region,region_manual=excluded.region_manual,links=excluded.links,
		destination=excluded.destination,destination_lat=excluded.destination_lat,destination_lng=excluded.destination_lng,
		label=excluded.label,name=excluded.name,latitude=excluded.latitude,longitude=excluded.longitude,rejected=0
		WHERE item_location_suggestions.source_key <> excluded.source_key`,
		uuid.New(), key, candidate.Label, candidate.Name, candidate.Latitude, candidate.Longitude,
		item.ID, item.VacationID, item.Title, item.Category, item.Location, item.Region, item.RegionManual, links,
		vacation.Destination, vacation.Latitude, vacation.Longitude)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (s *SQLite) RejectLocationSuggestion(ctx context.Context, itemID, suggestionID uuid.UUID) error {
	result, err := s.db.ExecContext(ctx, `UPDATE item_location_suggestions SET rejected = 1
		WHERE item_id = ? AND id = ? AND item_id IN (SELECT p.item_id`+locationSuggestionSources+`)`, itemID, suggestionID)
	if err != nil {
		return err
	}
	return checkAffected(result)
}
