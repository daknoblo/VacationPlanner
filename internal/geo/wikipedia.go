package geo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Wikipedia resolves exact article titles and redirects, never arbitrary URLs
// or search snippets. The usual bounded cache and request pacing are reused.
type Wikipedia struct {
	client *Client
}

func NewWikipedia() *Wikipedia {
	client := New("")
	client.http.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &Wikipedia{client: client}
}

var wikipediaLanguage = regexp.MustCompile(`^[a-z]{2,3}(?:-[a-z0-9]{1,8})?$`)

func ValidWikipediaLanguage(language string) bool { return wikipediaLanguage.MatchString(language) }

func (w *Wikipedia) Lookup(ctx context.Context, language string, names []string) ([]Result, error) {
	if !ValidWikipediaLanguage(language) || len(names) > 8 {
		return nil, errors.New("geo: invalid Wikipedia lookup")
	}
	for _, name := range names {
		if strings.TrimSpace(name) == "" || len(name) > 800 || strings.ContainsAny(name, "|:\r\n") {
			return nil, errors.New("geo: invalid Wikipedia article title")
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	key := "wikipedia|" + language + "|" + strings.Join(names, "|")
	if results, ok := w.client.cachedResults(key); ok {
		return results, nil
	}
	query := url.Values{
		"action": {"query"}, "format": {"json"}, "formatversion": {"2"}, "redirects": {"1"},
		"prop": {"coordinates|pageprops"}, "ppprop": {"disambiguation"}, "coprimary": {"primary"},
		"titles": {strings.Join(names, "|")},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+language+".wikipedia.org/w/api.php?"+query.Encode(), nil)
	if err != nil {
		return nil, errors.New("geo: cannot build Wikipedia request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", w.client.userAgent)
	if err := w.client.waitRequest(ctx); err != nil {
		return nil, err
	}
	resp, err := w.client.http.Do(req)
	if err != nil {
		return nil, errors.New("geo: Wikipedia request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("geo: Wikipedia returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(body) > 2<<20 {
		return nil, errors.New("geo: invalid Wikipedia response size")
	}
	results, err := wikipediaResults(body, names)
	if err != nil {
		return nil, err
	}
	w.client.store(key, results)
	return results, nil
}

func wikipediaResults(body []byte, names []string) ([]Result, error) {
	type redirect struct{ From, To string }
	type page struct {
		PageID      int                        `json:"pageid"`
		NS          int                        `json:"ns"`
		Title       string                     `json:"title"`
		Missing     bool                       `json:"missing"`
		Pageprops   map[string]json.RawMessage `json:"pageprops"`
		Coordinates []struct {
			Lat, Lon *float64
			Primary  bool
			Globe    string
		} `json:"coordinates"`
	}
	var data struct {
		Error json.RawMessage `json:"error"`
		Query *struct {
			Normalized, Redirects []redirect
			Pages                 []page
		} `json:"query"`
	}
	if err := json.Unmarshal(body, &data); err != nil || data.Query == nil || len(data.Error) != 0 {
		return nil, errors.New("geo: invalid Wikipedia result")
	}
	redirects := make(map[string]string)
	for _, r := range append(data.Query.Normalized, data.Query.Redirects...) {
		redirects[r.From] = r.To
	}
	pages := make(map[string]page)
	for _, p := range data.Query.Pages {
		pages[p.Title] = p
	}
	var results []Result
	for _, name := range names {
		target := name
		seen := make(map[string]bool)
		for redirects[target] != "" && redirects[target] != target {
			if seen[target] || len(seen) >= 10 {
				return nil, errors.New("geo: invalid Wikipedia redirect chain")
			}
			seen[target] = true
			target = redirects[target]
		}
		p, exists := pages[target]
		if !exists || p.Missing || p.NS != 0 || p.PageID <= 0 || len(p.Coordinates) != 1 {
			continue
		}
		if _, ambiguous := p.Pageprops["disambiguation"]; ambiguous {
			continue
		}
		point := p.Coordinates[0]
		if !point.Primary || point.Globe != "earth" || point.Lat == nil || point.Lon == nil ||
			!validCoordinates(*point.Lat, *point.Lon) {
			continue
		}
		results = append(results, Result{
			Name: name, DisplayName: p.Title, Lat: *point.Lat, Lng: *point.Lon, Type: "landmark", Class: "wikipedia",
		})
	}
	return results, nil
}
