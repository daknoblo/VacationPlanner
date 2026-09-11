// Package geo provides a minimal, Nominatim-compatible forward-geocoding client.
// The public OpenStreetMap Nominatim endpoint needs no API key; compatible
// providers such as LocationIQ or a self-hosted Nominatim accept one via ?key=.
package geo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
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

	mu          sync.Mutex
	cache       map[string]cacheEntry
	rateGate    chan struct{}
	nextRequest time.Time
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
		rateGate:  make(chan struct{}, 1),
	}
}

// Result is a single geocoding match.
type Result struct {
	DisplayName string  `json:"display_name"`
	Lat         float64 `json:"lat"`
	Lng         float64 `json:"lng"`
	Type        string  `json:"type,omitempty"`
	Class       string  `json:"class,omitempty"`
	OSMValue    string  `json:"osm_value,omitempty"`
	Region      string  `json:"region,omitempty"`
	Country     string  `json:"country,omitempty"`
	Name        string  `json:"name,omitempty"`
	Street      string  `json:"street,omitempty"`
	HouseNumber string  `json:"house_number,omitempty"`
	City        string  `json:"city,omitempty"`
	Postcode    string  `json:"postcode,omitempty"`
}

// nominatimResult mirrors the subset of the Nominatim JSON response we use.
type nominatimResult struct {
	DisplayName string `json:"display_name"`
	Lat         string `json:"lat"`
	Lon         string `json:"lon"`
	Type        string `json:"type"`
	Class       string `json:"class"`
	Category    string `json:"category"`
	Name        string `json:"name"`
	Address     struct {
		State       string `json:"state"`
		Region      string `json:"region"`
		County      string `json:"county"`
		Country     string `json:"country"`
		Road        string `json:"road"`
		HouseNumber string `json:"house_number"`
		City        string `json:"city"`
		Town        string `json:"town"`
		Village     string `json:"village"`
		Postcode    string `json:"postcode"`
		Hotel       string `json:"hotel"`
	} `json:"address"`
}

// Search performs a forward geocode for the given free-text query. baseURL may
// be empty, in which case the public Photon endpoint is used. A non-zero
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
	photon := c.isPhotonProvider(baseURL)
	path := "/search"
	if photon {
		path = "/api/"
	} else {
		q.Set("format", "jsonv2")
		q.Set("addressdetails", "1")
	}
	if lang != "" && photon {
		q.Set("lang", lang)
	} else if lang != "" {
		q.Set("accept-language", lang)
	}
	if photon && (biasLat != 0 || biasLon != 0) {
		q.Set("lat", strconv.FormatFloat(biasLat, 'f', 6, 64))
		q.Set("lon", strconv.FormatFloat(biasLon, 'f', 6, 64))
	}
	if c.apiKey != "" {
		q.Set("key", c.apiKey)
	}

	endpoint, err := geocoderEndpoint(baseURL, path, q)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("geo: building request failed")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	if err := c.waitRequest(ctx); err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("geo: request failed")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		if !photon && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed) {
			// Custom Photon hosts need not contain "photon". Probe the other
			// protocol once, keeping it under the same request pace and timeout.
			_ = resp.Body.Close()
			c.store("provider|"+baseURL, []Result{{Type: "photon"}})
			return c.Search(ctx, baseURL, query, lang, limit, biasLat, biasLon)
		}
		return nil, fmt.Errorf("geo: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("geo: reading response: %w", err)
	}
	if !json.Valid(body) {
		return nil, errors.New("geo: invalid response")
	}

	results := parseResults(body)
	c.store(key, results)
	return results, nil
}

// isPhotonBaseURL reports whether the geocoder base URL points at a Photon
// instance. Photon returns GeoJSON and rejects unknown query parameters (such
// as "format"), which Nominatim-compatible providers require.
func isPhotonBaseURL(baseURL string) bool {
	u, err := url.Parse(baseURL)
	return err == nil && (strings.Contains(strings.ToLower(u.Hostname()), "photon") ||
		strings.Contains(strings.ToLower(u.Path), "photon") || strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/api"))
}

func (c *Client) isPhotonProvider(baseURL string) bool {
	if results, ok := c.cachedResults("provider|" + baseURL); ok && len(results) > 0 {
		return results[0].Type == "photon"
	}
	return isPhotonBaseURL(baseURL)
}

func geocoderEndpoint(baseURL, path string, query url.Values) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", errors.New("geo: invalid provider URL")
	}
	basePath := strings.TrimRight(u.Path, "/")
	for _, suffix := range []string{"/api", "/search", "/reverse"} {
		if strings.HasSuffix(basePath, suffix) {
			basePath = strings.TrimSuffix(basePath, suffix)
			break
		}
	}
	u.Path = basePath + path
	values := u.Query()
	for key, value := range query {
		values[key] = value
	}
	u.RawQuery = values.Encode()
	u.Fragment = ""
	return u.String(), nil
}

// All uncached forward and reverse calls share the provider's one-second pace.
func (c *Client) waitRequest(ctx context.Context) error {
	select {
	case c.rateGate <- struct{}{}:
		defer func() { <-c.rateGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if delay := time.Until(c.nextRequest); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.nextRequest = time.Now().Add(time.Second)
	return nil
}

// Reverse resolves coordinates to the nearest place label. ok is false when no
// match is found. baseURL may be empty (public Photon endpoint). Both Photon
// (GeoJSON) and Nominatim (single object) reverse responses are handled.
func (c *Client) Reverse(ctx context.Context, baseURL string, lat, lon float64, lang string) (Result, bool, error) {
	if !validCoordinates(lat, lon) {
		return Result{}, false, errors.New("geo: invalid reverse coordinates")
	}
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
	photon := c.isPhotonProvider(baseURL)
	if lang != "" && photon {
		q.Set("lang", lang)
	} else if lang != "" {
		q.Set("accept-language", lang)
	}
	// Nominatim-compatible providers default to XML and need an explicit JSON
	// format; Photon returns GeoJSON and rejects an unknown "format" parameter,
	// so only send it to non-Photon providers.
	if !photon {
		q.Set("format", "jsonv2")
		q.Set("addressdetails", "1")
	}
	if c.apiKey != "" {
		q.Set("key", c.apiKey)
	}

	endpoint, err := geocoderEndpoint(baseURL, "/reverse", q)
	if err != nil {
		return Result{}, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Result{}, false, errors.New("geo: building reverse request failed")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	if err := c.waitRequest(ctx); err != nil {
		return Result{}, false, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, false, errors.New("geo: reverse request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		if !photon && resp.StatusCode == http.StatusBadRequest {
			_ = resp.Body.Close()
			c.store("provider|"+baseURL, []Result{{Type: "photon"}})
			return c.Reverse(ctx, baseURL, lat, lon, lang)
		}
		return Result{}, false, fmt.Errorf("geo: reverse unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return Result{}, false, fmt.Errorf("geo: reading reverse response: %w", err)
	}
	if !json.Valid(body) {
		return Result{}, false, errors.New("geo: invalid reverse response")
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
				Region      string `json:"region"`
				Country     string `json:"country"`
				Type        string `json:"type"`
				OSMKey      string `json:"osm_key"`
				OSMValue    string `json:"osm_value"`
			} `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(body, &pr); err != nil {
		return nil
	}
	out := make([]Result, 0, len(pr.Features))
	for _, f := range pr.Features {
		if len(f.Geometry.Coordinates) < 2 || !validCoordinates(f.Geometry.Coordinates[1], f.Geometry.Coordinates[0]) {
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
			OSMValue:    p.OSMValue,
			Region:      regionLabel(p.State, p.Region, p.County, p.Country),
			Country:     strings.TrimSpace(p.Country),
			Name:        p.Name,
			Street:      p.Street,
			HouseNumber: p.HouseNumber,
			City:        p.City,
			Postcode:    p.Postcode,
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
		if err1 != nil || err2 != nil || !validCoordinates(lat, lng) {
			continue
		}
		out = append(out, r.result(lat, lng))
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
	if err1 != nil || err2 != nil || !validCoordinates(lat, lng) || strings.TrimSpace(r.DisplayName) == "" {
		return nil
	}
	return []Result{r.result(lat, lng)}
}

func (r nominatimResult) result(lat, lng float64) Result {
	return Result{
		DisplayName: r.DisplayName,
		Lat:         lat,
		Lng:         lng,
		Type:        r.Type,
		Class:       firstPart(r.Class, r.Category),
		Region:      regionLabel(r.Address.State, r.Address.Region, r.Address.County, r.Address.Country),
		Country:     strings.TrimSpace(r.Address.Country),
		Name:        firstPart(r.Name, r.Address.Hotel),
		Street:      r.Address.Road,
		HouseNumber: r.Address.HouseNumber,
		City:        firstPart(r.Address.City, r.Address.Town, r.Address.Village),
		Postcode:    r.Address.Postcode,
	}
}

func validCoordinates(lat, lng float64) bool {
	return !math.IsNaN(lat) && !math.IsNaN(lng) && !math.IsInf(lat, 0) && !math.IsInf(lng, 0) &&
		lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180
}

func firstPart(parts ...string) string {
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			return part
		}
	}
	return ""
}

func regionLabel(state, region, county, country string) string {
	part := firstPart(state, region, county)
	if part == "" {
		return ""
	}
	return joinParts(part, country)
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
		c.evictLocked()
	}
	c.cache[key] = cacheEntry{results: results, expires: time.Now().Add(cacheTTL)}
}

// evictLocked makes room in a full cache: expired entries are dropped first and,
// when none are expired, the single entry closest to expiry. This keeps the
// cache warm instead of discarding every entry at once. The caller holds c.mu.
func (c *Client) evictLocked() {
	now := time.Now()
	var soonestKey string
	var soonest time.Time
	for k, e := range c.cache {
		if now.After(e.expires) {
			delete(c.cache, k)
			continue
		}
		if soonestKey == "" || e.expires.Before(soonest) {
			soonestKey, soonest = k, e.expires
		}
	}
	if len(c.cache) >= cacheMaxSize && soonestKey != "" {
		delete(c.cache, soonestKey)
	}
}
