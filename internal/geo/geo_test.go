package geo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchParsesResults(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("q") == "" {
			t.Errorf("missing q parameter")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"display_name":"Denmark","lat":"56.0","lon":"10.0","type":"country","class":"boundary"}]`))
	}))
	defer srv.Close()

	c := New("")
	res, err := c.Search(context.Background(), srv.URL, "Däne", "en", 5, 0, 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	if res[0].DisplayName != "Denmark" || res[0].Lat != 56.0 || res[0].Lng != 10.0 {
		t.Fatalf("unexpected result: %+v", res[0])
	}

	// A second identical query must be served from cache (no extra HTTP call).
	if _, err := c.Search(context.Background(), srv.URL, "Däne", "en", 5, 0, 0); err != nil {
		t.Fatalf("cached Search: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 upstream call (cache hit), got %d", calls)
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	c := New("")
	res, err := c.Search(context.Background(), "", "  ", "en", 5, 0, 0)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil results for empty query, got %+v", res)
	}
}

func TestSearchParsesPhoton(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"type":"FeatureCollection","features":[{"geometry":{"type":"Point","coordinates":[10.0,56.0]},"properties":{"name":"Dänemark","country":"Dänemark","type":"country","osm_key":"place"}}]}`))
	}))
	defer srv.Close()

	c := New("")
	res, err := c.Search(context.Background(), srv.URL, "Däne", "de", 5, 0, 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].Lat != 56.0 || res[0].Lng != 10.0 || res[0].DisplayName != "Dänemark" {
		t.Fatalf("unexpected: %+v", res)
	}
}

func TestSearchAppendsAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("key"); got != "secret" {
			t.Errorf("expected key=secret, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := New("secret")
	if _, err := c.Search(context.Background(), srv.URL, "Berlin", "en", 3, 0, 0); err != nil {
		t.Fatalf("Search: %v", err)
	}
}

func TestReversePhoton(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("lat") == "" || r.URL.Query().Get("lon") == "" {
			t.Errorf("missing lat/lon parameters")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"type":"FeatureCollection","features":[{"geometry":{"type":"Point","coordinates":[12.9,41.46]},"properties":{"name":"Latina","country":"Italia","type":"city","osm_key":"place"}}]}`))
	}))
	defer srv.Close()

	c := New("")
	res, ok, err := c.Reverse(context.Background(), srv.URL, 41.46, 12.9, "en")
	if err != nil {
		t.Fatalf("Reverse: %v", err)
	}
	if !ok || res.DisplayName != "Latina, Italia" || res.Lat != 41.46 || res.Lng != 12.9 {
		t.Fatalf("unexpected: ok=%v %+v", ok, res)
	}
}

func TestReverseNominatimObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"display_name":"Latina, Italy","lat":"41.46","lon":"12.90","type":"city","class":"place"}`))
	}))
	defer srv.Close()

	c := New("")
	res, ok, err := c.Reverse(context.Background(), srv.URL, 41.46, 12.9, "en")
	if err != nil {
		t.Fatalf("Reverse: %v", err)
	}
	if !ok || res.DisplayName != "Latina, Italy" || res.Lat != 41.46 || res.Lng != 12.9 {
		t.Fatalf("unexpected: ok=%v %+v", ok, res)
	}
}

func TestReverseNoMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"type":"FeatureCollection","features":[]}`))
	}))
	defer srv.Close()

	c := New("")
	_, ok, err := c.Reverse(context.Background(), srv.URL, 0.1, 0.1, "en")
	if err != nil {
		t.Fatalf("Reverse: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false for an empty result set")
	}
}

func TestReverseOmitsFormatForPhoton(t *testing.T) {
	var sawFormat bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("format") {
			sawFormat = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"type":"FeatureCollection","features":[]}`))
	}))
	defer srv.Close()

	// A base URL containing "photon" must not receive the format parameter,
	// which the real Photon endpoint rejects.
	c := New("")
	if _, _, err := c.Reverse(context.Background(), srv.URL+"/photon", 41.4, 12.9, "en"); err != nil {
		t.Fatalf("Reverse: %v", err)
	}
	if sawFormat {
		t.Fatalf("format parameter must not be sent to a Photon base URL")
	}
}

func TestIsPhotonBaseURL(t *testing.T) {
	cases := map[string]bool{
		"https://photon.komoot.io":      true,
		"https://my-photon.example":     true,
		"https://nominatim.example.org": false,
		"https://locationiq.example":    false,
		"":                              false,
	}
	for in, want := range cases {
		if got := isPhotonBaseURL(in); got != want {
			t.Errorf("isPhotonBaseURL(%q) = %v, want %v", in, got, want)
		}
	}
}
