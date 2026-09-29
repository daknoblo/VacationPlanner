package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestIdeaReferenceLabelsAndCoordinatePin(t *testing.T) {
	renderer, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	for _, language := range []i18n.Lang{i18n.LangEN, i18n.LangDE} {
		for _, test := range []struct {
			name     string
			lat, lng *float64
			links    []models.ItemLink
			cost     *float64
		}{
			{name: "fallback", lat: fptr(55.837277), lng: fptr(8.267053)},
			{name: "sources and cost", lat: fptr(-33.123456789), lng: fptr(151.123456789), cost: fptr(25),
				links: []models.ItemLink{{Kind: "website", URL: "https://museum.example/"}, {Kind: "tripadvisor", URL: "https://www.tripadvisor.com/Search?q=Museum"}}},
			{name: "zero coordinates", lat: fptr(0), lng: fptr(0)},
			{name: "missing", cost: fptr(25)},
			{name: "partial", lat: fptr(55)},
		} {
			t.Run(string(language)+"/"+test.name, func(t *testing.T) {
				loc := i18n.NewLocalizer(language)
				item := models.Item{Title: "Museum & park", Latitude: test.lat, Longitude: test.lng, Links: test.links, Cost: test.cost}
				rec := httptest.NewRecorder()
				if err := renderer.fragment(rec, "item_row", loc, item, time.UTC, "€"); err != nil {
					t.Fatal(err)
				}
				tokenizer := html.NewTokenizer(strings.NewReader(rec.Body.String()))
				var anchors []map[string]string
				var text strings.Builder
				for tokenType := tokenizer.Next(); tokenType != html.ErrorToken; tokenType = tokenizer.Next() {
					token := tokenizer.Token()
					if tokenType == html.TextToken {
						text.WriteString(token.Data)
					}
					if tokenType != html.StartTagToken || token.Data != "a" {
						continue
					}
					attrs := make(map[string]string)
					for _, attr := range token.Attr {
						attrs[attr.Key] = attr.Val
					}
					if strings.Contains(attrs["class"], "suggestion__link") {
						anchors = append(anchors, attrs)
					}
				}
				sourceCount := 2
				if len(test.links) != 0 {
					sourceCount = len(test.links)
				}
				wantCount := sourceCount
				if item.HasCoords() {
					wantCount++
				}
				if len(anchors) != wantCount {
					t.Fatalf("links/pin count: got %d want %d", len(anchors), wantCount)
				}
				for _, anchor := range anchors {
					if anchor["target"] != "_blank" || anchor["rel"] != "noopener noreferrer" {
						t.Fatal("external navigation lost its safety attributes", anchor)
					}
				}
				if item.HasCoords() {
					pin := anchors[len(anchors)-1]
					u, err := url.Parse(pin["href"])
					if err != nil || u.Host != "www.google.com" || u.Path != "/maps/search/" ||
						u.Query().Get("api") != "1" || u.Query().Get("query") != coordValue(test.lat)+","+coordValue(test.lng) {
						t.Fatal("pin does not open the saved coordinates", pin, err)
					}
					if pin["aria-label"] != loc.T("item.open_maps") || pin["title"] != loc.T("item.open_maps") {
						t.Fatal("map pin lacks a localized accessible label", pin)
					}
					if strings.Contains(text.String(), coordValue(test.lat)+", "+coordValue(test.lng)) {
						t.Fatal("coordinates still occupy a visible row")
					}
				} else if strings.Contains(rec.Body.String(), "suggestion__link--map") {
					t.Fatal("unlocated idea has an invented map target")
				}
				if strings.Contains(text.String(), "💶") != (test.cost != nil) {
					t.Fatal("coordinate cleanup changed the cost display")
				}
			})
		}
	}
}

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
