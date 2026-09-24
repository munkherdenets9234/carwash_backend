package view

import "github.com/eandstravel/carwash/internal/models"

// GuestBooking is a booking as the person who made it sees it, without an
// account.
//
// It is view.Reservation plus the reference code, and deliberately nothing
// else. In particular it does not name the customer: whoever is reading this
// either booked it or was given the code and the phone number by somebody
// who did, and in neither case does adding the customer's name tell them
// something they did not already have. A response that repeats personal data
// back is a response that leaks it the day somebody forwards a screenshot.
//
// The car is present because the plate is how a person recognises which of
// their bookings this is, and they typed it themselves.
type GuestBooking struct {
	Reservation

	// Reference is shown so it can be written down. It is returned on the
	// booking response and on a successful lookup, and nowhere else — there
	// is no route that will tell somebody the code for a booking they cannot
	// already identify, because the code is the only thing standing between
	// a phone number and the booking behind it.
	//
	// Formatted with its dash for reading aloud; the stored value has none.
	Reference string `json:"reference"`
}

// GuestBookingOf builds the guest view of one booking.
func GuestBookingOf(r *models.Reservation, l Lookup) GuestBooking {
	return GuestBooking{
		Reservation: ReservationOf(r, l),
		Reference:   models.FormatReference(r.Reference),
	}
}
