package server

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// settingsCtxKey scopes the per-request settings snapshot in a request context.
type settingsCtxKey struct{}

// settingsSnapshot caches the settings table for the lifetime of one request.
// A single page render performs many small lookups (region, currency, geocoder
// and router base URLs, category icons …) and the route lookup even runs once
// per activity leg; without this they would each cost a database round trip.
type settingsSnapshot struct {
	mu     sync.Mutex
	loaded bool
	data   map[string]string
	err    error
}

// settingsScope attaches an empty snapshot to every request context. Handlers
// reach it through Server.settings, which loads the table at most once.
func (s *Server) settingsScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), settingsCtxKey{}, &settingsSnapshot{})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// settings returns the application settings. The returned map is shared within
// the request and must only be read, never mutated. Contexts without a snapshot
// (background jobs, tests) fall through to the store on every call.
func (s *Server) settings(ctx context.Context) (map[string]string, error) {
	snap, ok := ctx.Value(settingsCtxKey{}).(*settingsSnapshot)
	if !ok {
		return s.store.GetSettings(ctx)
	}
	snap.mu.Lock()
	defer snap.mu.Unlock()
	if !snap.loaded {
		snap.data, snap.err = s.store.GetSettings(ctx)
		snap.loaded = true
	}
	return snap.data, snap.err
}

// putSetting persists a setting and drops the request's cached snapshot so a
// later read in the same request sees the new value.
func (s *Server) putSetting(ctx context.Context, key, value string) error {
	if err := s.store.PutSetting(ctx, key, value); err != nil {
		return err
	}
	if snap, ok := ctx.Value(settingsCtxKey{}).(*settingsSnapshot); ok {
		snap.mu.Lock()
		snap.loaded, snap.data, snap.err = false, nil, nil
		snap.mu.Unlock()
	}
	return nil
}

// tzCache memoizes parsed IANA locations: time.LoadLocation re-reads and
// re-parses the embedded tzdata on every call, and the display timezone is
// resolved several times per request.
var (
	tzMu    sync.RWMutex
	tzCache = make(map[string]*time.Location)
)

// loadLocation resolves an IANA timezone name, caching successful lookups.
func loadLocation(name string) (*time.Location, error) {
	tzMu.RLock()
	loc, ok := tzCache[name]
	tzMu.RUnlock()
	if ok {
		return loc, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, err
	}
	tzMu.Lock()
	tzCache[name] = loc
	tzMu.Unlock()
	return loc, nil
}
