package geo

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlacesSearchUsesExplicitTownAndPOITags(t *testing.T) {
	for _, photon := range []bool{true, false} {
		t.Run(map[bool]string{true: "Photon", false: "Nominatim"}[photon], func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				q := r.URL.Query()
				switch q.Get("q") {
				case "Hamburg", "hamburg":
					_, _ = w.Write([]byte(`[{"name":"Hamburg","display_name":"Hamburg, Germany","lat":"53.55","lon":"10","type":"city"}]`))
				case "Skyline", "skyline":
					bounds := q.Get("viewbox")
					if photon && calls < 3 {
						bounds = q.Get("bbox")
						if q.Get("osm_tag") != "amenity:restaurant" {
							t.Error("restaurant filter missing", q)
						}
					} else if !photon && calls < 3 && q.Get("bounded") != "1" {
						t.Error("unbounded Nominatim search", q)
					}
					if calls < 3 && len(strings.Split(bounds, ",")) != 4 {
						t.Error("explicit Hamburg bounds missing", q)
					}
					_, _ = w.Write([]byte(`[{"name":"Skyline","display_name":"Skyline, Hamburg","lat":"53.55","lon":"10","type":"restaurant"},
						{"name":"Skyline","display_name":"Skyline, Canada","lat":"43","lon":"-78","type":"restaurant"},
						{"name":"Skyline Tattoo","display_name":"Skyline Tattoo, Hamburg","lat":"53.55","lon":"10","type":"tattoo"}]`))
				default:
					t.Errorf("unexpected query: %s", q)
					_, _ = w.Write([]byte(`[]`))
				}
			}))
			defer server.Close()
			base := server.URL
			if photon {
				base += "/photon"
			}
			c := New("")
			for _, query := range []string{"hamburg skyline restaurant", "Restaurant Skyline Hamburg"} {
				results, err := c.SearchPlaces(t.Context(), base, query, "de", 10, 55.5, 8.5, "Restaurant")
				if err != nil || len(results) != 1 || results[0].Type != "restaurant" {
					t.Fatal("local restaurant not found or unrelated business leaked", results, err)
				}
			}
			if calls != 2 {
				t.Fatal("equivalent searches did not use the location-scoped cache", calls)
			}
			_, err := c.Search(t.Context(), base, "Skyline", "de", 10, 53.55, 10)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 3 {
				t.Fatal("bounded and unrestricted caches were mixed", calls)
			}
		})
	}
}

func TestPlacesErrorsAreNotTreatedAsMissingMatches(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer s.Close()
	_, err := New("").SearchPlaces(t.Context(), s.URL, "Restaurant Hamburg", "de", 10, 0, 0, "")
	if err == nil || calls != 1 {
		t.Fatal("provider failure was hidden or retried as an alternative spelling", err, calls)
	}
}

func TestRestaurantAddressSearchCanReturnABuilding(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("q") == "Hamburg" {
			_, _ = w.Write([]byte(`[{"name":"Hamburg","lat":"53.55","lon":"10","type":"city"}]`))
			return
		}
		if q.Get("q") == "Lake" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		if q.Get("osm_tag") != "" || q.Get("q") != "Lake Road 42" || q.Get("bbox") == "" {
			t.Error("an address was incorrectly restricted to restaurant-tagged POIs", q)
		}
		_, _ = w.Write([]byte(`[{"name":"42","display_name":"Lake Road 42, Hamburg","lat":"53.55","lon":"10","type":"house"}]`))
	}))
	defer s.Close()
	results, err := New("").SearchPlaces(t.Context(), s.URL+"/photon", "Lake Road 42 Hamburg", "en", 10, 55, 9, "Restaurant")
	if err != nil || len(results) != 1 || results[0].Type != "house" {
		t.Fatal("explicit street address no longer selectable", results, err)
	}
}

func TestCompoundCityNamesAreNotSplitIntoTheWrongTown(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("bbox") != "" {
			t.Error("New York was incorrectly restricted to York", q)
		}
		switch q.Get("q") {
		case "York":
			_, _ = w.Write([]byte(`[{"name":"York","lat":"53.96","lon":"-1.08","type":"city"}]`))
		case "New":
			_, _ = w.Write([]byte(`[]`))
		case "New York":
			_, _ = w.Write([]byte(`[{"name":"New York","lat":"40.71","lon":"-74","type":"city"}]`))
		default:
			t.Error("unexpected query", q)
		}
	}))
	defer s.Close()
	results, err := New("").SearchPlaces(t.Context(), s.URL+"/photon", "New York", "en", 10, 0, 0, "")
	if err != nil || len(results) != 1 || results[0].Name != "New York" {
		t.Fatal("compound place search changed meaning", results, err)
	}
}
