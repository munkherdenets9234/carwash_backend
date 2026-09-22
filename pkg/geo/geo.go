// Package geo holds the one piece of geography this service needs: how far
// apart two coordinates are.
//
// It is a package rather than a helper inside the attendance service because
// it is the rule an employee's pay depends on, and a rule like that should be
// testable without a database, an HTTP request or a clock. See geo_test.go.
package geo

import "math"

// earthRadiusM is the mean radius. The haversine below assumes a sphere,
// which is wrong by up to ~0.5% against the real ellipsoid. At the distances
// that matter here — tens to hundreds of metres — that is centimetres, far
// inside consumer GPS error, so the extra complexity of Vincenty would buy
// precision the input does not have.
const earthRadiusM = 6371008.8

// DistanceM returns the great-circle distance in metres between two
// WGS-84 coordinates.
func DistanceM(lat1, lng1, lat2, lng2 float64) float64 {
	p1 := lat1 * math.Pi / 180
	p2 := lat2 * math.Pi / 180
	dp := (lat2 - lat1) * math.Pi / 180
	dl := (lng2 - lng1) * math.Pi / 180

	a := math.Sin(dp/2)*math.Sin(dp/2) +
		math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * earthRadiusM * math.Asin(math.Sqrt(a))
}

// ValidCoordinate reports whether a latitude/longitude pair is in range.
//
// Worth checking explicitly: a phone that has not got a fix yet reports
// (0, 0), which is a real coordinate in the Gulf of Guinea. Without this the
// distance check would simply return "several thousand kilometres away" and
// the employee would be told they are at the wrong branch rather than that
// their location is not available yet.
func ValidCoordinate(lat, lng float64) bool {
	if math.IsNaN(lat) || math.IsNaN(lng) || math.IsInf(lat, 0) || math.IsInf(lng, 0) {
		return false
	}
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return false
	}
	// Exactly (0, 0) is treated as "no fix". A wash site there is not a case
	// worth supporting at the cost of misreporting the common one.
	return !(lat == 0 && lng == 0)
}
