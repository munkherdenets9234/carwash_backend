package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// GeoPoint is a WGS-84 coordinate pair. Stored as two named fields rather
// than GeoJSON because nothing here runs a geospatial query — the only
// distance calculation is a single haversine against one known point, done
// in Go (see pkg/geo).
type GeoPoint struct {
	Lat float64 `bson:"lat" json:"lat"`
	Lng float64 `bson:"lng" json:"lng"`
}

// Location is one wash site.
type Location struct {
	ID primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	// TenantID is the business this row belongs to. Every query filters on
	// it, in the filter itself rather than as a check after the read, so a
	// forgotten scope is an empty result rather than another business data.
	//
	// json:"-" because it is never on the wire: a client already proved
	// which tenant it is by presenting the API key, and echoing the id back
	// tells it nothing it can use.
	TenantID primitive.ObjectID `bson:"tenant_id" json:"-"`
	Name     string             `bson:"name" json:"name"`
	Address  string             `bson:"address" json:"address"`
	Point    GeoPoint           `bson:"point" json:"point"`

	// GeofenceRadiusM is how far from Point a clock-in is still accepted.
	//
	// Per-location, not a service-wide constant, because sites differ: a
	// forecourt works at 75 m, while a bay inside a shopping centre needs
	// 200 m before honest staff start failing on indoor GPS drift. One
	// global radius would have to be set to the loosest site, which makes
	// the tightest one meaningless.
	GeofenceRadiusM float64 `bson:"geofence_radius_m" json:"geofence_radius_m"`

	Active    bool      `bson:"active" json:"active"`
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}
