package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// TimeEntry is one clock-in and, later, the matching clock-out.
//
// The coordinates and the measured distance are kept, not just the pass/fail
// verdict. When someone disputes a rejected clock-in, the only useful record
// is where the phone said they were and how far that was from the site;
// storing the verdict alone leaves the argument unresolvable.
type TimeEntry struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	EmployeeID primitive.ObjectID `bson:"employee_id" json:"employee_id"`
	LocationID primitive.ObjectID `bson:"location_id" json:"location_id"`

	ClockInAt        time.Time `bson:"clock_in_at" json:"clock_in_at"`
	ClockInPoint     GeoPoint  `bson:"clock_in_point" json:"clock_in_point"`
	ClockInDistanceM float64   `bson:"clock_in_distance_m" json:"clock_in_distance_m"`

	// OpenKey is the employee id, present only while this entry is running,
	// and covered by a unique sparse index. It is what makes "one open
	// entry per employee" a database constraint rather than a hopeful
	// read-then-write in the service.
	//
	// The obvious version of this index — unique on employee_id, filtered
	// to {clock_out_at: {$exists: false}} — does not exist: MongoDB rejects
	// it with "Expression not supported in partial index: $not", because
	// $exists:false compiles to a negation and partialFilterExpression
	// allows only $eq, $exists:true, the range operators, $type and $and.
	// A sparse unique index on a field that is present exactly when the
	// entry is open expresses the same rule with operators the server
	// accepts.
	OpenKey string `bson:"open_key,omitempty" json:"-"`

	ClockOutAt        *time.Time `bson:"clock_out_at,omitempty" json:"clock_out_at,omitempty"`
	ClockOutPoint     *GeoPoint  `bson:"clock_out_point,omitempty" json:"clock_out_point,omitempty"`
	ClockOutDistanceM *float64   `bson:"clock_out_distance_m,omitempty" json:"clock_out_distance_m,omitempty"`

	// WorkedMinutes is written once, at clock-out. Derived, but persisted:
	// the timesheet sums it across a month, and recomputing a subtraction
	// per row per read to save one int is a poor trade.
	WorkedMinutes int `bson:"worked_minutes" json:"worked_minutes"`

	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// Running reports whether this entry is still open — an employee is clocked
// in exactly when their most recent entry is running.
func (t TimeEntry) Running() bool { return t.ClockOutAt == nil }
