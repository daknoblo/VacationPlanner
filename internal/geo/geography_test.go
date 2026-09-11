package geo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGeographyMetadata(t *testing.T) {
	tests := []struct {
		name, body, region, country, nameField, street, house string
	}{
		{"photon state", `{"features":[{"geometry":{"coordinates":[10,50]},"properties":{"name":"Blue Harbour Hotel","street":"Quay","housenumber":"12","state":"Bavaria","region":"Other","county":"County","country":"Germany","type":"house","osm_key":"tourism","osm_value":"hotel"}}]}`, "Bavaria, Germany", "Germany", "Blue Harbour Hotel", "Quay", "12"},
		{"photon region", `{"features":[{"geometry":{"coordinates":[10,50]},"properties":{"region":"North","country":"Norway"}}]}`, "North, Norway", "Norway", "", "", ""},
		{"photon county", `{"features":[{"geometry":{"coordinates":[10,50]},"properties":{"county":"County","country":"Ireland"}}]}`, "County, Ireland", "Ireland", "", "", ""},
		{"nominatim state", `[{"display_name":"Hotel","lat":"50","lon":"10","category":"tourism","type":"hotel","name":"Blue Harbour Hotel","address":{"state":"Bavaria","region":"Other","country":"Germany","road":"Quay","house_number":"12"}}]`, "Bavaria, Germany", "Germany", "Blue Harbour Hotel", "Quay", "12"},
		{"nominatim reverse region", `{"display_name":"Hotel","lat":"50","lon":"10","address":{"region":"North","country":"Norway"}}`, "North, Norway", "Norway", "", "", ""},
		{"nominatim county", `{"display_name":"Hotel","lat":"50","lon":"10","address":{"county":"County","country":"Ireland"}}`, "County, Ireland", "Ireland", "", "", ""},
		{"no label inference", `{"display_name":"Hotel, Guess Region, Germany","lat":"50","lon":"10","address":{"country":"Germany"}}`, "", "Germany", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := parseReverse([]byte(tt.body))
			if len(results) != 1 {
				t.Fatalf("results = %+v", results)
			}
			got := results[0]
			if got.Region != tt.region || got.Country != tt.country || got.Name != tt.nameField || got.Street != tt.street || got.HouseNumber != tt.house {
				t.Fatalf("unexpected metadata: %+v", got)
			}
		})
	}
}

func TestGeographyForwardPaths(t *testing.T) {
	for _, tt := range []struct {
		base, path string
		photon     bool
	}{
		{"", "/search", false},
		{"/nominatim", "/nominatim/search", false},
		{"/search", "/search", false},
		{"/photon", "/photon/api/", true},
		{"/api", "/api/", true},
	} {
		t.Run(tt.base, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.path {
					t.Errorf("path = %q, want %q", r.URL.Path, tt.path)
				}
				q := r.URL.Query()
				if tt.photon {
					if q.Has("format") || q.Get("lang") != "de" || q.Get("lat") == "" {
						t.Errorf("invalid Photon query: %v", q)
					}
				} else if q.Get("format") != "jsonv2" || q.Get("addressdetails") != "1" || q.Get("accept-language") != "de" || q.Has("lat") {
					t.Errorf("invalid Nominatim query: %v", q)
				}
				_, _ = w.Write([]byte(`[]`))
			}))
			defer srv.Close()
			_, err := New("").Search(context.Background(), srv.URL+tt.base, "hotel", "de", 10, 10, 20)
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGeographyErrorsDoNotExposeCredentials(t *testing.T) {
	for _, base := range []string{"http://127.0.0.1:1", "http://[invalid?key=secret"} {
		client := New("secret")
		_, err := client.Search(context.Background(), base, "hotel", "en", 10, 0, 0)
		if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "http:") {
			t.Fatalf("unsafe search error: %v", err)
		}
		_, _, err = client.Reverse(context.Background(), base, 1, 2, "en")
		if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "http:") {
			t.Fatalf("unsafe reverse error: %v", err)
		}
	}
}

func TestGeographyRateLimitCacheAndCancellation(t *testing.T) {
	var mu sync.Mutex
	var calls []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls = append(calls, time.Now())
		mu.Unlock()
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	client := New("")
	ctx := context.Background()
	if _, err := client.Search(ctx, srv.URL, "first", "en", 10, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Search(ctx, srv.URL, "first", "en", 10, 0, 0); err != nil {
		t.Fatal(err)
	}
	shortCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, _, err := client.Reverse(shortCtx, srv.URL, 1, 2, "en"); err == nil {
		t.Fatal("expected cancellation while waiting for rate limit")
	}
	if _, _, err := client.Reverse(ctx, srv.URL, 1, 2, "en"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 || calls[1].Sub(calls[0]) < 950*time.Millisecond {
		t.Fatalf("uncached calls not paced: %v", calls)
	}
}

func TestGeographyRejectsInvalidCoordinates(t *testing.T) {
	for _, body := range []string{
		`[{"display_name":"Hotel","lat":"NaN","lon":"1"}]`,
		`[{"display_name":"Hotel","lat":"91","lon":"1"}]`,
		`{"features":[{"geometry":{"coordinates":[181,50]}}]}`,
	} {
		if results := parseResults([]byte(body)); len(results) != 0 {
			t.Fatalf("invalid coordinates accepted: %+v", results)
		}
	}
}

func TestGeographyCustomPhotonProtocolDiscovery(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/search" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/api/" || r.URL.Query().Has("format") {
			t.Errorf("unexpected Photon request: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"features":[{"geometry":{"coordinates":[10,50]},"properties":{"state":"Bavaria","country":"Germany"}}]}`))
	}))
	defer srv.Close()
	client := New("")
	for _, query := range []string{"first", "first", "second"} {
		results, err := client.Search(context.Background(), srv.URL, query, "en", 10, 0, 0)
		if err != nil || len(results) != 1 || results[0].Region != "Bavaria, Germany" {
			t.Fatalf("custom Photon discovery failed: %+v %v", results, err)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("protocol/cache discovery repeated unnecessarily: %d calls", calls.Load())
	}
}
