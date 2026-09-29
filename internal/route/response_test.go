package route

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRouteWithoutInstructionsUsesSummary(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request orsRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Instructions || !request.Geometry {
			t.Error("two-point requests should request geometry without turn instructions")
		}
		// ORS omits segments when instructions=false, but still returns the
		// complete distance/duration summary and requested road geometry.
		_, _ = w.Write([]byte("{\"routes\":[{\"summary\":{\"distance\":12345,\"duration\":4567},\"geometry\":\"_p~iF~ps|U_ulLnnqC_mqNvxq`@\"}]}"))
	}))
	defer provider.Close()
	client := New("test")
	points := []Point{{38.5, -120.2}, {43.252, -126.453}}
	for range 2 {
		result, err := client.Route(t.Context(), provider.URL, "", points)
		if err != nil || len(result.Legs) != 1 || result.Legs[0] != (Leg{12345, 4567}) ||
			result.TotalDistanceM != 12345 || result.TotalDurationS != 4567 || len(result.Geometry) != 3 {
			t.Fatalf("valid summary-only ORS response rejected or lost: %+v, %v", result, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("valid result was not cached")
	}
}

func TestRouteValidatesProviderMetricsAndLegCounts(t *testing.T) {
	for _, test := range []struct {
		name, body         string
		points             int
		wantOK             bool
		distance, duration float64
	}{
		{"explicit zero summary", `{"summary":{"distance":0,"duration":0}}`, 2, true, 0, 0},
		{"segments without summary", `{"segments":[{"distance":12,"duration":34}]}`, 2, true, 12, 34},
		{"multi-leg", `{"summary":{"distance":30,"duration":90},"segments":[{"distance":10,"duration":40},{"distance":20,"duration":50}]}`, 3, true, 30, 90},
		{"multi-leg segment totals", `{"segments":[{"distance":10,"duration":40},{"distance":20,"duration":50}]}`, 3, true, 30, 90},
		{"missing summary", `{}`, 2, false, 0, 0},
		{"null summary", `{"summary":null}`, 2, false, 0, 0},
		{"empty summary", `{"summary":{}}`, 2, false, 0, 0},
		{"missing duration", `{"summary":{"distance":50}}`, 2, false, 0, 0},
		{"null distance", `{"summary":{"distance":null,"duration":50}}`, 2, false, 0, 0},
		{"negative distance", `{"summary":{"distance":-1,"duration":50}}`, 2, false, 0, 0},
		{"negative duration", `{"summary":{"distance":1,"duration":-1}}`, 2, false, 0, 0},
		{"overflowing summary", `{"summary":{"distance":1e309,"duration":50}}`, 2, false, 0, 0},
		{"invalid segment", `{"summary":{"distance":10,"duration":50},"segments":[{}]}`, 2, false, 0, 0},
		{"invalid summary despite valid segment", `{"summary":{},"segments":[{"distance":10,"duration":50}]}`, 2, false, 0, 0},
		{"cannot split total into multiple legs", `{"summary":{"distance":10,"duration":50}}`, 3, false, 0, 0},
		{"incorrect segment count", `{"segments":[{"distance":10,"duration":50}]}`, 3, false, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var request orsRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if request.Instructions != (test.points > 2) {
					t.Error("multi-point requests must retain provider segment details")
				}
				_, _ = w.Write([]byte(`{"routes":[` + test.body + `]}`))
			}))
			defer provider.Close()
			client := New("test")
			for range 2 {
				result, err := client.Route(t.Context(), provider.URL, "", make([]Point, test.points))
				if (err == nil) != test.wantOK {
					t.Fatalf("unexpected validation: %+v %v", result, err)
				}
				if test.wantOK && (len(result.Legs) != test.points-1 || result.TotalDistanceM != test.distance || result.TotalDurationS != test.duration) {
					t.Fatalf("incorrect totals or legs: %+v", result)
				}
			}
			wantCalls := int32(2)
			if test.wantOK {
				wantCalls = 1
			}
			if calls.Load() != wantCalls {
				t.Fatalf("incorrect cache behavior: %d calls", calls.Load())
			}
		})
	}
}
