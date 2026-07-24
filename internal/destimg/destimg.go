// Package destimg fetches a small representative image for a travel destination
// from Wikipedia page thumbnails and caches the result. All requests happen
// server-side so the browser only ever loads the image from our own origin
// (keeping the strict Content-Security-Policy intact).
package destimg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	cacheTTL     = 24 * time.Hour
	negativeTTL  = 6 * time.Hour
	cacheMaxSize = 512
	maxImageSize = 3 << 20 // 3 MiB
)

// Client resolves destinations to cached image bytes.
type Client struct {
	http      *http.Client
	userAgent string

	mu    sync.Mutex
	cache map[string]entry

	sumMu    sync.Mutex
	sumCache map[string]sumEntry
}

type entry struct {
	data  []byte
	ctype string
	exp   time.Time
}

// Summary is the Wikipedia intro text and link for a destination.
type Summary struct {
	Title       string
	Description string
	Extract     string
	URL         string
}

type sumEntry struct {
	s   Summary
	exp time.Time
}

// New builds a destination image client.
func New() *Client {
	return &Client{
		http:      &http.Client{Timeout: 8 * time.Second},
		userAgent: "VacationPlanner/1.0 (+https://github.com/daknoblo/vacationplanner)",
		cache:     make(map[string]entry),
		sumCache:  make(map[string]sumEntry),
	}
}

// Summary returns the Wikipedia intro summary for a destination. ok is false
// when no article/extract was found.
func (c *Client) Summary(ctx context.Context, destination, lang string) (Summary, bool) {
	title := firstSegment(destination)
	if title == "" {
		return Summary{}, false
	}
	lang = normalizeLang(lang)
	key := lang + "|" + strings.ToLower(title)

	c.sumMu.Lock()
	if e, hit := c.sumCache[key]; hit && time.Now().Before(e.exp) {
		c.sumMu.Unlock()
		return e.s, e.s.Extract != ""
	}
	c.sumMu.Unlock()

	sum, ok := c.fetchSummary(ctx, title, lang)
	ttl := cacheTTL
	if !ok {
		ttl = negativeTTL
	}
	c.sumMu.Lock()
	if len(c.sumCache) >= cacheMaxSize {
		c.sumCache = make(map[string]sumEntry)
	}
	c.sumCache[key] = sumEntry{s: sum, exp: time.Now().Add(ttl)}
	c.sumMu.Unlock()
	return sum, ok
}

func (c *Client) fetchSummary(ctx context.Context, title, lang string) (Summary, bool) {
	summaryURL := fmt.Sprintf("https://%s.wikipedia.org/api/rest_v1/page/summary/%s",
		lang, url.PathEscape(title))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, summaryURL, nil)
	if err != nil {
		return Summary{}, false
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return Summary{}, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Summary{}, false
	}

	var payload struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Extract     string `json:"extract"`
		ContentURLs struct {
			Desktop struct {
				Page string `json:"page"`
			} `json:"desktop"`
		} `json:"content_urls"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return Summary{}, false
	}
	extract := strings.TrimSpace(payload.Extract)
	return Summary{
		Title:       strings.TrimSpace(payload.Title),
		Description: strings.TrimSpace(payload.Description),
		Extract:     extract,
		URL:         payload.ContentURLs.Desktop.Page,
	}, extract != ""
}

// Fetch returns image bytes and content type for a destination using Wikipedia
// page thumbnails. ok is false when no suitable image was found.
func (c *Client) Fetch(ctx context.Context, destination, lang string) (data []byte, contentType string, ok bool) {
	title := firstSegment(destination)
	if title == "" {
		return nil, "", false
	}
	lang = normalizeLang(lang)
	key := lang + "|" + strings.ToLower(title)

	if e, hit := c.cached(key); hit {
		return e.data, e.ctype, len(e.data) > 0
	}

	data, contentType, ok = c.fetch(ctx, title, lang)
	ttl := cacheTTL
	if !ok {
		ttl = negativeTTL
	}
	c.store(key, data, contentType, ttl)
	return data, contentType, ok
}

func (c *Client) fetch(ctx context.Context, title, lang string) ([]byte, string, bool) {
	// Try, in order of decreasing precision: the exact-title Wikipedia summary
	// thumbnail in the UI language, then in English, then a search-based page
	// image (handles compound or slightly-off names that have no exact article)
	// in the UI language, then in English. Everything stays on Wikimedia hosts.
	langs := []string{lang}
	if lang != "en" {
		langs = append(langs, "en")
	}
	for _, lg := range langs {
		summaryURL := fmt.Sprintf("https://%s.wikipedia.org/api/rest_v1/page/summary/%s",
			lg, url.PathEscape(title))
		if thumb, err := c.thumbnailURL(ctx, summaryURL); err == nil {
			if data, ctype, ok := c.downloadThumbnail(ctx, thumb); ok {
				return data, ctype, true
			}
		}
	}
	for _, lg := range langs {
		if thumb := c.searchThumbnailURL(ctx, title, lg); thumb != "" {
			if data, ctype, ok := c.downloadThumbnail(ctx, thumb); ok {
				return data, ctype, true
			}
		}
	}
	return nil, "", false
}

// downloadThumbnail validates that a thumbnail URL is hosted on Wikimedia (SSRF
// guard) and downloads it. An empty URL yields ok=false.
func (c *Client) downloadThumbnail(ctx context.Context, thumb string) ([]byte, string, bool) {
	if thumb == "" {
		return nil, "", false
	}
	u, err := url.Parse(thumb)
	if err != nil || !isWikimediaHost(u.Host) {
		return nil, "", false
	}
	return c.download(ctx, thumb)
}

// searchThumbnailURL finds the best-matching Wikipedia article for the query via
// the MediaWiki search generator and returns its page image. This recovers an
// image when the exact-title summary lookup fails (e.g. compound POI names).
func (c *Client) searchThumbnailURL(ctx context.Context, title, lang string) string {
	q := url.Values{}
	q.Set("action", "query")
	q.Set("format", "json")
	q.Set("generator", "search")
	q.Set("gsrsearch", title)
	q.Set("gsrlimit", "5")
	q.Set("gsrnamespace", "0")
	q.Set("prop", "pageimages")
	q.Set("piprop", "thumbnail")
	q.Set("pithumbsize", "640")
	q.Set("redirects", "1")
	apiURL := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?%s", lang, q.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var payload struct {
		Query struct {
			Pages map[string]struct {
				Index     int `json:"index"`
				Thumbnail struct {
					Source string `json:"source"`
				} `json:"thumbnail"`
			} `json:"pages"`
		} `json:"query"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return ""
	}
	// Pick the thumbnail of the highest-ranked search result (lowest index).
	best, bestIndex := "", 0
	for _, p := range payload.Query.Pages {
		if p.Thumbnail.Source == "" {
			continue
		}
		if best == "" || p.Index < bestIndex {
			best, bestIndex = p.Thumbnail.Source, p.Index
		}
	}
	return best
}

func (c *Client) thumbnailURL(ctx context.Context, summaryURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, summaryURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("destimg: summary status %d", resp.StatusCode)
	}

	var payload struct {
		Thumbnail struct {
			Source string `json:"source"`
		} `json:"thumbnail"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return "", err
	}
	return payload.Thumbnail.Source, nil
}

func (c *Client) download(ctx context.Context, imageURL string) ([]byte, string, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, "", false
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", false
	}
	ctype := resp.Header.Get("Content-Type")
	if !allowedImageType(ctype) {
		return nil, "", false
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageSize))
	if err != nil || len(data) == 0 {
		return nil, "", false
	}
	return data, ctype, true
}

func (c *Client) cached(key string) (entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[key]
	if !ok || time.Now().After(e.exp) {
		return entry{}, false
	}
	return e, true
}

func (c *Client) store(key string, data []byte, ctype string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cache) >= cacheMaxSize {
		c.cache = make(map[string]entry)
	}
	c.cache[key] = entry{data: data, ctype: ctype, exp: time.Now().Add(ttl)}
}

func firstSegment(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func normalizeLang(lang string) string {
	if strings.EqualFold(strings.TrimSpace(lang), "de") {
		return "de"
	}
	return "en"
}

func isWikimediaHost(host string) bool {
	host = strings.ToLower(host)
	return host == "upload.wikimedia.org" || strings.HasSuffix(host, ".wikimedia.org")
}

// allowedImageType permits only raster image types. Remote SVGs are rejected so
// a fetched document can never carry active content.
func allowedImageType(ctype string) bool {
	ctype = strings.ToLower(strings.TrimSpace(ctype))
	if i := strings.IndexByte(ctype, ';'); i >= 0 {
		ctype = strings.TrimSpace(ctype[:i])
	}
	switch ctype {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}
