package server

import (
	"context"
	"time"
)

func (s *Server) StartIdeaDescriptionWorker(ctx context.Context) func() {
	s.ideaDescriptionStart.Do(func() {
		ctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		s.ideaDescriptionStop = func() { cancel(); <-done }
		go func() {
			defer close(done)
			recovered := false
			for ctx.Err() == nil {
				delay := 2 * time.Second
				if !recovered {
					recoveryCtx, stop := context.WithTimeout(ctx, 2*time.Second)
					err := s.store.InterruptIdeaDescriptions(recoveryCtx)
					stop()
					recovered = err == nil
					if err != nil {
						s.log.Warn("cannot recover idea descriptions", "err", err)
					}
				}
				if recovered {
					if err := s.prepareIdeaDescription(ctx); err != nil {
						s.log.Warn("background idea description failed", "err", err)
						delay = 30 * time.Second
					}
				}
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}()
	})
	return s.ideaDescriptionStop
}

func (s *Server) prepareIdeaDescription(parent context.Context) error {
	if s.ai == nil || !s.ai.Enabled() {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	settings, err := s.settings(ctx)
	if err != nil {
		return err
	}
	deployment := s.foundryDeployment(settings)
	if deployment == "" {
		return nil
	}
	if s.foundry != nil {
		if _, err := s.foundry.ResolveChat(ctx, deployment); err != nil {
			return err
		}
	}
	job, err := s.store.ClaimIdeaDescription(ctx)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	providerErr := s.ai.DescribeIdea(ctx, deployment, job)
	job.Status = "ready"
	if providerErr != nil {
		job.Status = "unavailable"
	}
	saveCtx, stop := context.WithTimeout(context.WithoutCancel(parent), 2*time.Second)
	defer stop()
	if err := s.store.FinishIdeaDescription(saveCtx, job); err != nil {
		return err
	}
	return providerErr
}
