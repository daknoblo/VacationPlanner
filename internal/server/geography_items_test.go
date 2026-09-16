package server

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/geo"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/route"
)

func TestIdeaGeographyConfidence(t *testing.T) {
	v := &models.Vacation{Destination: "Denmark", Latitude: fptr(55.5), Longitude: fptr(8.5)}
	anchors := ideaGeographyAnchors(v, nil)
	monument := geo.Result{Name: "Mennesket ved Havet", Type: "artwork", Lat: 55.48778, Lng: 8.41116, Region: "Southern Denmark, Denmark"}
	for _, test := range []struct {
		title   string
		results []geo.Result
		want    bool
	}{
		{"Mennesket ved Havet (Der Mensch am Meer)", []geo.Result{monument}, true},
		{"Mennesket ved Havet", []geo.Result{monument, monument}, true},
		{"Mennesket ved Havet", []geo.Result{monument, {Name: monument.Name, Type: "artwork", Lat: 56, Lng: 9}}, false},
		{"Mennesket ved Havet", []geo.Result{{Name: "Ved Havet", Type: "street", Lat: 55.5, Lng: 8.5}}, false},
		{"Mennesket ved Havet", []geo.Result{{Name: monument.Name, Type: "artwork", Lat: -33, Lng: 151}}, false},
		{"Museum", []geo.Result{{Name: "Museum", Lat: 55.5, Lng: 8.5}}, false},
		{"Bernstein suchen an der Nordsee", []geo.Result{{Name: "Nordsee", Type: "sea", Lat: 55.5, Lng: 8.5}}, false},
		{"Ribe (älteste Stadt, 30 min) / VikingeCenter", []geo.Result{{Name: "Ribe", Type: "city", Lat: 55.3, Lng: 8.7}}, false},
		{"Ribe", []geo.Result{{Name: "Ribe", Type: "city", Lat: 55.3, Lng: 8.7}}, true},
		{"Mennesket ved Havet", make([]geo.Result, 10), false},
	} {
		t.Run(test.title, func(t *testing.T) {
			_, matched, _ := matchIdeaPlace(&models.Item{Title: test.title}, v, anchors, test.results)
			if matched != test.want {
				t.Fatalf("match=%v, want %v", matched, test.want)
			}
		})
	}
	_, matched, _ := matchIdeaPlace(&models.Item{Title: monument.Name}, &models.Vacation{Destination: "Unrelated trip"}, nil, []geo.Result{monument})
	if matched {
		t.Fatal("accepted a place without corroborating trip geography")
	}
	if route.Haversine(anchors[0], route.Point{Lat: monument.Lat, Lng: monument.Lng}) > 500_000 {
		t.Fatal("invalid nearby fixture")
	}
}

type ideaWikiStub func(context.Context, string, []string) ([]geo.Result, error)

func (f ideaWikiStub) Lookup(ctx context.Context, lang string, names []string) ([]geo.Result, error) {
	return f(ctx, lang, names)
}

func TestIdeaGeographyWikipediaFallbackAndRegion(t *testing.T) {
	var geoCalls, wikiCalls atomic.Int32
	s, st, v := newGeographyTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		geoCalls.Add(1)
		if r.URL.Path == "/reverse" {
			if r.URL.Query().Get("lat") != "55.487780" {
				t.Errorf("wrong reverse coordinates: %s", r.URL)
			}
			_, _ = w.Write([]byte(`{"display_name":"Esbjerg","lat":"55.48778","lon":"8.41116","address":{"state":"Southern Denmark","country":"Denmark"}}`))
		} else {
			// Photon can fill its limit with fuzzy street/business matches
			// without including the named monument at all.
			fuzzy := `{"name":"Ved Havet","type":"street","lat":"55","lon":"8"},`
			_, _ = w.Write([]byte("[" + strings.TrimSuffix(strings.Repeat(fuzzy, 10), ",") + "]"))
		}
	})
	v.Destination, v.Latitude, v.Longitude = "Denmark", fptr(55.5), fptr(8.5)
	if err := st.UpdateVacation(t.Context(), v); err != nil {
		t.Fatal(err)
	}
	s.wikipedia = ideaWikiStub(func(_ context.Context, lang string, names []string) ([]geo.Result, error) {
		wikiCalls.Add(1)
		if lang != "de" || len(names) != 1 || names[0] != "Mennesket ved Havet" {
			t.Errorf("unexpected Wikipedia request: %s %v", lang, names)
		}
		return []geo.Result{{Name: names[0], DisplayName: "Der Mensch am Meer", Class: "wikipedia", Type: "landmark", Lat: 55.48778, Lng: 8.41116}}, nil
	})
	item := &models.Item{VacationID: v.ID, Title: "Mennesket ved Havet (Der Mensch am Meer)", Notes: "Keep notes", Cost: fptr(25)}
	if err := st.CreateItem(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	s.queueGeography(v.ID, "de")
	s.geography.run(t.Context(), <-s.geography.queue)
	got, err := st.GetItem(t.Context(), item.ID)
	if err != nil || !got.HasCoords() || *got.Latitude != 55.48778 || got.Region != "Southern Denmark, Denmark" ||
		got.Location != "Der Mensch am Meer" || got.Title != item.Title || got.Notes != item.Notes || *got.Cost != 25 {
		t.Fatalf("idea not safely enriched: %+v %v", got, err)
	}
	status := s.geographyStatus(v.ID)
	if status.Error || status.Pending || status.Limited || status.Completed != 3 || status.Total != 3 ||
		status.UnknownRegions != 0 || len(status.UnresolvedIdeas) != 0 || wikiCalls.Load() != 1 || geoCalls.Load() != 2 {
		t.Fatalf("incorrect fallback budget/status: %+v geo=%d wiki=%d", status, geoCalls.Load(), wikiCalls.Load())
	}
	s.retryGeography(v.ID, "de")
	s.geography.run(t.Context(), <-s.geography.queue)
	if wikiCalls.Load() != 1 || geoCalls.Load() != 2 {
		t.Fatal("already located item triggered another provider call")
	}
}

func TestIdeaGeographyAmbiguityDoesNotFallBackOrGuess(t *testing.T) {
	s, st, v := newGeographyTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"Blue Museum","type":"museum","lat":"55","lon":"9","address":{"state":"Region"}},
			{"name":"Blue Museum","type":"museum","lat":"56","lon":"9","address":{"state":"Region"}}]`))
	})
	v.Latitude, v.Longitude = fptr(55), fptr(9)
	if err := st.UpdateVacation(t.Context(), v); err != nil {
		t.Fatal(err)
	}
	var wikiCalls atomic.Int32
	s.wikipedia = ideaWikiStub(func(context.Context, string, []string) ([]geo.Result, error) {
		wikiCalls.Add(1)
		return nil, nil
	})
	item := &models.Item{VacationID: v.ID, Title: "Blue Museum"}
	if err := st.CreateItem(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	s.queueGeography(v.ID, "en")
	s.geography.run(t.Context(), <-s.geography.queue)
	got, err := st.GetItem(t.Context(), item.ID)
	status := s.geographyStatus(v.ID)
	if err != nil || got.HasCoords() || wikiCalls.Load() != 0 || len(status.UnresolvedIdeas) != 1 ||
		status.Completed != 1 || status.Total != 1 || status.Limited {
		t.Fatalf("ambiguous place guessed or unresolved status lost: %+v %+v %v", got, status, err)
	}
}

func TestIdeaGeographyBudgetRemainsBounded(t *testing.T) {
	s, st, v := newGeographyTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"Blue Museum","type":"museum","lat":"55","lon":"9","address":{"state":"Region","country":"Norway"}}]`))
	})
	v.Latitude, v.Longitude = fptr(55), fptr(9)
	if err := st.UpdateVacation(t.Context(), v); err != nil {
		t.Fatal(err)
	}
	for range 41 {
		if err := st.CreateItem(t.Context(), &models.Item{VacationID: v.ID, Title: "Blue Museum"}); err != nil {
			t.Fatal(err)
		}
	}
	s.wikipedia = ideaWikiStub(func(context.Context, string, []string) ([]geo.Result, error) {
		t.Error("Wikipedia called despite a matching geocoder result")
		return nil, nil
	})
	s.queueGeography(v.ID, "de")
	s.geography.run(t.Context(), <-s.geography.queue)
	status := s.geographyStatus(v.ID)
	if status.Completed != 40 || status.Total != 40 || !status.Limited || len(status.UnresolvedIdeas) != 1 {
		t.Fatalf("batch budget changed: %+v", status)
	}
	s.retryGeography(v.ID, "de")
	s.geography.run(t.Context(), <-s.geography.queue)
	if status := s.geographyStatus(v.ID); status.Limited || len(status.UnresolvedIdeas) != 0 || status.Completed != 1 {
		t.Fatalf("next batch lost source priority or failed to finish: %+v", status)
	}
}

func TestIdeaWikipediaHintsNeverUseArbitraryURLs(t *testing.T) {
	item := &models.Item{Title: "Blue Museum (visit before 10)", Links: []models.ItemLink{
		{Kind: "website", URL: "https://private.example/wiki/Secret"},
		{Kind: "wikipedia", URL: "https://da.wikipedia.org/wiki/Blue_Museum"},
		{Kind: "wikipedia", URL: "https://de.wikipedia.org/wiki/Special:Search?search=Blue"},
	}}
	v := &models.Vacation{Destination: "Denmark", StartDate: time.Now()}
	queries := ideaGeographyQueries(item, v)
	if len(queries) != 1 || !strings.Contains(queries[0], "Blue Museum") || strings.Contains(queries[0], "Secret") {
		t.Fatalf("unsafe or duplicate name hints: %v", queries)
	}
}
