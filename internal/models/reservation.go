package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type ReservationStatus string

const (
	ReservationBooked     ReservationStatus = "booked"
	ReservationInProgress ReservationStatus = "in_progress"
	ReservationCompleted  ReservationStatus = "completed"
	ReservationCancelled  ReservationStatus = "cancelled"
	ReservationNoShow     ReservationStatus = "no_show"
)

// Reservation is one booked wash: a customer's car, an employee, a service,
// a window.
type Reservation struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	CustomerID primitive.ObjectID `bson:"customer_id" json:"customer_id"`

	// EmployeeID is chosen by the customer at booking time and is what ties
	// the bonus to a person. A manager may reassign it while the job is
	// still open; once completed it is frozen, because the bonus has already
	// been earned by whoever did the work.
	EmployeeID primitive.ObjectID `bson:"employee_id" json:"employee_id"`

	CarID      primitive.ObjectID `bson:"car_id" json:"car_id"`
	ServiceID  primitive.ObjectID `bson:"service_id" json:"service_id"`
	LocationID primitive.ObjectID `bson:"location_id" json:"location_id"`

	StartAt time.Time `bson:"start_at" json:"start_at"`
	EndAt   time.Time `bson:"end_at" json:"end_at"`

	Status ReservationStatus `bson:"status" json:"status"`

	// PriceMNT and BonusMNT are copied from the service at booking time, not
	// read through at report time.
	//
	// This is the difference between a report that reproduces last Tuesday
	// and one that reprices it. Editing the price list must not retroactively
	// change revenue already banked or a bonus already paid, and a join to
	// the live service row would do exactly that — silently, months later.
	PriceMNT MNT `bson:"price_mnt" json:"price_mnt"`
	BonusMNT MNT `bson:"bonus_mnt" json:"bonus_mnt"`

	// SlotKey is employee + start instant, present only while this booking
	// holds its slot, and covered by a unique sparse index.
	//
	// The service already checks availability before inserting, but that
	// check and the insert are two round trips: two customers who load the
	// same page and tap 14:00 together both pass it. This key makes the
	// database refuse the second one. It guards identical start times only,
	// which is the case that actually happens because slots are anchored to
	// a fixed step — a partial overlap from a manual reschedule can still
	// slip through, and closing that needs a transaction on a replica set.
	// Recorded rather than left implied.
	SlotKey string `bson:"slot_key,omitempty" json:"-"`

	Notes string `bson:"notes,omitempty" json:"notes,omitempty"`

	CompletedAt *time.Time `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
	CancelledAt *time.Time `bson:"cancelled_at,omitempty" json:"cancelled_at,omitempty"`

	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// Blocking reports whether this reservation still occupies its slot. A
// cancelled or no-show booking frees the time; every other state holds it.
func (r Reservation) Blocking() bool {
	switch r.Status {
	case ReservationCancelled, ReservationNoShow:
		return false
	}
	return true
}

// Open reports whether the job is still live — reassignable by a manager,
// and cancellable by the customer.
func (r Reservation) Open() bool {
	switch r.Status {
	case ReservationBooked, ReservationInProgress:
		return true
	}
	return false
}
