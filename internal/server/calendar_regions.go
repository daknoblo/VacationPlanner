package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

type calendarRegionDay struct {
	Label string `json:"label"`
	Title string `json:"title"`
}

type calendarRegionSpan struct {
	calendarRegionDay
	Start int `json:"start"`
	Span  int `json:"span"`
}

type calendarRegionView struct {
	Days  map[string]calendarRegionDay    `json:"days"`
	Weeks map[string][]calendarRegionSpan `json:"weeks"`
}

// Regions follow accommodation calendar dates in the display timezone. The
// checkout date is included, so a transfer day retains both booked regions.
func calendarRegions(loc *i18n.Localizer, tz *time.Location, mondayStart bool, v *models.Vacation) calendarRegionView {
	view := calendarRegionView{
		Days: make(map[string]calendarRegionDay), Weeks: make(map[string][]calendarRegionSpan),
	}
	lodgings := append([]models.Lodging(nil), v.Lodgings...)
	sort.SliceStable(lodgings, func(i, j int) bool {
		if !lodgings[i].CheckIn.Equal(lodgings[j].CheckIn) {
			return lodgings[i].CheckIn.Before(lodgings[j].CheckIn)
		}
		return lodgings[i].ID.String() < lodgings[j].ID.String()
	})
	var spanNames []string
	for _, d := range v.Days() {
		day := d.Format("2006-01-02")
		var regions, names []string
		seen := make(map[string]bool)
		for _, lodging := range lodgings {
			if !lodging.CheckOut.After(lodging.CheckIn) ||
				day < lodging.CheckIn.In(tz).Format("2006-01-02") ||
				day > lodging.CheckOut.In(tz).Format("2006-01-02") {
				continue
			}
			region := strings.TrimSpace(lodging.Region)
			if region == "" {
				region = loc.T("planner.regions.unknown")
			}
			key := strings.ToLower(region)
			if !seen[key] {
				regions = append(regions, region)
				seen[key] = true
			}
			names = append(names, lodging.Name+" · "+region)
		}
		entry := calendarRegionDay{Label: strings.Join(regions, " · "), Title: strings.Join(names, "; ")}
		if len(names) == 0 {
			entry.Label = loc.T("planner.regions.no_lodging")
			entry.Title = entry.Label
		}
		view.Days[day] = entry
		col := weekdayCol(d, mondayStart)
		week := d.AddDate(0, 0, -col).Format("2006-01-02")
		spans := view.Weeks[week]
		if len(spans) > 0 && spans[len(spans)-1].Label == entry.Label {
			last := &spans[len(spans)-1]
			last.Span++
			for _, name := range names {
				if !slices.Contains(spanNames, name) {
					spanNames = append(spanNames, name)
				}
			}
			if len(spanNames) > 0 {
				last.Title = strings.Join(spanNames, "; ")
			}
		} else {
			spans = append(spans, calendarRegionSpan{calendarRegionDay: entry, Start: col, Span: 1})
			spanNames = append([]string(nil), names...)
		}
		view.Weeks[week] = spans
	}
	return view
}

func (s *Server) handleCalendarRegions(w http.ResponseWriter, r *http.Request) {
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
	v.Lodgings, err = s.store.ListLodgings(r.Context(), id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	weekStart, tz := s.regionSettings(r.Context())
	view := calendarRegions(i18n.FromContext(r.Context()), tz, weekStart != "sunday", v)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(view); err != nil {
		s.log.Error("encoding calendar regions", "err", err)
	}
}
