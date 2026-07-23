package server

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

// dashboardCard augments a vacation with the computed figures shown on its
// dashboard card: the planned spend (for the budget pie) and a countdown label.
type dashboardCard struct {
	models.Vacation
	Spent     float64
	HasBudget bool
	Percent   int
	Over      bool
	Countdown string
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	vacations, err := s.store.ListVacations(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	loc := i18n.FromContext(r.Context())
	_, tz := s.regionSettings(r.Context())
	now := time.Now().In(tz)

	cards := make([]dashboardCard, 0, len(vacations))
	for i := range vacations {
		v := vacations[i]
		card := dashboardCard{Vacation: v}
		items, ierr := s.store.ListItems(r.Context(), v.ID)
		if ierr != nil {
			s.serverError(w, r, ierr)
			return
		}
		for _, it := range items {
			if it.Cost != nil {
				card.Spent += *it.Cost
			}
		}
		lodgings, lerr := s.store.ListLodgings(r.Context(), v.ID)
		if lerr != nil {
			s.serverError(w, r, lerr)
			return
		}
		for _, lo := range lodgings {
			if lo.Cost != nil {
				card.Spent += *lo.Cost
			}
		}
		if v.Budget != nil && *v.Budget > 0 {
			card.HasBudget = true
			card.Over = card.Spent > *v.Budget
			pct := int(math.Round(card.Spent / *v.Budget * 100))
			if pct < 0 {
				pct = 0
			}
			if pct > 100 {
				pct = 100
			}
			card.Percent = pct
		}
		card.Countdown = countdownLabel(loc, now, v.StartDate, v.EndDate)
		cards = append(cards, card)
	}

	s.page(w, r, "index", loc.T("page.vacations.title"), map[string]any{
		"Cards": cards,
	})
}

// countdownLabel returns a short human label for the time until the trip starts,
// e.g. "in 17 days" or "in 2½ months"; once it has begun or ended it reports the
// trip as ongoing or past.
func countdownLabel(loc *i18n.Localizer, now, start, end time.Time) string {
	dateOnly := func(t time.Time) time.Time {
		y, m, d := t.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
	today := dateOnly(now)
	s := dateOnly(start)
	e := dateOnly(end)
	switch {
	case today.After(e):
		return loc.T("countdown.past")
	case !today.Before(s):
		return loc.T("countdown.ongoing")
	}
	days := int(math.Round(s.Sub(today).Hours() / 24))
	switch {
	case days <= 0:
		return loc.T("countdown.ongoing")
	case days == 1:
		return loc.T("countdown.tomorrow")
	case days < 45:
		return loc.T("countdown.in_days", days)
	default:
		months := float64(days) / 30.436875
		half := math.Round(months*2) / 2
		whole := int(half)
		label := strconv.Itoa(whole)
		if half-float64(whole) >= 0.25 {
			label += "½"
		}
		return loc.T("countdown.in_months", label)
	}
}

// budgetView is the computed budget breakdown shown on the Budget tab.
type budgetView struct {
	HasBudget      bool
	Currency       string
	Total          *float64
	Spent          float64
	Remaining      float64
	Over           bool
	PercentClamped int
	People         int
	Nights         int
	PerPerson      float64
	PerNight       float64

	// Spending statistics and breakdown.
	ExpenseCount   int
	AvgExpense     float64
	TopAmount      float64
	SpentPerPerson float64
	SpentPerNight  float64
	Categories     []budgetCategory
	Expenses       []budgetExpense
}

// budgetCategory is the total spent within one category.
type budgetCategory struct {
	Name    string
	Icon    string
	Amount  float64
	Percent int // share of total spending
}

// budgetExpense is a single costed item shown in the spending overview.
type budgetExpense struct {
	Title    string
	Icon     string
	DayLabel string
	Amount   float64
}

// newBudgetView computes the budget breakdown and spending statistics for a
// vacation from its items. icons maps lower-cased category names to emoji.
func newBudgetView(v *models.Vacation, items []models.Item, icons map[string]string, currency, lodgingLabel string) budgetView {
	b := budgetView{People: v.People, Nights: v.Nights(), Currency: currency}

	catAmount := map[string]float64{}
	var catOrder []string
	for _, it := range items {
		if it.Cost == nil {
			continue
		}
		amt := *it.Cost
		b.Spent += amt
		b.ExpenseCount++
		if amt > b.TopAmount {
			b.TopAmount = amt
		}
		if _, ok := catAmount[it.Category]; !ok {
			catOrder = append(catOrder, it.Category)
		}
		catAmount[it.Category] += amt

		day := ""
		if it.Day != nil {
			day = fmtDate(*it.Day)
		}
		b.Expenses = append(b.Expenses, budgetExpense{
			Title:    it.Title,
			Icon:     icons[strings.ToLower(it.Category)],
			DayLabel: day,
			Amount:   amt,
		})
	}

	// Accommodations count toward the budget under their own category.
	for _, lo := range v.Lodgings {
		if lo.Cost == nil {
			continue
		}
		amt := *lo.Cost
		b.Spent += amt
		b.ExpenseCount++
		if amt > b.TopAmount {
			b.TopAmount = amt
		}
		if _, ok := catAmount[lodgingLabel]; !ok {
			catOrder = append(catOrder, lodgingLabel)
		}
		catAmount[lodgingLabel] += amt
		b.Expenses = append(b.Expenses, budgetExpense{
			Title:    lo.Name,
			Icon:     "🛏",
			DayLabel: fmtDate(lo.CheckIn),
			Amount:   amt,
		})
	}

	if b.ExpenseCount > 0 {
		b.AvgExpense = b.Spent / float64(b.ExpenseCount)
	}
	if v.People > 0 {
		b.SpentPerPerson = b.Spent / float64(v.People)
	}
	if b.Nights > 0 {
		b.SpentPerNight = b.Spent / float64(b.Nights)
	}

	for _, name := range catOrder {
		amt := catAmount[name]
		pct := 0
		if b.Spent > 0 {
			pct = int(amt / b.Spent * 100)
		}
		icon := icons[strings.ToLower(name)]
		if name == lodgingLabel {
			icon = "🛏"
		}
		b.Categories = append(b.Categories, budgetCategory{
			Name: name, Icon: icon, Amount: amt, Percent: pct,
		})
	}
	sort.SliceStable(b.Categories, func(i, j int) bool { return b.Categories[i].Amount > b.Categories[j].Amount })
	sort.SliceStable(b.Expenses, func(i, j int) bool { return b.Expenses[i].Amount > b.Expenses[j].Amount })

	if v.Budget != nil {
		total := *v.Budget
		b.HasBudget = true
		b.Total = v.Budget
		b.Remaining = total - b.Spent
		b.Over = b.Spent > total
		if total > 0 {
			p := int(b.Spent / total * 100)
			if p < 0 {
				p = 0
			}
			if p > 100 {
				p = 100
			}
			b.PercentClamped = p
		}
		if v.People > 0 {
			b.PerPerson = total / float64(v.People)
		}
		if b.Nights > 0 {
			b.PerNight = total / float64(b.Nights)
		}
	}
	return b
}

func (s *Server) handleVacationDetail(w http.ResponseWriter, r *http.Request) {
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

	if v.TravelSegments, err = s.store.ListTravelSegments(r.Context(), id); err != nil {
		s.serverError(w, r, err)
		return
	}
	if v.Items, err = s.store.ListItems(r.Context(), id); err != nil {
		s.serverError(w, r, err)
		return
	}
	if v.Lodgings, err = s.store.ListLodgings(r.Context(), id); err != nil {
		s.serverError(w, r, err)
		return
	}

	categories, _ := s.store.ListCategories(r.Context())
	loc := i18n.FromContext(r.Context())
	weekStart, tz := s.regionSettings(r.Context())
	mondayStart := weekStart != "sunday"
	cardMap := s.dayCardMap(r.Context(), loc, v)
	activities, ideas := overviewFromCards(loc, tz, v, cardMap)
	currency := s.currencySymbol(r.Context())

	s.page(w, r, "vacation", v.Title, map[string]any{
		"Vacation":        v,
		"AIEnabled":       s.ai.Enabled(),
		"Budget":          newBudgetView(v, v.Items, s.categoryIcons(r.Context()), currency, loc.T("tab.lodging")),
		"Currency":        currency,
		"Categories":      categories,
		"HomeAddress":     s.homeAddress(r.Context()),
		"ActivityList":    activities,
		"Ideas":           ideas,
		"DayCards":        cardMap,
		"WeekCards":       weekCardGroups(mondayStart, v, cardMap),
		"CalTravel":       travelCalBlocks(loc, tz, v),
		"CalLodging":      lodgingDayStrips(tz, v.Lodgings),
		"Lodgings":        lodgingBlock(tz, v),
		"WeekCalendar":    buildWeekCalendar(loc, tz, mondayStart, v),
		"WeekHeaders":     calWeekdayHeaders(loc, mondayStart),
		"HourRows":        calHourRows(),
		"ArrivalTravel":   s.travelBlock(r.Context(), tz, v, models.TravelArrival),
		"DepartureTravel": s.travelBlock(r.Context(), tz, v, models.TravelDeparture),
		"ArrivalTotal":    travelTotalFor(v, models.TravelArrival, false),
		"DepartureTotal":  travelTotalFor(v, models.TravelDeparture, false),
	})
}

// findTravelSegment returns the vacation's segment of the given kind, or nil.
func findTravelSegment(v *models.Vacation, kind models.TravelKind) *models.TravelSegment {
	for i := range v.TravelSegments {
		if v.TravelSegments[i].Kind == kind {
			return &v.TravelSegments[i]
		}
	}
	return nil
}

// applyEndpointDefaults fills empty From/To locations (and their coordinates):
// the arrival defaults to home → destination, and the departure to the reverse
// of the arrival (falling back to destination → home when the arrival is empty).
func (s *Server) applyEndpointDefaults(ctx context.Context, v *models.Vacation, seg *models.TravelSegment) {
	home := s.homeAddress(ctx)
	var fromLoc, toLoc string
	var fromLat, fromLng, toLat, toLng *float64

	switch seg.Kind {
	case models.TravelArrival:
		fromLoc = home
		toLoc, toLat, toLng = v.Destination, v.Latitude, v.Longitude
	case models.TravelDeparture:
		arr := findTravelSegment(v, models.TravelArrival)
		if arr != nil && arr.ToLocation != "" {
			fromLoc, fromLat, fromLng = arr.ToLocation, arr.ToLat, arr.ToLng
		} else {
			fromLoc, fromLat, fromLng = v.Destination, v.Latitude, v.Longitude
		}
		if arr != nil && arr.FromLocation != "" {
			toLoc, toLat, toLng = arr.FromLocation, arr.FromLat, arr.FromLng
		} else {
			toLoc = home
		}
	}

	if seg.FromLocation == "" {
		seg.FromLocation, seg.FromLat, seg.FromLng = fromLoc, fromLat, fromLng
	}
	if seg.ToLocation == "" {
		seg.ToLocation, seg.ToLat, seg.ToLng = toLoc, toLat, toLng
	}
}

// overviewActivity is a scheduled entry shown in the Overview activity list and
// in the day/week card lists. Travel rows carry the leg totals; item rows carry
// the origin (start point) plus the distance and time to reach it.
type overviewActivity struct {
	ItemID        string // "" for travel rows (no origin picker)
	Weekday       string
	DateLabel     string
	TimeLabel     string
	Title         string
	Category      string
	DistanceLabel string // travel: total leg distance; item: distance from the origin
	DurationLabel string // travel: total leg duration; item: time from the origin
	OriginLabel   string // item rows: the resolved start point (e.g. "🛏 Hotel Lisboa")
	Approx        bool   // duration is a straight-line estimate (item rows)
	IsTravel      bool
	DayKey        string         // dateInput of the item's day (scopes the origin picker)
	Origins       []originOption // predecessor options for the origin picker (item rows)
	Latitude      *float64
	Longitude     *float64
	HasCoords     bool
	sortKey       time.Time
}

// originOption is one selectable predecessor in an activity's origin picker.
type originOption struct {
	Value    string // "", "hotel" or an item UUID
	Label    string
	Selected bool
}

// weekdayLabel returns the localized weekday name for a date.
func weekdayLabel(loc *i18n.Localizer, t time.Time) string {
	return loc.T("weekday." + strings.ToLower(t.Weekday().String()))
}

// calTravelBlock is a read-only travel leg rendered on the day/week calendar.
type calTravelBlock struct {
	StartMin int
	EndMin   int
	Title    string
	Label    string
}

// travelLabel builds a human label for a travel segment, e.g. "Arrival · BER → LIS".
func travelLabel(loc *i18n.Localizer, t models.TravelSegment) string {
	label := loc.T("travel.kind." + string(t.Kind))
	switch {
	case t.FromLocation != "" && t.ToLocation != "":
		label += " · " + t.FromLocation + " → " + t.ToLocation
	case t.ToLocation != "":
		label += " · " + t.ToLocation
	case t.FromLocation != "":
		label += " · " + t.FromLocation
	}
	return label
}

// modeLabel returns the localized travel mode name (empty when no mode is set).
func modeLabel(loc *i18n.Localizer, mode string) string {
	if mode == "" {
		return ""
	}
	return loc.T("travel.mode." + mode)
}

// travelCalBlocks groups travel legs by their departure day (in the display
// timezone) so the calendar can render them as read-only blocks.
func travelCalBlocks(loc *i18n.Localizer, tz *time.Location, v *models.Vacation) map[string][]calTravelBlock {
	out := make(map[string][]calTravelBlock)
	for _, ts := range v.TravelSegments {
		if ts.DepartAt == nil {
			continue
		}
		dep := ts.DepartAt.In(tz)
		day := dep.Format("2006-01-02")
		startMin := dep.Hour()*60 + dep.Minute()
		endMin := startMin + 60
		label := dep.Format("15:04")
		if ts.ArriveAt != nil {
			arr := ts.ArriveAt.In(tz)
			label += "–" + arr.Format("15:04")
			if arr.Format("2006-01-02") == day && arr.Hour()*60+arr.Minute() > startMin {
				endMin = arr.Hour()*60 + arr.Minute()
			} else if ts.ArriveAt.After(*ts.DepartAt) {
				endMin = 1440
			}
		}
		if endMin > 1440 {
			endMin = 1440
		}
		out[day] = append(out[day], calTravelBlock{StartMin: startMin, EndMin: endMin, Title: travelLabel(loc, ts), Label: label})
	}
	return out
}

// ---- Week calendar (calendar weeks, Mon–Sun rows) ----

const (
	calEarlyHourPx  = 16 // 0:00–6:00 rendered compressed (people sleep then)
	calNormalHourPx = 40 // 6:00–24:00 normal spacing
	calEarlyEndHour = 6
)

// calMinPx maps a minute-of-day to a vertical pixel offset, compressing the
// hours before 6:00 and using normal spacing afterwards.
func calMinPx(min int) int {
	if min < 0 {
		min = 0
	}
	if min > 1440 {
		min = 1440
	}
	const earlyEnd = calEarlyEndHour * 60
	if min <= earlyEnd {
		return int(math.Round(float64(min) * float64(calEarlyHourPx) / 60))
	}
	base := calEarlyEndHour * calEarlyHourPx
	return base + int(math.Round(float64(min-earlyEnd)*float64(calNormalHourPx)/60))
}

// calHourRow is one hour label on the week calendar's time axis.
type calHourRow struct {
	Label string
	Px    int
}

func calHourRows() []calHourRow {
	rows := make([]calHourRow, 24)
	for h := 0; h < 24; h++ {
		px := calNormalHourPx
		if h < calEarlyEndHour {
			px = calEarlyHourPx
		}
		rows[h] = calHourRow{Label: fmt.Sprintf("%d:00", h), Px: px}
	}
	return rows
}

// calWeekday is a column header for the week calendar.
type calWeekday struct {
	Label   string
	Weekend bool
}

// weekOrder returns the seven weekdays in display order for the configured week start.
func weekOrder(mondayStart bool) []time.Weekday {
	if mondayStart {
		return []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}
	}
	return []time.Weekday{time.Sunday, time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday}
}

func calWeekdayHeaders(loc *i18n.Localizer, mondayStart bool) []calWeekday {
	order := weekOrder(mondayStart)
	out := make([]calWeekday, len(order))
	for i, wd := range order {
		out[i] = calWeekday{
			Label:   loc.T("weekday." + strings.ToLower(wd.String())),
			Weekend: wd == time.Saturday || wd == time.Sunday,
		}
	}
	return out
}

// weekdayCol returns the 0-based column for a date under the configured week start.
func weekdayCol(d time.Time, mondayStart bool) int {
	if mondayStart {
		return (int(d.Weekday()) + 6) % 7
	}
	return int(d.Weekday())
}

// calBlock is a positioned block (item or travel) on the week calendar.
type calBlock struct {
	TopPx    int
	HeightPx int
	Title    string
	Label    string
	Travel   bool
}

// calDay is one occupied day cell in a calendar week.
type calDay struct {
	Date     time.Time
	DayIndex int
	Weekend  bool
	Blocks   []calBlock
	Lodging  []calBlock
}

// calWeek is one calendar-week row; Days is indexed by weekday column
// (nil where the day falls outside the trip).
type calWeek struct {
	Days [7]*calDay
}

// buildWeekCalendar groups the trip days into calendar weeks (Mon–Sun rows),
// placing each day in its weekday column with its timed items and travel legs.
func buildWeekCalendar(loc *i18n.Localizer, tz *time.Location, mondayStart bool, v *models.Vacation) []calWeek {
	travel := travelCalBlocks(loc, tz, v)
	lodging := lodgingDayStrips(tz, v.Lodgings)
	var weeks []calWeek
	idx := make(map[string]int)
	for i, d := range v.Days() {
		col := weekdayCol(d, mondayStart)
		key := d.AddDate(0, 0, -col).Format("2006-01-02")
		wi, ok := idx[key]
		if !ok {
			wi = len(weeks)
			idx[key] = wi
			weeks = append(weeks, calWeek{})
		}
		cd := &calDay{
			Date:     d,
			DayIndex: i,
			Weekend:  d.Weekday() == time.Saturday || d.Weekday() == time.Sunday,
		}
		for _, it := range v.Items {
			if it.OnDay(d) && it.Timed() {
				top := calMinPx(it.StartMin)
				cd.Blocks = append(cd.Blocks, calBlock{TopPx: top, HeightPx: calMinPx(it.EndMin) - top, Title: it.Title, Label: it.StartLabel()})
			}
		}
		for _, tb := range travel[d.Format("2006-01-02")] {
			top := calMinPx(tb.StartMin)
			cd.Blocks = append(cd.Blocks, calBlock{TopPx: top, HeightPx: calMinPx(tb.EndMin) - top, Title: tb.Title, Label: tb.Label, Travel: true})
		}
		for _, ls := range lodging[d.Format("2006-01-02")] {
			top := calMinPx(ls.StartMin)
			cd.Lodging = append(cd.Lodging, calBlock{TopPx: top, HeightPx: calMinPx(ls.EndMin) - top, Title: ls.Name})
		}
		weeks[wi].Days[col] = cd
	}
	return weeks
}

// overviewFromCards merges the travel legs (each direction collapsed into a
// single entry whose distance and duration are the sum of its legs) with the
// per-day item cards from cardMap, plus the unscheduled "ideas" bucket. The
// activity list is returned sorted chronologically.
func overviewFromCards(loc *i18n.Localizer, tz *time.Location, v *models.Vacation, cardMap map[string][]overviewActivity) (activities []overviewActivity, ideas []models.Item) {
	// Each travel direction (arrival/departure) is collapsed into a single entry
	// whose distance and duration are the sum of all its legs.
	for _, kind := range []models.TravelKind{models.TravelArrival, models.TravelDeparture} {
		legs := stepsForKind(v, kind)
		if len(legs) == 0 {
			continue
		}
		var distM float64
		var durS int
		var haveDist, haveDur bool
		for _, ts := range legs {
			if ts.DistanceM != nil {
				distM += *ts.DistanceM
				haveDist = true
			}
			if ts.DurationS != nil {
				durS += *ts.DurationS
				haveDur = true
			}
		}
		first, last := legs[0], legs[len(legs)-1]

		var date, tm string
		var key, wd time.Time
		switch {
		case first.DepartAt != nil:
			dep := first.DepartAt.In(tz)
			date, tm, key, wd = fmtDate(dep), dep.Format("15:04"), *first.DepartAt, dep
		case kind == models.TravelArrival:
			date, key, wd = fmtDate(v.StartDate), v.StartDate, v.StartDate
		default:
			date, key, wd = fmtDate(v.EndDate), v.EndDate.Add(24*time.Hour-time.Minute), v.EndDate
		}

		title := loc.T("travel.kind." + string(kind))
		switch {
		case first.FromLocation != "" && last.ToLocation != "":
			title += " · " + first.FromLocation + " → " + last.ToLocation
		case last.ToLocation != "":
			title += " · " + last.ToLocation
		case first.FromLocation != "":
			title += " · " + first.FromLocation
		}

		lat, lng := last.ToLat, last.ToLng
		if lat == nil {
			lat, lng = last.FromLat, last.FromLng
		}
		if lat == nil {
			lat, lng = first.FromLat, first.FromLng
		}

		cat := modeLabel(loc, first.Mode)
		if len(legs) > 1 {
			cat = loc.T("overview.legs", len(legs))
		}

		oa := overviewActivity{
			Weekday:   weekdayLabel(loc, wd),
			DateLabel: date,
			TimeLabel: tm,
			Title:     title,
			Category:  cat,
			IsTravel:  true,
			Latitude:  lat,
			Longitude: lng,
			HasCoords: lat != nil && lng != nil,
			sortKey:   key,
		}
		if haveDist {
			oa.DistanceLabel = formatDistance(distM)
		}
		if haveDur {
			oa.DurationLabel = formatDuration(float64(durS))
		}
		activities = append(activities, oa)
	}
	// Item cards, grouped by day and already carrying their origin/leg info.
	for _, it := range v.Items {
		if it.Day == nil {
			ideas = append(ideas, it)
		}
	}
	for _, cards := range cardMap {
		activities = append(activities, cards...)
	}
	sort.SliceStable(activities, func(i, j int) bool {
		return activities[i].sortKey.Before(activities[j].sortKey)
	})
	return activities, ideas
}

// handleBudgetFragment re-renders the budget panel so it can refresh after item
// changes without a full page reload.
func (s *Server) handleBudgetFragment(w http.ResponseWriter, r *http.Request) {
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
	items, err := s.store.ListItems(r.Context(), id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if v.Lodgings, err = s.store.ListLodgings(r.Context(), id); err != nil {
		s.serverError(w, r, err)
		return
	}
	loc := i18n.FromContext(r.Context())
	s.fragment(w, r, "budget_panel", newBudgetView(v, items, s.categoryIcons(r.Context()), s.currencySymbol(r.Context()), loc.T("tab.lodging")))
}

// handleOverviewFragment re-renders the overview activity list.
func (s *Server) handleOverviewFragment(w http.ResponseWriter, r *http.Request) {
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
	if v.TravelSegments, err = s.store.ListTravelSegments(r.Context(), id); err != nil {
		s.serverError(w, r, err)
		return
	}
	if v.Items, err = s.store.ListItems(r.Context(), id); err != nil {
		s.serverError(w, r, err)
		return
	}
	if v.Lodgings, err = s.store.ListLodgings(r.Context(), id); err != nil {
		s.serverError(w, r, err)
		return
	}
	loc := i18n.FromContext(r.Context())
	_, tz := s.regionSettings(r.Context())
	activities, _ := overviewFromCards(loc, tz, v, s.dayCardMap(r.Context(), loc, v))
	s.fragment(w, r, "overview_list", activities)
}

// handleDayCards renders the activity card list for a single day of the trip.
func (s *Server) handleDayCards(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	v, err := s.loadVacationFull(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	day := parseDayParam(r)
	if day == nil {
		s.fragment(w, r, "activity_cards", []overviewActivity(nil))
		return
	}
	var items []models.Item
	for _, it := range v.Items {
		if it.OnDay(*day) {
			items = append(items, it)
		}
	}
	loc := i18n.FromContext(r.Context())
	s.fragment(w, r, "activity_cards", s.dayCards(r.Context(), loc, *day, v, items))
}

// handleWeekCards renders the per-week activity card lists for the week view.
func (s *Server) handleWeekCards(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	v, err := s.loadVacationFull(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	loc := i18n.FromContext(r.Context())
	weekStart, _ := s.regionSettings(r.Context())
	mondayStart := weekStart != "sunday"
	s.fragment(w, r, "week_cards", weekCardGroups(mondayStart, v, s.dayCardMap(r.Context(), loc, v)))
}

// handleIdeasFragment re-renders the unscheduled ideas backlog.
func (s *Server) handleIdeasFragment(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	items, err := s.store.ListItems(r.Context(), id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var ideas []models.Item
	for _, it := range items {
		if it.Day == nil {
			ideas = append(ideas, it)
		}
	}
	s.fragment(w, r, "ideas_backlog", ideas)
}

// destinationInfoView is the Wikipedia intro shown under the map on the General tab.
type destinationInfoView struct {
	Destination string
	Description string
	Extract     string
	URL         string
}

// handleDestinationInfo renders a short Wikipedia intro for the destination.
func (s *Server) handleDestinationInfo(w http.ResponseWriter, r *http.Request) {
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
	view := destinationInfoView{Destination: v.Destination}
	if v.Destination != "" {
		lang := i18n.FromContext(r.Context()).Code()
		if sum, ok := s.destImg.Summary(r.Context(), v.Destination, lang); ok {
			view.Description = sum.Description
			view.Extract = sum.Extract
			view.URL = sum.URL
		}
	}
	s.fragment(w, r, "destination_info", view)
}

func (s *Server) handleCreateVacation(w http.ResponseWriter, r *http.Request) {
	v, err := s.vacationFromForm(r)
	if err != nil {
		s.formError(w, r, "#form-error", err.Error())
		return
	}
	if err := s.store.CreateVacation(r.Context(), v); err != nil {
		s.serverError(w, r, err)
		return
	}

	target := "/vacations/" + v.ID.String()
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) handleUpdateVacation(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}

	existing, err := s.store.GetVacation(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}

	updated, err := s.vacationFromForm(r)
	if err != nil {
		s.formError(w, r, "#meta-error", err.Error())
		return
	}
	updated.ID = existing.ID

	if err := s.store.UpdateVacation(r.Context(), updated); err != nil {
		s.serverError(w, r, err)
		return
	}

	if !isHTMX(r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Changing the dates regenerates the day plan, week calendar and travel
	// defaults, so a full refresh is needed. Other fields only affect the
	// header, which we update out-of-band to keep the current tab and scroll.
	if !existing.StartDate.Equal(updated.StartDate) || !existing.EndDate.Equal(updated.EndDate) {
		w.Header().Set("HX-Refresh", "true")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	events := "saved"
	if !strings.EqualFold(strings.TrimSpace(existing.Destination), strings.TrimSpace(updated.Destination)) {
		events += ", infoChanged"
	}
	hxTrigger(w, events)
	s.fragment(w, r, "detail_head", map[string]any{"V": updated, "OOB": true})
}

// handleUpdateNotes saves just the trip notes (used by the quick notes editor on
// the overview tab) without touching the other trip fields.
func (s *Server) handleUpdateNotes(w http.ResponseWriter, r *http.Request) {
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
	notes := formStr(r, "notes")
	if !maxLen(notes, 5000) {
		s.formError(w, r, "#overview-notes-error", loc.T("error.notes_toolong"))
		return
	}
	v.Notes = notes
	if err := s.store.UpdateVacation(r.Context(), v); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteVacation(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := s.store.DeleteVacation(r.Context(), id); err != nil && !isNotFound(err) {
		s.serverError(w, r, err)
		return
	}
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", "/")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleDeleteVacationSettings deletes a vacation from the Settings management
// list, removing just its row (no redirect) so the user stays on the page.
func (s *Server) handleDeleteVacationSettings(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := s.store.DeleteVacation(r.Context(), id); err != nil && !isNotFound(err) {
		s.serverError(w, r, err)
		return
	}
	if isHTMX(r) {
		// Empty body swaps out the row (outerHTML).
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// vacationFromForm parses and validates the shared create/update form.
func (s *Server) vacationFromForm(r *http.Request) (*models.Vacation, error) {
	loc := i18n.FromContext(r.Context())
	title := formStr(r, "title")
	destination := formStr(r, "destination")
	notes := formStr(r, "notes")

	if title == "" || destination == "" {
		return nil, errValidation(loc.T("error.title_destination_required"))
	}
	if !maxLen(title, 200) || !maxLen(destination, 200) {
		return nil, errValidation(loc.T("error.title_destination_toolong"))
	}
	if !maxLen(notes, 5000) {
		return nil, errValidation(loc.T("error.notes_toolong"))
	}

	start, err := parseDate(r, "start_date")
	if err != nil {
		return nil, errValidation(loc.T("error.start_invalid"))
	}
	end, err := parseDate(r, "end_date")
	if err != nil {
		return nil, errValidation(loc.T("error.end_invalid"))
	}
	if end.Before(start) {
		return nil, errValidation(loc.T("error.end_before_start"))
	}

	lat, lng, err := parseCoords(r, "latitude", "longitude")
	if err != nil {
		return nil, err
	}
	zoom := parseZoomPtr(r, "map_zoom")

	var budget *float64
	if raw := formStr(r, "budget"); raw != "" {
		bv, err := strconv.ParseFloat(raw, 64)
		if err != nil || bv < 0 {
			return nil, errValidation(loc.T("error.budget_invalid"))
		}
		budget = &bv
	}
	people := 1
	if raw := formStr(r, "people"); raw != "" {
		if pv, err := strconv.Atoi(raw); err == nil && pv >= 1 {
			people = pv
		}
	}
	if people > 999 {
		people = 999
	}

	return &models.Vacation{
		Title:       title,
		Destination: destination,
		StartDate:   start,
		EndDate:     end,
		Latitude:    lat,
		Longitude:   lng,
		MapZoom:     zoom,
		Notes:       notes,
		Budget:      budget,
		People:      people,
	}, nil
}

// parseZoomPtr reads an optional Leaflet map zoom level (1–19); anything out of
// range or unparseable yields nil so the client falls back to its default.
func parseZoomPtr(r *http.Request, field string) *int {
	raw := formStr(r, field)
	if raw == "" {
		return nil
	}
	z, err := strconv.Atoi(raw)
	if err != nil || z < 1 || z > 19 {
		return nil
	}
	return &z
}
