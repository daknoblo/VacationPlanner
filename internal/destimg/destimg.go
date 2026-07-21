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
}

type entry struct {
	data  []byte
	ctype string
	exp   time.Time
}

// New builds a destination image client.
func New() *Client {
	return &Client{
		http:      &http.Client{Timeout: 8 * time.Second},
		userAgent: "VacationPlanner/1.0 (+https://github.com/daknoblo/vacationplanner)",
		cache:     make(map[string]entry),
	}
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
	summaryURL := fmt.Sprintf("https://%s.wikipedia.org/api/rest_v1/page/summary/%s",
		lang, url.PathEscape(title))
	thumb, err := c.thumbnailURL(ctx, summaryURL)
	if err != nil || thumb == "" {
		return nil, "", false
	}
	// SSRF guard: only download images hosted on Wikimedia.
	u, err := url.Parse(thumb)
	if err != nil || !isWikimediaHost(u.Host) {
		return nil, "", false
	}
	return c.download(ctx, thumb)
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
