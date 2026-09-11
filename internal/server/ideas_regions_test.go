package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestIdeasAreGroupedByRegionAndUnknownIdeasRemainVisible(t *testing.T) {
	groups := groupIdeas([]models.Item{
		{Title: "Unknown"}, {Title: "West", Region: "Jutland"},
		{Title: "West two", Region: "jutland"}, {Title: "East", Region: "Zealand"},
	})
	if len(groups) != 3 || len(groups[0].Items) != 2 || groups[0].Region != "Jutland" || groups[2].Region != "" {
		t.Fatalf("unexpected region groups: %+v", groups)
	}
	if len(groups[2].Items) != 1 || groups[2].Items[0].Title != "Unknown" {
		t.Fatal("unlocated ideas were hidden")
	}
}

func TestManualRegionCanBeSetAndClearedWithoutLosingItemData(t *testing.T) {
	s := newIntegrationServer(t)
	ctx := context.Background()
	v := sampleVacation()
	if err := s.store.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	item := &models.Item{VacationID: v.ID, Title: "Museum", Region: "Automatic region"}
	if err := s.store.CreateItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	for _, region := range []string{"My area", ""} {
		rec := postAISettings(s, "/items/"+item.ID.String()+"/edit", url.Values{"title": {item.Title}, "region": {region}}, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("edit: %d %s", rec.Code, rec.Body.String())
		}
		got, err := s.store.GetItem(ctx, item.ID)
		if err != nil || got.Region != region || got.RegionManual != (region != "") {
			t.Fatalf("region not persisted: %+v, %v", got, err)
		}
	}
}

func TestUnassignedNoticeLinksOnlyUnassignedSourceBookings(t *testing.T) {
	renderer, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	view := budgetView{
		HasBudget: true, Currency: "€", Unassigned: 20, UnassignedCount: 1, ExpenseCount: 2,
		Expenses: []budgetExpense{
			{Source: "lodging", SourceID: "unassigned-id", Title: "Unassigned hotel", Amount: 20},
			{Source: "item", SourceID: "assigned-id", Title: "Paid museum", Amount: 10, PayerID: "payer", PayerName: "Alex"},
		},
	}
	rec := httptest.NewRecorder()
	if err := renderer.fragment(rec, "budget_panel", i18n.NewLocalizer(i18n.LangEN), view, time.UTC, "€"); err != nil {
		t.Fatal(err)
	}
	start := strings.Index(rec.Body.String(), `<aside class="budget-notice"`)
	if start < 0 {
		t.Fatal("missing unassigned notice")
	}
	end := strings.Index(rec.Body.String()[start:], "</aside>")
	if end < 0 {
		t.Fatal("missing end of unassigned notice")
	}
	notice := rec.Body.String()[start : start+end]
	if !strings.Contains(notice, "#budget-source-lodging-unassigned-id") || strings.Contains(notice, "assigned-id\" data-budget-source=\"item\"") {
		t.Fatalf("incorrect unassigned source links: %s", notice)
	}
}
