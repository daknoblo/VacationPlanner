package server

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/route"
)

// The source records are the durable queue: new/changed/geocoded coordinate
// pairs are discovered without handler hooks, page visits or materializing N*M
// jobs in memory. One request at a time, at least two seconds apart.
func (s *Server) StartIdeaRouteWorker(ctx context.Context) func() {
	s.ideasRouteStart.Do(func() {
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		s.ideasRouteStop = func() { cancel(); <-done }
		go func() {
			defer close(done)
			activityTurn := true
			for runCtx.Err() == nil {
				delay := 2 * time.Second
				var err error
				var worked bool
				if activityTurn {
					worked, err = s.prepareNextActivityRoute(runCtx)
				}
				if !worked && err == nil {
					err = s.prepareNextIdeaRoute(runCtx)
				}
				activityTurn = !activityTurn
				if err != nil {
					if runCtx.Err() != nil {
						return
					}
					s.log.Warn("background routing failed", "err", err)
					delay = 30 * time.Second
				}
				timer := time.NewTimer(delay)
				select {
				case <-runCtx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}()
	})
	return s.ideasRouteStop
}

func (s *Server) activityRouteSources(ctx context.Context, vacationID uuid.UUID, provider string) ([]models.ActivityRoute, error) {
	vacation, err := s.store.GetVacation(ctx, vacationID)
	if err != nil {
		return nil, err
	}
	vacation.Items, err = s.store.ListItems(ctx, vacationID)
	if err != nil {
		return nil, err
	}
	vacation.Lodgings, err = s.store.ListLodgings(ctx, vacationID)
	if err != nil {
		return nil, err
	}
	_, timezone := s.regionSettings(ctx)
	byDay := make(map[string][]models.Item)
	for _, item := range vacation.Items {
		if item.Day != nil {
			key := item.Day.Format("2006-01-02")
			byDay[key] = append(byDay[key], item)
		}
	}
	days := make([]string, 0, len(byDay))
	for day := range byDay {
		days = append(days, day)
	}
	sort.Strings(days)
	var sources []models.ActivityRoute
	for _, key := range days {
		items := orderDayItems(byDay[key])
		base, label := dayHotel(i18n.NewLocalizer(i18n.LangEN), timezone, vacation, *items[0].Day)
		for index, item := range items {
			from, _ := resolveOrigin(items, index, base, label)
			to := itemPoint(item)
			if !validActivityPoint(from) || !validActivityPoint(to) {
				continue
			}
			sources = append(sources, *activityRouteSource(item, provider, from, to))
		}
	}
	return sources, nil
}

func validActivityPoint(point *route.Point) bool {
	return point != nil && point.Lat >= -90 && point.Lat <= 90 && point.Lng >= -180 && point.Lng <= 180
}

func (s *Server) prepareNextActivityRoute(parent context.Context) (bool, error) {
	if s.routing == nil || !s.routing.Enabled() {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return false, err
	}
	vacations, err := s.store.ListVacations(ctx)
	if err != nil {
		return false, err
	}
	for _, vacation := range vacations {
		sources, err := s.activityRouteSources(ctx, vacation.ID, settings[settingRouteBaseURL])
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return false, err
		}
		for _, source := range sources {
			if _, err := s.store.GetActivityRoute(ctx, &source); err == nil {
				continue
			} else if !isNotFound(err) {
				return false, err
			}
			s.activityRouting.Store(true)
			defer s.activityRouting.Store(false)
			result, routeErr := activityRoute(ctx, activityRouteKey{
				client: s.routing, baseURL: source.Provider,
				from: route.Point{Lat: source.FromLat, Lng: source.FromLng},
				to:   route.Point{Lat: source.ToLat, Lng: source.ToLng},
			})
			if parent.Err() != nil {
				return true, parent.Err()
			}
			current, err := s.activityRouteSources(ctx, vacation.ID, source.Provider)
			if isNotFound(err) {
				return true, nil
			}
			if err != nil {
				return true, err
			}
			for _, candidate := range current {
				if candidate != source {
					continue
				}
				source.Status = "unavailable"
				if routeErr == nil && usableIdeaDrive(result) {
					source.Status = "ready"
					source.DistanceM, source.DurationS = result.TotalDistanceM, result.TotalDurationS
				} else if routeErr == nil {
					routeErr = fmt.Errorf("route: invalid activity driving metrics")
				}
				if saved, err := s.store.PutActivityRoute(ctx, &source); err != nil {
					return true, err
				} else if saved {
					s.routeRevision.Add(1)
				}
				return true, routeErr
			}
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) prepareNextIdeaRoute(parent context.Context) error {
	if s.routing == nil || !s.routing.Enabled() {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	if err := s.queueUnlocatedRouteGeography(ctx); err != nil {
		return err
	}
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return err
	}
	job, err := s.store.NextIdeaRoute(ctx, settings[settingRouteBaseURL])
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	result, routeErr := activityRoute(ctx, activityRouteKey{
		client: s.routing, baseURL: job.Provider,
		from: route.Point{Lat: job.FromLat, Lng: job.FromLng},
		to:   route.Point{Lat: job.ToLat, Lng: job.ToLng},
	})
	if parent.Err() != nil {
		return parent.Err()
	}
	job.Status = "unavailable"
	if routeErr == nil && usableIdeaDrive(result) {
		job.Status, job.DistanceM, job.DurationS, job.Geometry = "ready", result.Legs[0].DistanceM, result.Legs[0].DurationS, result.Geometry
		if len(result.Geometry) < 2 {
			s.log.Warn("background idea route has no road geometry", "item_id", job.ItemID, "lodging_id", job.LodgingID)
			s.routing.Forget(job.Provider, route.DefaultProfile, []route.Point{
				{Lat: job.FromLat, Lng: job.FromLng}, {Lat: job.ToLat, Lng: job.ToLng},
			})
		}
	} else if routeErr == nil {
		s.routing.Forget(job.Provider, route.DefaultProfile, []route.Point{
			{Lat: job.FromLat, Lng: job.FromLng}, {Lat: job.ToLat, Lng: job.ToLng},
		})
		routeErr = fmt.Errorf("route: invalid driving metrics")
	}
	if saved, err := s.store.PutIdeaRoute(ctx, job); err != nil {
		return err
	} else if saved {
		s.routeRevision.Add(1)
	}
	return routeErr
}

func (s *Server) queueUnlocatedRouteGeography(ctx context.Context) error {
	if s.geography == nil || s.geo == nil {
		return nil
	}
	ids, err := s.store.UnlocatedRouteVacations(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		s.geography.mu.Lock()
		state := s.geography.states[id]
		// Do not turn unresolved/ambiguous places into repeated provider calls.
		// Edits and the Settings refresh explicitly request another attempt.
		attempted := state != nil && (state.status.Pending || !state.finished.IsZero())
		s.geography.mu.Unlock()
		if !attempted {
			s.queueGeography(id, "en")
		}
	}
	return nil
}
