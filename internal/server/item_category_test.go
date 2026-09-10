package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestOriginalItemEditorPreservesUnlistedCategoryAndPayer(t *testing.T) {
	s := newIntegrationServer(t)
	ctx := context.Background()
	v := sampleVacation()
	if err := s.store.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	payer := &models.Person{Name: "Payer"}
	if err := s.store.CreatePerson(ctx, payer); err != nil {
		t.Fatal(err)
	}
	cost := 20.0
	item := &models.Item{VacationID: v.ID, Title: "Museum", Category: "AI culture category", Cost: &cost, PaidBy: &payer.ID}
	if err := s.store.CreateItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/items/"+item.ID.String()+"/edit", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `value="AI culture category" selected`) {
		t.Fatalf("original category absent from editor: %d %s", rec.Code, rec.Body.String())
	}
	rec = postAISettings(s, "/items/"+item.ID.String()+"/edit", url.Values{
		"title": {item.Title}, "category": {item.Category}, "cost": {"0"},
	}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("source edit failed: %d %s", rec.Code, rec.Body.String())
	}
	got, err := s.store.GetItem(ctx, item.ID)
	if err != nil || got.Category != item.Category || got.PaidBy == nil || *got.PaidBy != payer.ID || got.Cost == nil || *got.Cost != 0 {
		t.Fatalf("source edit lost metadata: %+v %v", got, err)
	}
}
