package route

import "fmt"

// ORS JSON directions encode two-dimensional road geometry using Google's
// precision-5 polyline format. Bound both encoded input and decoded vertices.
func decodeGeometry(encoded string) ([][2]float64, error) {
	if len(encoded) > 200000 {
		return nil, fmt.Errorf("route: geometry too large")
	}
	pos := 0
	decode := func() (int64, error) {
		var value int64
		for shift := uint(0); shift <= 30 && pos < len(encoded); shift += 5 {
			char := encoded[pos]
			pos++
			if char < 63 || char > 126 {
				return 0, fmt.Errorf("route: invalid geometry character")
			}
			part := int64(char - 63)
			value |= (part & 31) << shift
			if part < 32 {
				if value&1 != 0 {
					return ^(value >> 1), nil
				}
				return value >> 1, nil
			}
		}
		return 0, fmt.Errorf("route: truncated or overflowing geometry")
	}
	var latitude, longitude int64
	var points [][2]float64
	for pos < len(encoded) {
		lat, err := decode()
		if err != nil {
			return nil, err
		}
		lng, err := decode()
		if err != nil {
			return nil, err
		}
		latitude, longitude = latitude+lat, longitude+lng
		if latitude < -9000000 || latitude > 9000000 || longitude < -18000000 || longitude > 18000000 || len(points) >= 20000 {
			return nil, fmt.Errorf("route: invalid or excessive geometry coordinates")
		}
		points = append(points, [2]float64{float64(latitude) / 1e5, float64(longitude) / 1e5})
	}
	if len(points) < 2 {
		return nil, fmt.Errorf("route: geometry needs at least two points")
	}
	return points, nil
}
