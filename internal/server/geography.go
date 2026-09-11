package server

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/geo"
	"github.com/daknoblo/vacationplanner/internal/models"
)

const (
	geographyQueueSize   = 16
	geographyStatusLimit = 128
	geographyLookupLimit = 40
	geographyCooldown    = 5 * time.Minute
)

// geographyStatus contains presentation data only, never provider errors or URLs.
type geographyStatus struct {
	Pending        bool     `json:"pending"`
	Error          bool     `json:"error"`
	Limited        bool     `json:"limited"`
	Unresolved     []string `json:"unresolved"`
	UnknownRegions int      `json:"unknown_regions"`
	UpdatedCount   int      `json:"updated_count"`
	Completed      int      `json:"completed"`
	Total          int      `json:"total"`
}

type geographyJob struct {
	id   uuid.UUID
	lang string
}

type geographyState struct {
	status   geographyStatus
	finished time.Time
	cursor   int
	retry    bool
}

type geographyWorker struct {
	server  *Server
	queue   chan geographyJob
	mu      sync.Mutex
	states  map[uuid.UUID]*geographyState
	start   sync.Once
	stop    func()
	stopped bool
}

func newGeographyWorker(s *Server) *geographyWorker {
	return &geographyWorker{
		server: s,
		queue:  make(chan geographyJob, geographyQueueSize),
		states: make(map[uuid.UUID]*geographyState),
	}
}

// StartGeographyWorker starts one bounded enrichment worker. The returned stop
// function cancels active lookups and joins the goroutine; it is safe to repeat.
func (s *Server) StartGeographyWorker(ctx context.Context) func() {
	if s.geography == nil {
		return func() {}
	}
	w := s.geography
	w.start.Do(func() {
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		w.stop = func() { cancel(); <-done }
		go func() {
			defer close(done)
			defer func() {
				w.mu.Lock()
				defer w.mu.Unlock()
				w.stopped = true
				for _, state := range w.states {
					if state.status.Pending {
						state.status.Pending = false
						state.status.Error = true
					}
				}
			}()
			for {
				select {
				case <-runCtx.Done():
					return
				case job := <-w.queue:
					if runCtx.Err() != nil {
						return
					}
					w.run(runCtx, job)
				}
			}
		}()
	})
	return w.stop
}

func (s *Server) queueGeography(id uuid.UUID, lang string) {
	if s.geography != nil {
		s.geography.enqueue(id, lang, false)
	}
}

// retryGeography is for an explicit refresh or a relevant user edit, not polling.
func (s *Server) retryGeography(id uuid.UUID, lang string) {
	if s.geography != nil {
		s.geography.enqueue(id, lang, true)
	}
}

func (s *Server) geographyStatus(id uuid.UUID) geographyStatus {
	if s.geography == nil {
		return geographyStatus{Error: true}
	}
	w := s.geography
	w.mu.Lock()
	defer w.mu.Unlock()
	if state := w.states[id]; state != nil {
		status := state.status
		status.Unresolved = append([]string(nil), status.Unresolved...)
		return status
	}
	return geographyStatus{Error: w.stopped}
}

func (w *geographyWorker) enqueue(id uuid.UUID, lang string, force bool) {
	if id == uuid.Nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	state := w.states[id]
	if state != nil {
		if state.status.Pending {
			state.retry = state.retry || force
			return
		}
		if !force && time.Since(state.finished) < geographyCooldown {
			return
		}
	} else {
		if len(w.states) >= geographyStatusLimit {
			var oldest uuid.UUID
			var finished time.Time
			for key, value := range w.states {
				if !value.status.Pending && (oldest == uuid.Nil || value.finished.Before(finished)) {
					oldest, finished = key, value.finished
				}
			}
			delete(w.states, oldest)
		}
		state = &geographyState{}
		w.states[id] = state
	}
	if w.stopped {
		state.status.Error = true
		return
	}
	select {
	case w.queue <- geographyJob{id: id, lang: lang}:
		state.status.Pending = true
		state.status.Error = false
		state.status.Limited = false
		state.status.Completed = 0
		state.status.Total = 0
	default:
		state.status.Error = true
		state.status.Limited = true
		// Queue pressure is transient; a later view can retry immediately.
	}
}

func (w *geographyWorker) run(parent context.Context, job geographyJob) {
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	status := geographyStatus{}
	w.mu.Lock()
	cursor := w.states[job.id].cursor
	w.mu.Unlock()
	nextCursor := cursor
	defer func() {
		if ctx.Err() != nil {
			status.Error = true
		}
		w.mu.Lock()
		state := w.states[job.id]
		retry := state.retry
		state.status, state.finished, state.cursor, state.retry = status, time.Now(), nextCursor, false
		w.mu.Unlock()
		if retry && parent.Err() == nil {
			w.enqueue(job.id, job.lang, true)
		}
	}()
	s := w.server
	if s.geo == nil || s.store == nil {
		status.Error = true
		return
	}
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		status.Error = true
		return
	}
	lodgings, err := s.store.ListLodgings(ctx, job.id)
	if err != nil {
		status.Error = true
		return
	}
	items, err := s.store.ListItems(ctx, job.id)
	if err != nil {
		status.Error = true
		return
	}
	type lookup struct {
		lodging *models.Lodging
		item    *models.Item
	}
	var work []lookup
	for idx := range lodgings {
		l := &lodgings[idx]
		if l.Latitude == nil && l.Longitude == nil {
			work = append(work, lookup{lodging: l})
		} else if !l.HasCoords() {
			status.Unresolved = append(status.Unresolved, l.Name)
		}
	}
	for idx := range items {
		item := &items[idx]
		if item.Region == "" {
			status.UnknownRegions++
			if item.HasCoords() && !item.RegionManual {
				work = append(work, lookup{item: item})
			}
		}
	}
	if len(work) == 0 {
		return
	}
	status.Total = min(len(work), geographyLookupLimit)
	w.setProgress(job.id, 0, status.Total)
	baseURL := settings[settingGeoBaseURL]
	processed := 0
	for processed < len(work) && processed < geographyLookupLimit && ctx.Err() == nil {
		idx := (cursor + processed) % len(work)
		entry := work[idx]
		lookupCtx, lookupCancel := context.WithTimeout(ctx, 8*time.Second)
		if entry.lodging != nil {
			l := entry.lodging
			query := strings.TrimSpace(l.Location)
			if query == "" {
				query = strings.TrimSpace(l.Name)
			}
			results, lookupErr := s.geo.Search(lookupCtx, baseURL, query, "en", 10, 0, 0)
			if lookupErr != nil {
				status.Error = true
			}
			result, ok := matchLodging(l, results)
			if ok && lookupErr == nil {
				changed, updateErr := s.store.UpdateLodgingCoordinates(ctx, l, result.Lat, result.Lng)
				if updateErr != nil {
					status.Error = true
				}
				if changed {
					status.UpdatedCount++
				} else {
					status.Unresolved = append(status.Unresolved, l.Name)
				}
			} else {
				status.Unresolved = append(status.Unresolved, l.Name)
			}
		} else {
			item := entry.item
			// Persist canonical English region labels, independent of who opened
			// the page first; manually entered labels remain untouched.
			result, ok, lookupErr := s.geo.Reverse(lookupCtx, baseURL, *item.Latitude, *item.Longitude, "en")
			if lookupErr != nil {
				status.Error = true
			} else if ok && result.Region != "" {
				changed, updateErr := s.store.UpdateItemRegion(ctx, item, result.Region)
				if updateErr != nil {
					status.Error = true
				}
				if changed {
					status.UpdatedCount++
					status.UnknownRegions--
				}
			}
		}
		lookupCancel()
		processed++
		status.Completed = processed
		w.setProgress(job.id, status.Completed, status.Total)
	}
	status.Limited = processed < len(work)
	// Rotate across failed and outstanding entries rather than allowing the
	// first ambiguous accommodation to starve a large idea collection.
	nextCursor = (cursor + processed) % len(work)
	for offset := processed; offset < len(work); offset++ {
		if l := work[(cursor+offset)%len(work)].lodging; l != nil {
			status.Unresolved = append(status.Unresolved, l.Name)
		}
	}
}

func (w *geographyWorker) setProgress(id uuid.UUID, completed, total int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if state := w.states[id]; state != nil {
		state.status.Completed, state.status.Total = completed, total
	}
}

// matchLodging prefers a lodging whose name and address corroborate each other
// over other POIs sharing the same address (such as a hotel's restaurant).
// Equally supported matches at different coordinates remain ambiguous.
// Name-only queries are global: arrival hotels can be in a different country.
func matchLodging(l *models.Lodging, results []geo.Result) (geo.Result, bool) {
	if len(results) >= 10 {
		return geo.Result{}, false
	}
	var matches []geo.Result
	bestRank := 2
	for _, result := range results {
		if !specificLodgingResult(result) {
			continue
		}
		location := normalizeGeography(l.Location)
		name := normalizeGeography(l.Name)
		addressMatch := location != "" && containsGeography(location, result.Street) &&
			containsGeography(location, result.HouseNumber) &&
			(containsGeography(location, result.City) || containsGeography(location, result.Postcode))
		namedHotel := (hotelType(result.Type) || hotelType(result.OSMValue)) && normalizeGeography(result.Name) == name && name != ""
		namedMatch := namedHotel && location != "" &&
			(containsGeography(location, result.City) || containsGeography(location, result.Postcode)) &&
			(containsGeography(location, result.Name) || location == normalizeGeography(result.City))
		if location == "" && namedHotel {
			namedMatch = distinctiveLodgingName(name)
		}
		if !addressMatch && !namedMatch {
			continue
		}
		rank := 1
		if namedHotel {
			rank = 0
		}
		if rank > bestRank {
			continue
		}
		if rank < bestRank {
			matches = nil
			bestRank = rank
		}
		duplicate := false
		for _, match := range matches {
			if match.Lat == result.Lat && match.Lng == result.Lng {
				duplicate = true
			}
		}
		if !duplicate {
			matches = append(matches, result)
		}
	}
	if len(matches) != 1 {
		return geo.Result{}, false
	}
	return matches[0], true
}

func specificLodgingResult(result geo.Result) bool {
	switch result.Type {
	case "city", "town", "village", "country", "state", "county", "administrative", "region", "locality", "suburb", "street":
		return false
	}
	return hotelType(result.Type) || hotelType(result.OSMValue) || (result.Street != "" && result.HouseNumber != "")
}

func hotelType(kind string) bool {
	switch kind {
	case "hotel", "motel", "hostel", "guest_house", "apartment", "chalet", "resort", "camp_site", "caravan_site":
		return true
	}
	return false
}

func normalizeGeography(value string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}), " ")
}

func containsGeography(haystack, needle string) bool {
	needle = normalizeGeography(needle)
	return needle != "" && strings.Contains(" "+haystack+" ", " "+needle+" ")
}

func distinctiveLodgingName(name string) bool {
	words := make(map[string]bool)
	for _, word := range strings.Fields(name) {
		switch word {
		case "hotel", "motel", "hostel", "resort", "the", "a", "an", "guest", "house", "central", "city", "airport", "anreise", "abreise":
			continue
		}
		if len([]rune(word)) >= 3 {
			words[word] = true
		}
	}
	return len(words) >= 2
}
