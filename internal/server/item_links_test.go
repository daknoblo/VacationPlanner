package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestIdeaLinksSurviveEditingAndScheduling(t *testing.T) {
	s := newIntegrationServer(t)
	ctx := context.Background()
	v := &models.Vacation{Title: "Rome", Destination: "Rome, Italy", StartDate: time.Now(), EndDate: time.Now().Add(24 * time.Hour)}
	if err := s.store.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"title": {"Museum"}, "description": {"A place to visit"},
		"latitude": {"41.9"}, "longitude": {"12.5"},
		"link_kind": {"wikipedia", "tripadvisor", "website"},
		"link_url":  {"https://en.wikipedia.org/wiki/Museum", "https://www.tripadvisor.com/Search?q=Museum", "https://museum.example/"},
	}
	rec := postAISettings(s, "/vacations/"+v.ID.String()+"/items", form, true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "https://museum.example/") {
		t.Fatalf("links not added: %d %s", rec.Code, rec.Body.String())
	}
	items, err := s.store.ListItems(ctx, v.ID)
	if err != nil || len(items) != 1 {
		t.Fatal("item not created", err)
	}
	id := items[0].ID
	rec = postAISettings(s, "/items/"+id.String()+"/edit", url.Values{"title": {"Renamed museum"}, "description": {"Edited"}}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body.String())
	}
	rec = postAISettings(s, "/items/"+id.String()+"/schedule", url.Values{"day": {time.Now().Format("2006-01-02")}, "start": {"10:00"}, "end": {"11:00"}}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("schedule: %d %s", rec.Code, rec.Body.String())
	}
	postAISettings(s, "/items/"+id.String()+"/visited", nil, true)
	got, err := s.store.GetItem(ctx, id)
	if err != nil || len(got.Links) != 3 || got.Links[2].URL != form["link_url"][2] || !got.HasCoords() || got.Day == nil {
		t.Fatalf("reference data lost: %+v %v", got, err)
	}
	form["link_url"][0] = "javascript:alert(1)"
	if rec := postAISettings(s, "/vacations/"+v.ID.String()+"/items", form, true); rec.Code != http.StatusUnprocessableEntity {
		t.Fatal("unsafe suggestion link accepted")
	}
}
