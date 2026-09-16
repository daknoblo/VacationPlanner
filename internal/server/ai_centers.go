package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

type aiSearchCenter struct {
	Key   string   `json:"key"`
	Label string   `json:"label"`
	Name  string   `json:"name"`
	Lat   *float64 `json:"lat"`
	Lng   *float64 `json:"lng"`
	Zoom  int      `json:"zoom"`
}

func aiSearchCenters(loc *i18n.Localizer, vacation *models.Vacation, lodgings []models.Lodging) []aiSearchCenter {
	centers := []aiSearchCenter{{
		Key: "destination", Label: loc.T("ai.center.destination", vacation.Destination), Name: vacation.Destination,
		Lat: vacation.Latitude, Lng: vacation.Longitude, Zoom: 6,
	}}
	type regionPoints struct {
		name    string
		x, y, z float64
		count   int
	}
	regions := make(map[string]*regionPoints)
	for _, lodging := range lodgings {
		name := strings.TrimSpace(lodging.Region)
		if name == "" || !lodging.HasCoords() || math.IsNaN(*lodging.Latitude) || math.IsNaN(*lodging.Longitude) ||
			math.Abs(*lodging.Latitude) > 90 || math.Abs(*lodging.Longitude) > 180 {
			continue
		}
		key := strings.ToLower(name)
		group := regions[key]
		if group == nil {
			group = &regionPoints{name: name}
			regions[key] = group
		}
		lat, lng := *lodging.Latitude*math.Pi/180, *lodging.Longitude*math.Pi/180
		group.x += math.Cos(lat) * math.Cos(lng)
		group.y += math.Cos(lat) * math.Sin(lng)
		group.z += math.Sin(lat)
		group.count++
	}
	keys := make([]string, 0, len(regions))
	for key := range regions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := regions[key]
		horizontal := math.Hypot(group.x, group.y)
		// Antipodal points have no unique center. Keep the region visible but
		// without selectable coordinates instead of inventing an origin.
		center := aiSearchCenter{
			Key: fmt.Sprintf("region:%x", sha256.Sum256([]byte(key))), Label: group.name, Name: group.name, Zoom: 9,
		}
		if math.Hypot(horizontal, group.z)/float64(group.count) > 1e-9 {
			lat := math.Atan2(group.z, horizontal) * 180 / math.Pi
			lng := math.Atan2(group.y, group.x) * 180 / math.Pi
			center.Lat, center.Lng = &lat, &lng
		}
		centers = append(centers, center)
	}
	return append(centers, aiSearchCenter{Key: "custom", Label: loc.T("ai.center.custom"), Zoom: 9})
}

func (s *Server) handleAISearchCenters(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	v, err := s.store.GetVacation(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
		} else {
			s.serverError(w, r, err)
		}
		return
	}
	lodgings, err := s.store.ListLodgings(r.Context(), id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(aiSearchCenters(i18n.FromContext(r.Context()), v, lodgings)); err != nil {
		s.log.Error("encoding AI search centers", "err", err)
	}
}
