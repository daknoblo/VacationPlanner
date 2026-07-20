// Package pdf renders a vacation itinerary to a PDF document using a pure-Go
// engine (no CGO). Text is drawn with the embedded Go (UTF-8) font, which
// covers Latin, Cyrillic and Greek; emoji and other pictographs are stripped.
package pdf

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

// Vacation writes a PDF itinerary for v to w. When day is non-nil only that day
// is rendered; otherwise the full trip overview is produced.
func Vacation(w io.Writer, v *models.Vacation, day *time.Time, loc *i18n.Localizer) error {
	doc := fpdf.New("P", "mm", "A4", "")
	doc.AddUTF8FontFromBytes("Go", "", goregular.TTF)
	doc.AddUTF8FontFromBytes("Go", "B", gobold.TTF)
	doc.SetMargins(15, 15, 15)
	doc.SetAutoPageBreak(true, 15)
	doc.AddPage()

	title := func(s string) {
		doc.SetFont("Go", "B", 18)
		doc.MultiCell(0, 9, plainText(s), "", "L", false)
	}
	h2 := func(s string) {
		doc.Ln(3)
		doc.SetFont("Go", "B", 13)
		doc.MultiCell(0, 7, plainText(s), "", "L", false)
		doc.Ln(1)
	}
	body := func(s string) {
		doc.SetFont("Go", "", 11)
		doc.MultiCell(0, 5.5, plainText(s), "", "L", false)
	}
	muted := func(s string) {
		doc.SetFont("Go", "", 10)
		doc.SetTextColor(100, 116, 139)
		doc.MultiCell(0, 5, plainText(s), "", "L", false)
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

// plainText strips emoji and other pictographs the text font cannot render,
// keeping letters, digits and punctuation.
func plainText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isPictograph(r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func isPictograph(r rune) bool {
	switch {
	case r == 0xFE0F || r == 0x200D: // emoji variation selector, zero-width joiner
		return true
	case r >= 0x2600 && r <= 0x27BF: // misc symbols + dingbats (incl. plane, checkmark)
		return true
	case r >= 0x1F000 && r <= 0x1FAFF: // emoji / pictographs
		return true
	default:
		return false
	}
}
