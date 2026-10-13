package route

import "testing"

func TestHaversine(t *testing.T) {
	// Berlin to Paris is ~878 km.
	berlin := Point{Lat: 52.52, Lng: 13.405}
	paris := Point{Lat: 48.8566, Lng: 2.3522}
	km := Haversine(berlin, paris) / 1000
	if km < 850 || km > 900 {
		t.Fatalf("Berlin-Paris distance = %.0f km, want ~878", km)
	}
	if Haversine(berlin, berlin) != 0 {
		t.Fatal("distance to self must be 0")
	}
}

func TestEnabled(t *testing.T) {
	if New("").Enabled() {
		t.Fatal("empty key must be disabled")
	}
	if !New("  key  ").Enabled() {
		t.Fatal("non-empty key must be enabled")
	}
}

func TestCachedNormalizesKeysWithoutRouting(t *testing.T) {
	client := New("test")
	points := []Point{{Lat: 48, Lng: 16}, {Lat: 49, Lng: 17}}
	if _, found := client.Cached("", "", points); found {
		t.Fatal("empty cache returned a route")
	}
	client.store(cacheKey(DefaultBaseURL, DefaultProfile, points), Result{TotalDistanceM: 1234, TotalDurationS: 120})
	for _, base := range []string{"", " " + DefaultBaseURL + "/ "} {
		result, found := client.Cached(base, " ", points)
		if !found || result.TotalDistanceM != 1234 || result.TotalDurationS != 120 {
			t.Fatal("read-only lookup did not reuse the normalized cache key", result, found)
		}
	}
	client.Forget(DefaultBaseURL, DefaultProfile, points)
	if _, found := client.Cached("", "", points); found {
		t.Fatal("forgotten route was reused")
	}
}
