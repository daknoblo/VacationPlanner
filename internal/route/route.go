// Package route provides a minimal client for OpenRouteService-compatible
// directions APIs (also usable against a self-hosted ORS). It returns per-leg
// distance and duration for an ordered list of waypoints and caches recent
// results to respect provider rate limits. A straight-line fallback (Haversine)
// is provided for when no routing key is configured.
package route

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the public OpenRouteService endpoint. It requires an API key
// (set via the ROUTER_API_KEY environment variable).
const DefaultBaseURL = "https://api.openrouteservice.org"

// DefaultProfile is the ORS routing profile used for travel legs.
const DefaultProfile = "driving-car"

const (
	cacheTTL     = 30 * time.Minute
	cacheMaxSize = 256
)

// Client talks to an OpenRouteService-compatible directions API.
type Client struct {
	http      *http.Client
	apiKey    string
	userAgent string

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	result  Result
	expires time.Time
}

// New builds a routing client. An empty apiKey disables live routing (callers
// should fall back to Haversine distances).
func New(apiKey string) *Client {
	return &Client{
		http:      &http.Client{Timeout: 12 * time.Second},
		apiKey:    strings.TrimSpace(apiKey),
		userAgent: "VacationPlanner/1.0 (+https://github.com/daknoblo/vacationplanner)",
		cache:     make(map[string]cacheEntry),
	}
}

// Enabled reports whether live routing is configured (an API key is present).
func (c *Client) Enabled() bool { return c.apiKey != "" }

// Leg is the distance (metres) and duration (seconds) between two consecutive
// waypoints.
type Leg struct {
	DistanceM float64
	DurationS float64
}

// Result holds the per-leg breakdown of a route. len(Legs) == len(points)-1.
type Result struct {
	Legs           []Leg
	TotalDistanceM float64
	TotalDurationS float64
}

// Point is a geographic coordinate as {latitude, longitude}.
type Point struct {
	Lat, Lng float64
}

// orsRequest is the ORS directions request body ([lng, lat] order).
type orsRequest struct {
	Coordinates [][2]float64 `json:"coordinates"`
}

type orsResponse struct {
	Routes []struct {
		Summary struct {
			Distance float64 `json:"distance"`
			Duration float64 `json:"duration"`
		} `json:"summary"`
		Segments []struct {
			Distance float64 `json:"distance"`
			Duration float64 `json:"duration"`
		} `json:"segments"`
	} `json:"routes"`
}

// Route returns per-leg distance and duration for the ordered points. baseURL
// and profile may be empty to use the defaults. It requires at least two points.
func (c *Client) Route(ctx context.Context, baseURL, profile string, points []Point) (Result, error) {
	if len(points) < 2 {
		return Result{}, fmt.Errorf("route: need at least two points")
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	profile = strings.TrimSpace(profile)
	if profile == "" {
		profile = DefaultProfile
	}

	key := cacheKey(baseURL, profile, points)
	if cached, ok := c.cached(key); ok {
		return cached, nil
	}

	coords := make([][2]float64, len(points))
	for i, p := range points {
		coords[i] = [2]float64{p.Lng, p.Lat} // ORS expects [lng, lat]
	}
	body, err := json.Marshal(orsRequest{Coordinates: coords})
	if err != nil {
		return Result{}, fmt.Errorf("route: encoding request: %w", err)
	}

	url := baseURL + "/v2/directions/" + profile
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("route: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if c.apiKey != "" {
		req.Header.Set("Authorization", c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("route: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return Result{}, fmt.Errorf("route: unexpected status %d", resp.StatusCode)
	}

	var parsed orsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&parsed); err != nil {
		return Result{}, fmt.Errorf("route: decoding response: %w", err)
	}
	if len(parsed.Routes) == 0 {
		return Result{}, fmt.Errorf("route: empty response")
	}

	r0 := parsed.Routes[0]
	res := Result{
		TotalDistanceM: r0.Summary.Distance,
		TotalDurationS: r0.Summary.Duration,
		Legs:           make([]Leg, 0, len(r0.Segments)),
	}
	for _, seg := range r0.Segments {
		res.Legs = append(res.Legs, Leg{DistanceM: seg.Distance, DurationS: seg.Duration})
	}

	c.store(key, res)
	return res, nil
}

// Haversine returns the great-circle distance in metres between two points.
func Haversine(a, b Point) float64 {
	const earthRadiusM = 6371000.0
	lat1 := a.Lat * math.Pi / 180
	lat2 := b.Lat * math.Pi / 180
	dLat := (b.Lat - a.Lat) * math.Pi / 180
	dLng := (b.Lng - a.Lng) * math.Pi / 180
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadiusM * math.Asin(math.Min(1, math.Sqrt(h)))
}

func cacheKey(baseURL, profile string, points []Point) string {
	var b strings.Builder
	b.WriteString(baseURL)
	b.WriteByte('|')
	b.WriteString(profile)
	for _, p := range points {
		b.WriteByte('|')
		b.WriteString(strconv.FormatFloat(p.Lat, 'f', 5, 64))
		b.WriteByte(',')
		b.WriteString(strconv.FormatFloat(p.Lng, 'f', 5, 64))
	}
	return b.String()
}

func (c *Client) cached(key string) (Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[key]
	if !ok || time.Now().After(e.expires) {
		if ok {
			delete(c.cache, key)
		}
		return Result{}, false
	}
	return e.result, true
}

func (c *Client) store(key string, res Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cache) >= cacheMaxSize {
		for k := range c.cache { // evict an arbitrary entry to bound memory
			delete(c.cache, k)
			break
		}
	}
	c.cache[key] = cacheEntry{result: res, expires: time.Now().Add(cacheTTL)}
}
