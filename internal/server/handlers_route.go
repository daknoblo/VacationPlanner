package server

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/route"
)

const settingRouteBaseURL = "route.base_url"

// routeBaseURL returns the configured routing base URL or the built-in default.
func (s *Server) routeBaseURL(ctx context.Context) string {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return route.DefaultBaseURL
	}
	if v := strings.TrimSpace(settings[settingRouteBaseURL]); v != "" {
		return v
	}
	return route.DefaultBaseURL
}

// handleUpdateRouteSettings persists the routing base URL. The optional API key
// is never stored here; it comes from ROUTER_API_KEY.
func (s *Server) handleUpdateRouteSettings(w http.ResponseWriter, r *http.Request) {
	loc := i18n.FromContext(r.Context())
	baseURL := formStr(r, "base_url")
	if !maxLen(baseURL, 500) {
		s.formError(w, r, "#route-settings-error", loc.T("error.input_toolong"))
		return
	}
	if baseURL != "" && !validBaseURL(baseURL) {
		s.formError(w, r, "#route-settings-error", loc.T("error.ai_base_url_invalid"))
		return
	}
	if err := s.store.PutSetting(r.Context(), settingRouteBaseURL, baseURL); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.settingSaved(w, r)
}

// daySummaryNode is one stop on a day's timeline (hotel or a planned item).
type daySummaryNode struct {
	id    string
	label string // already sanitized; may contain <br/>
	pt    *route.Point
}

// daySummaryView is the rendered day summary passed to the template.
type daySummaryView struct {
	Empty   bool
	Mermaid string
	Lines   []string
	Approx  bool
}

// handleDaySummary builds a full-width route diagram (Hotel → stops → Hotel)
// for a single day, with driving time/distance between located stops.
func (s *Server) handleDaySummary(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	v, err := s.store.GetVacation(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	loc := i18n.FromContext(r.Context())
	day := parseDayParam(r)
	if day == nil {
		s.fragment(w, r, "day_summary", daySummaryView{Empty: true})
		return
	}

	items, err := s.store.ListItems(r.Context(), id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var dayItems []models.Item
	for _, it := range items {
		if it.OnDay(*day) && it.Timed() {
			dayItems = append(dayItems, it)
		}
	}
	if len(dayItems) == 0 {
		s.fragment(w, r, "day_summary", daySummaryView{Empty: true})
		return
	}

	icons := s.categoryIcons(r.Context())

	// Visual chain: Hotel? + timed items + Hotel?.
	var hotelPt *route.Point
	if v.HasCoords() {
		hotelPt = &route.Point{Lat: *v.Latitude, Lng: *v.Longitude}
	}
	hotelLabel := "🏨 " + sanitizeMermaid(loc.T("day.hotel"))

	var nodes []daySummaryNode
	if hotelPt != nil {
		nodes = append(nodes, daySummaryNode{label: hotelLabel, pt: hotelPt})
	}
	for _, it := range dayItems {
		icon := icons[strings.ToLower(it.Category)]
		if icon == "" {
			icon = "📍"
		}
		label := icon + " " + sanitizeMermaid(it.Title) + "<br/>" + it.StartLabel() + "–" + it.EndLabel()
		var pt *route.Point
		if it.HasCoords() {
			pt = &route.Point{Lat: *it.Latitude, Lng: *it.Longitude}
		}
		nodes = append(nodes, daySummaryNode{label: label, pt: pt})
	}
	if hotelPt != nil {
		nodes = append(nodes, daySummaryNode{label: hotelLabel, pt: hotelPt})
	}
	for i := range nodes {
		nodes[i].id = fmt.Sprintf("n%d", i)
	}

	// Located stops (subset) drive the routing.
	var coordIdx []int
	var coordPts []route.Point
	for i, n := range nodes {
		if n.pt != nil {
			coordIdx = append(coordIdx, i)
			coordPts = append(coordPts, *n.pt)
		}
	}

	// legByStop[k] is the drive from located stop k-1 to located stop k.
	legDist := make(map[int]float64)
	legDur := make(map[int]float64)
	approx := true
	if len(coordPts) >= 2 {
		if s.routing.Enabled() {
			if res, rerr := s.routing.Route(r.Context(), s.routeBaseURL(r.Context()), route.DefaultProfile, coordPts); rerr == nil && len(res.Legs) == len(coordPts)-1 {
				approx = false
				for k, leg := range res.Legs {
					legDist[k+1] = leg.DistanceM
					legDur[k+1] = leg.DurationS
				}
			} else if rerr != nil {
				s.log.Warn("day route failed", "err", rerr)
			}
		}
		if approx {
			for k := 1; k < len(coordPts); k++ {
				legDist[k] = route.Haversine(coordPts[k-1], coordPts[k])
			}
		}
	}

	// Map a node index to its position among located stops (for edge labels).
	posOf := make(map[int]int, len(coordIdx))
	for k, ni := range coordIdx {
		posOf[ni] = k
	}

	var b strings.Builder
	b.WriteString("flowchart LR\n")
	for _, n := range nodes {
		b.WriteString("  " + n.id + "[\"" + n.label + "\"]\n")
	}
	for i := 0; i < len(nodes)-1; i++ {
		label := ""
		if k, ok := posOf[i+1]; ok && k >= 1 {
			if d, has := legDist[k]; has {
				label = legLabel(d, legDur[k], approx, loc)
			}
		}
		if label != "" {
			b.WriteString("  " + nodes[i].id + " -->|\"" + label + "\"| " + nodes[i+1].id + "\n")
		} else {
			b.WriteString("  " + nodes[i].id + " --> " + nodes[i+1].id + "\n")
		}
	}

	lines := make([]string, 0, len(dayItems))
	for _, it := range dayItems {
		line := it.StartLabel() + "–" + it.EndLabel() + " · " + it.Title
		if it.Category != "" {
			line += " (" + it.Category + ")"
		}
		lines = append(lines, line)
	}

	s.fragment(w, r, "day_summary", daySummaryView{
		Mermaid: b.String(),
		Lines:   lines,
		Approx:  approx && len(coordPts) >= 2,
	})
}

// categoryIcons maps lower-cased category names to their emoji icon.
func (s *Server) categoryIcons(ctx context.Context) map[string]string {
	out := make(map[string]string)
	cats, err := s.store.ListCategories(ctx)
	if err != nil {
		return out
	}
	for _, c := range cats {
		if c.Icon != "" {
			out[strings.ToLower(c.Name)] = c.Icon
		}
	}
	return out
}

func legLabel(distM, durS float64, approx bool, _ *i18n.Localizer) string {
	dist := formatDistance(distM)
	if approx || durS <= 0 {
		return "📏 ~" + dist
	}
	return "🚗 " + formatDuration(durS) + " · " + dist
}

func formatDistance(m float64) string {
	if m < 1000 {
		return fmt.Sprintf("%.0f m", m)
	}
	return fmt.Sprintf("%.1f km", m/1000)
}

func formatDuration(sec float64) string {
	mins := int(math.Round(sec / 60))
	if mins < 60 {
		return fmt.Sprintf("%d min", mins)
	}
	h, m := mins/60, mins%60
	if m == 0 {
		return fmt.Sprintf("%d h", h)
	}
	return fmt.Sprintf("%d h %d min", h, m)
}

// sanitizeMermaid strips characters that would break Mermaid's flowchart syntax
// or allow markup injection, and caps the length for readable node labels.
func sanitizeMermaid(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '"', '[', ']', '{', '}', '|', '<', '>', '`', '\\', '\n', '\r':
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 40 {
		s = string(r[:40]) + "…"
	}
	if s == "" {
		s = "•"
	}
	return s
}
