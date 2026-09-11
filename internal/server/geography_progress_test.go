package server

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestGeographyPublishesLiveProgress(t *testing.T) {
	var calls atomic.Int32
	blocked, release := make(chan struct{}), make(chan struct{})
	s, st, v := newGeographyTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 2 {
			close(blocked)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = w.Write([]byte(`{"lat":"50","lon":"1","display_name":"Test place","address":{"state":"Region","country":"Country"}}`))
	})
	for _, lat := range []float64{50, 51} {
		lng := 1.0
		item := &models.Item{VacationID: v.ID, Title: "Located idea", Latitude: &lat, Longitude: &lng}
		if err := st.CreateItem(context.Background(), item); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := s.StartGeographyWorker(ctx)
	defer stop()
	s.queueGeography(v.ID, "en")
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("second lookup did not start")
	}
	status := s.geographyStatus(v.ID)
	if !status.Pending || status.Completed != 1 || status.Total != 2 {
		t.Fatalf("in-flight progress is incorrect: %+v", status)
	}
	view, err := s.backgroundStatus(context.Background())
	if err != nil || !view.Determinate || view.Completed != 1 || view.Total != 2 {
		t.Fatalf("header progress is incorrect: %+v %v", view, err)
	}
	close(release)
	status = waitGeography(t, s, v.ID)
	if status.Pending || status.Completed != 2 || status.Total != 2 {
		t.Fatalf("finished progress is incorrect: %+v", status)
	}
}
