package geo

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type wikipediaTransport func(*http.Request) (*http.Response, error)

func (f wikipediaTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWikipediaExactRedirectCoordinatesAndCache(t *testing.T) {
	wiki := NewWikipedia()
	calls := 0
	wiki.client.http.Transport = wikipediaTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Scheme != "https" || r.URL.Host != "de.wikipedia.org" || r.URL.Path != "/w/api.php" ||
			r.URL.Query().Get("titles") != "Mennesket ved Havet" || r.URL.Query().Get("redirects") != "1" ||
			r.URL.Query().Has("gsrsearch") || r.Header.Get("Authorization") != "" {
			t.Fatalf("unsafe Wikipedia lookup: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"query":{
			"redirects":[{"from":"Mennesket ved Havet","to":"Der Mensch am Meer"}],
			"pages":[{"pageid":1,"ns":0,"title":"Der Mensch am Meer","coordinates":[{"lat":55.48778,"lon":8.41116,"primary":true,"globe":"earth"}]}]}}`))}, nil
	})
	for range 2 {
		results, err := wiki.Lookup(context.Background(), "de", []string{"Mennesket ved Havet"})
		if err != nil || len(results) != 1 || results[0].Name != "Mennesket ved Havet" ||
			results[0].DisplayName != "Der Mensch am Meer" || results[0].Lat != 55.48778 {
			t.Fatalf("exact redirected article not resolved: %+v %v", results, err)
		}
	}
	if calls != 1 {
		t.Fatal("lookup did not use bounded cache")
	}
	for _, language := range []string{"de.evil.example", "../de", "en:443", ""} {
		if _, err := wiki.Lookup(t.Context(), language, []string{"A monument"}); err == nil {
			t.Fatal("invalid Wikipedia hostname accepted")
		}
	}
	if _, err := wiki.Lookup(t.Context(), "de", []string{"Special:Search"}); err == nil {
		t.Fatal("namespace lookup accepted")
	}
	if calls != 1 {
		t.Fatal("invalid input contacted a provider")
	}
}

func TestWikipediaRejectsUncertainCoordinates(t *testing.T) {
	for _, page := range []string{
		`{"pageid":1,"ns":0,"title":"Example place","pageprops":{"disambiguation":""},"coordinates":[{"lat":1,"lon":2,"primary":true,"globe":"earth"}]}`,
		`{"pageid":1,"ns":1,"title":"Example place","coordinates":[{"lat":1,"lon":2,"primary":true,"globe":"earth"}]}`,
		`{"pageid":1,"ns":0,"title":"Example place","coordinates":[{"lat":1,"lon":2,"primary":true,"globe":"mars"}]}`,
		`{"pageid":1,"ns":0,"title":"Example place","coordinates":[{"lat":1,"primary":true,"globe":"earth"}]}`,
		`{"pageid":1,"ns":0,"title":"Example place","coordinates":[{"lat":91,"lon":2,"primary":true,"globe":"earth"}]}`,
		`{"pageid":1,"ns":0,"title":"Different page","coordinates":[{"lat":1,"lon":2,"primary":true,"globe":"earth"}]}`,
		`{"title":"Example place","missing":true}`,
	} {
		results, err := wikipediaResults([]byte(`{"query":{"pages":[`+page+`]}}`), []string{"Example place"})
		if err != nil || len(results) != 0 {
			t.Fatalf("uncertain Wikipedia coordinates accepted: %+v %v", results, err)
		}
	}
	for _, body := range []string{`{}`, `not JSON`, `{"error":{"code":"failed"}}`,
		`{"query":{"redirects":[{"from":"A","to":"B"},{"from":"B","to":"A"}],"pages":[]}}`} {
		if _, err := wikipediaResults([]byte(body), []string{"A"}); err == nil {
			t.Fatal("invalid Wikipedia response accepted")
		}
	}
}
