// Package geo provides a minimal, Nominatim-compatible forward-geocoding client.
// The public OpenStreetMap Nominatim endpoint needs no API key; compatible
// providers such as LocationIQ or a self-hosted Nominatim accept one via ?key=.
package geo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the public Photon (Komoot) endpoint — an OSM-based geocoder
// designed for as-you-type autocomplete (prefix matching).
const DefaultBaseURL = "https://photon.komoot.io"

const (
	cacheTTL     = 15 * time.Minute
	cacheMaxSize = 512
)

// Client performs forward geocoding against a Nominatim-compatible service and
// caches recent results to stay within provider usage policies.
type Client struct {
	http      *http.Client
	apiKey    string
	userAgent string

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	results []Result
	expires time.Time
}

// New builds a geocoding client. apiKey is optional and, when set, appended as
// the ?key= query parameter for Nominatim-compatible providers that require it.
func New(apiKey string) *Client {
	return &Client{
		http:      &http.Client{Timeout: 10 * time.Second},
		apiKey:    strings.TrimSpace(apiKey),
		userAgent: "VacationPlanner/1.0 (+https://github.com/daknoblo/vacationplanner)",
		cache:     make(map[string]cacheEntry),
	}
}

// Result is a single geocoding match.
type Result struct {
	DisplayName string  `json:"display_name"`
	Lat         float64 `json:"lat"`
	Lng         float64 `json:"lng"`
	Type        string  `json:"type,omitempty"`
	Class       string  `json:"class,omitempty"`
}

// nominatimResult mirrors the subset of the Nominatim JSON response we use.
type nominatimResult struct {
	DisplayName string `json:"display_name"`
	Lat         string `json:"lat"`
	Lon         string `json:"lon"`
	Type        string `json:"type"`
	Class       string `json:"class"`
}

// Search performs a forward geocode for the given free-text query. baseURL may
// be empty, in which case the public Nominatim endpoint is used. A non-zero
// biasLat/biasLon prioritizes results near that point (Photon location bias),
// so activity searches favour the current destination.
func (c *Client) Search(ctx context.Context, baseURL, query, lang string, limit int, biasLat, biasLon float64) ([]Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 10 {
		limit = 5
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	lang = strings.ToLower(strings.TrimSpace(lang))

	bias := ""
	if biasLat != 0 || biasLon != 0 {
		bias = strconv.FormatFloat(biasLat, 'f', 5, 64) + "," + strconv.FormatFloat(biasLon, 'f', 5, 64)
	}
	key := baseURL + "|" + lang + "|" + strconv.Itoa(limit) + "|" + bias + "|" + strings.ToLower(query)
	if cached, ok := c.cachedResults(key); ok {
		return cached, nil
	}

	q := url.Values{}
	q.Set("q", query)
	q.Set("limit", strconv.Itoa(limit))
	if lang != "" {
		q.Set("lang", lang)
	}
	if biasLat != 0 || biasLon != 0 {
		q.Set("lat", strconv.FormatFloat(biasLat, 'f', 6, 64))
		q.Set("lon", strconv.FormatFloat(biasLon, 'f', 6, 64))
	}
	if c.apiKey != "" {
		q.Set("key", c.apiKey)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("geo: building request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("geo: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("geo: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("geo: reading response: %w", err)
	}

	results := parseResults(body)
	c.store(key, results)
	return results, nil
}

// Reverse resolves coordinates to the nearest place label. ok is false when no
// match is found. baseURL may be empty (public Photon endpoint). Both Photon
// (GeoJSON) and Nominatim (single object) reverse responses are handled.
func (c *Client) Reverse(ctx context.Context, baseURL string, lat, lon float64, lang string) (Result, bool, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	lang = strings.ToLower(strings.TrimSpace(lang))

	key := "rev|" + baseURL + "|" + lang + "|" +
		strconv.FormatFloat(lat, 'f', 5, 64) + "," + strconv.FormatFloat(lon, 'f', 5, 64)
	if cached, ok := c.cachedResults(key); ok {
		if len(cached) == 0 {
			return Result{}, false, nil
		}
		return cached[0], true, nil
	}

	q := url.Values{}
	q.Set("lat", strconv.FormatFloat(lat, 'f', 6, 64))
	q.Set("lon", strconv.FormatFloat(lon, 'f', 6, 64))
	if lang != "" {
		q.Set("lang", lang)
	}
	q.Set("format", "jsonv2") // required by Nominatim; ignored by Photon
	if c.apiKey != "" {
		q.Set("key", c.apiKey)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/reverse?"+q.Encode(), nil)
	if err != nil {
		return Result{}, false, fmt.Errorf("geo: building reverse request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, false, fmt.Errorf("geo: reverse request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return Result{}, false, fmt.Errorf("geo: reverse unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return Result{}, false, fmt.Errorf("geo: reading reverse response: %w", err)
	}

	results := parseReverse(body)
	c.store(key, results)
	if len(results) == 0 {
		return Result{}, false, nil
	}
	return results[0], true, nil
}

// parseResults auto-detects the response shape: a Photon/GeoJSON object or a
// Nominatim-style array, so either provider can back the geocoder.
func parseResults(body []byte) []Result {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] == '{' {
		return parsePhoton(body)
	}
	return parseNominatim(body)
}

// parseReverse handles a reverse-geocode response: a Photon GeoJSON object, a
// Nominatim array, or a single Nominatim object (its reverse endpoint returns
// one result rather than a list).
func parseReverse(body []byte) []Result {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] == '[' {
		return parseNominatim(body)
	}
	if r := parsePhoton(body); len(r) > 0 {
		return r
	}
	return parseNominatimObject(body)
}

func parsePhoton(body []byte) []Result {
	var pr struct {
		Features []struct {
			Geometry struct {
				Coordinates []float64 `json:"coordinates"`
			} `json:"geometry"`
			Properties struct {
				Name        string `json:"name"`
				Street      string `json:"street"`
				HouseNumber string `json:"housenumber"`
				Postcode    string `json:"postcode"`
				City        string `json:"city"`
				County      string `json:"county"`
				State       string `json:"state"`
				Country     string `json:"country"`
				Type        string `json:"type"`
				OSMKey      string `json:"osm_key"`
			} `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(body, &pr); err != nil {
		return nil
	}
	out := make([]Result, 0, len(pr.Features))
	for _, f := range pr.Features {
		if len(f.Geometry.Coordinates) < 2 {
			continue
		}
		p := f.Properties
		street := strings.TrimSpace(p.Street)
		if street != "" && strings.TrimSpace(p.HouseNumber) != "" {
			street += " " + strings.TrimSpace(p.HouseNumber)
		}
		cityLine := strings.TrimSpace(p.Postcode + " " + p.City)
		out = append(out, Result{
			DisplayName: joinParts(p.Name, street, cityLine, p.County, p.State, p.Country),
			Lat:         f.Geometry.Coordinates[1],
			Lng:         f.Geometry.Coordinates[0],
			Type:        p.Type,
			Class:       p.OSMKey,
		})
	}
	return out
}

func parseNominatim(body []byte) []Result {
	var raw []nominatimResult
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	out := make([]Result, 0, len(raw))
	for _, r := range raw {
		lat, err1 := strconv.ParseFloat(strings.TrimSpace(r.Lat), 64)
		lng, err2 := strconv.ParseFloat(strings.TrimSpace(r.Lon), 64)
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, Result{
			DisplayName: r.DisplayName,
			Lat:         lat,
			Lng:         lng,
			Type:        r.Type,
			Class:       r.Class,
		})
	}
	return out
}

// parseNominatimObject parses a single Nominatim result object, as returned by
// the /reverse endpoint (a bare object, not an array).
func parseNominatimObject(body []byte) []Result {
	var r nominatimResult
	if err := json.Unmarshal(body, &r); err != nil {
		return nil
	}
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(r.Lat), 64)
	lng, err2 := strconv.ParseFloat(strings.TrimSpace(r.Lon), 64)
	if err1 != nil || err2 != nil || strings.TrimSpace(r.DisplayName) == "" {
		return nil
	}
	return []Result{{
		DisplayName: r.DisplayName,
		Lat:         lat,
		Lng:         lng,
		Type:        r.Type,
		Class:       r.Class,
	}}
}

// joinParts builds a display label from unique, non-empty location parts.
func joinParts(parts ...string) string {
	seen := make(map[string]bool, len(parts))
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return strings.Join(out, ", ")
}

func (c *Client) cachedResults(key string) ([]Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[key]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.results, true
}

func (c *Client) store(key string, results []Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cache) >= cacheMaxSize {
		c.cache = make(map[string]cacheEntry)
	}
	c.cache[key] = cacheEntry{results: results, expires: time.Now().Add(cacheTTL)}
}
