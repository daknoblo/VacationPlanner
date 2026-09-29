package route

import (
	"reflect"
	"strings"
	"testing"
)

func TestDecodeRoadGeometry(t *testing.T) {
	points, err := decodeGeometry("_p~iF~ps|U_ulLnnqC_mqNvxq`@")
	want := [][2]float64{{38.5, -120.2}, {40.7, -120.95}, {43.252, -126.453}}
	if err != nil || !reflect.DeepEqual(points, want) {
		t.Fatalf("incorrect provider polyline: %v %v", points, err)
	}
	for _, bad := range []string{"", "?", "??", "_", "!", strings.Repeat("~", 8), strings.Repeat("?", 40002), strings.Repeat("?", 200001)} {
		if _, err := decodeGeometry(bad); err == nil {
			t.Fatalf("accepted invalid/oversized geometry of length %d", len(bad))
		}
	}
}
