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
	ID primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	// TenantID is the business this row belongs to. Every query filters on
	// it, in the filter itself rather than as a check after the read, so a
	// forgotten scope is an empty result rather than another business data.
	//
	// json:"-" because it is never on the wire: a client already proved
	// which tenant it is by presenting the API key, and echoing the id back
	// tells it nothing it can use.
	TenantID   primitive.ObjectID `bson:"tenant_id" json:"-"`
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

	// Reference is the code a guest is given to find this booking again.
	//
	// It exists because booking no longer requires an account, which leaves
	// nothing else that proves the asker is the person who booked. A phone
	// number does not: anybody can type anybody's number, so a lookup keyed
	// on the number alone would hand a stranger a name, a plate, a time and
	// a place — enough to know when a particular car is away from a
	// particular address. The reference is the secret half; the phone number
	// is the half that stops a leaked code being useful on its own.
	//
	// Present on every booking, including ones made by signed-in customers,
	// so that a single lookup path serves both and there is no second,
	// less-tested one for guests.
	Reference string `bson:"reference,omitempty" json:"reference,omitempty"`

	// ReferenceKey is tenant + reference, and backs the sparse unique index
	// that makes a code mean exactly one booking. Derived rather than
	// compound — see models.ReferenceKey for why the compound form does not
	// work.
	ReferenceKey string `bson:"reference_key,omitempty" json:"-"`

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
