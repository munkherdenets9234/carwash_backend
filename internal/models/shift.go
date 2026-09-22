package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Shift is one employee rostered at one location for one window.
//
// Availability is derived from shifts rather than stored: "who is free at
// 14:00" is a shift minus the reservations overlapping it, computed on read.
// A materialised slot table would need invalidating on every booking,
// cancellation and roster edit, and the first missed invalidation is a
// double-booking.
type Shift struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	EmployeeID primitive.ObjectID `bson:"employee_id" json:"employee_id"`
	LocationID primitive.ObjectID `bson:"location_id" json:"location_id"`

	// Absolute instants, stored UTC. Mongo has no timezone-aware type, so a
	// local wall-clock time would turn ambiguous the moment a second branch
	// opens in another zone.
	StartAt time.Time `bson:"start_at" json:"start_at"`
	EndAt   time.Time `bson:"end_at" json:"end_at"`

	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}
