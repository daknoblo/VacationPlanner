package server

import (
	"context"
	"fmt"
	"time"

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
			for runCtx.Err() == nil {
				delay := 2 * time.Second
				if err := s.prepareNextIdeaRoute(runCtx); err != nil {
					if runCtx.Err() != nil {
						return
					}
					s.log.Warn("background idea routing failed", "err", err)
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
	if _, err := s.store.PutIdeaRoute(ctx, job); err != nil {
		return err
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
