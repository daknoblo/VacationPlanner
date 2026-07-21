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
