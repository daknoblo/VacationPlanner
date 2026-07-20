// Package pdf renders a vacation itinerary to a PDF document using a pure-Go
// engine (no CGO). Text is encoded to CP1252, which covers Western European
// scripts; characters outside that range are dropped.
package pdf

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

// Vacation writes a PDF itinerary for v to w. When day is non-nil only that day
// is rendered; otherwise the full trip overview is produced.
func Vacation(w io.Writer, v *models.Vacation, day *time.Time, loc *i18n.Localizer) error {
	doc := fpdf.New("P", "mm", "A4", "")
	tr := doc.UnicodeTranslatorFromDescriptor("")
	doc.SetMargins(15, 15, 15)
	doc.SetAutoPageBreak(true, 15)
	doc.AddPage()

	title := func(s string) {
		doc.SetFont("Helvetica", "B", 18)
		doc.MultiCell(0, 9, tr(s), "", "L", false)
	}
	h2 := func(s string) {
		doc.Ln(3)
		doc.SetFont("Helvetica", "B", 13)
		doc.MultiCell(0, 7, tr(s), "", "L", false)
		doc.Ln(1)
	}
	body := func(s string) {
		doc.SetFont("Helvetica", "", 11)
		doc.MultiCell(0, 5.5, tr(s), "", "L", false)
	}
	muted := func(s string) {
		doc.SetFont("Helvetica", "", 10)
		doc.SetTextColor(100, 116, 139)
		doc.MultiCell(0, 5, tr(s), "", "L", false)
		doc.SetTextColor(0, 0, 0)
	}

	title(v.Title)
	muted(v.Destination)

	meta := fmt.Sprintf("%s - %s  |  %d %s  |  %d %s",
		v.StartDate.Format("02.01.2006"), v.EndDate.Format("02.01.2006"),
		v.Nights(), loc.T("vacation.nights"), v.People, loc.T("field.people"))
	if v.Budget != nil {
		meta += fmt.Sprintf("  |  %s %s", loc.T("field.budget"), strconv.FormatFloat(*v.Budget, 'f', -1, 64))
	}
	body(meta)

	if day == nil {
		if strings.TrimSpace(v.Notes) != "" {
			h2(loc.T("field.notes"))
			body(v.Notes)
		}
		if len(v.TravelSegments) > 0 {
			h2(loc.T("section.travel"))
			for _, t := range v.TravelSegments {
				body("- " + travelLine(t, loc))
			}
		}
	}

	for i, d := range v.Days() {
		if day != nil && !sameDate(d, *day) {
			continue
		}
		h2(fmt.Sprintf("%s %d - %s", loc.T("tab.day"), i+1, d.Format("02.01.2006")))
		found := false
		for _, a := range v.Activities {
			if !a.OnDay(d) {
				continue
			}
			found = true
			line := fmt.Sprintf("%s-%s  %s", a.StartLabel(), a.EndLabel(), a.Title)
			if a.Category != "" {
				line += " (" + a.Category + ")"
			}
			body(line)
			if strings.TrimSpace(a.Description) != "" {
				muted("    " + a.Description)
			}
		}
		for _, s := range v.Sights {
			if s.PlannedDate != nil && sameDate(*s.PlannedDate, d) {
				found = true
				line := "- " + s.Name
				if s.Category != "" {
					line += " (" + s.Category + ")"
				}
				body(line)
			}
		}
		if !found {
			muted(loc.T("activity.none"))
		}
	}

	return doc.Output(w)
}

func travelLine(t models.TravelSegment, loc *i18n.Localizer) string {
	line := loc.T("travel.kind." + string(t.Kind))
	if t.Mode != "" {
		line += " - " + loc.T("travel.mode."+t.Mode)
	}
	if t.FromLocation != "" || t.ToLocation != "" {
		line += ": " + t.FromLocation
		if t.ToLocation != "" {
			line += " -> " + t.ToLocation
		}
	}
	return line
}

func sameDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
