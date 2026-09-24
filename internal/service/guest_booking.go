package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/pkg/apierr"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Booking without an account.
//
// The decision this file implements: a customer should not have to register
// to buy a car wash. Requiring an account before the first booking asks
// somebody to commit to a relationship with a business they have not bought
// anything from yet, and the usual result is that they telephone instead, or
// go elsewhere.
//
// What it is NOT is a second booking system. Everything that makes a booking
// correct — the roster check, the overlap check, the slot key that stops two
// customers taking one time, the price and bonus copied at booking time —
// lives in ReservationService.Book and is reached unchanged. A guest booking
// differs from a registered one in exactly one respect: who the customer is
// and how they were identified. So that is all this does.
//
// The identity is a phone number, and the one thing to be clear about is
// that a phone number PROVES NOTHING. Anybody can type anybody's number.
// It groups a returning customer's visits so the business sees one person
// rather than five; it is never, on its own, permission to read a booking
// back. That needs the reference code as well — see
// ReservationService.LookupByReference.

// GuestBookInput is a booking from somebody with no account.
type GuestBookInput struct {
	// Name is optional. A business would like it, but refusing a booking
	// because somebody would rather not give their name loses the sale over
	// a field that is decoration on a car wash: the plate identifies the
	// car, and the phone reaches the person.
	Name  string
	Phone string
	Plate string

	EmployeeID string
	ServiceID  string
	LocationID string
	StartAt    time.Time
	Notes      string
}

// BookAsGuest books a wash for somebody who has not registered.
//
// Find-or-create, twice: the customer by phone, then their car by plate. A
// returning guest is recognised by both, so a business accumulates a real
// customer history rather than a pile of one-visit strangers — which is the
// only reason worth asking for the number at all.
func (s *ReservationService) BookAsGuest(ctx context.Context, tenantID primitive.ObjectID, in GuestBookInput) (*models.Reservation, error) {
	phone := models.NormalizePhone(in.Phone)
	if phone == "" {
		return nil, apierr.ValidationFailed("a phone number is required, so the business can reach you about this booking")
	}
	if len(phone) < 6 {
		return nil, apierr.ValidationFailed("that phone number is too short")
	}

	plate := repository.NormalizePlate(in.Plate)
	if plate == "" {
		return nil, apierr.ValidationFailed("a number plate is required")
	}

	customer, err := s.findOrCreateGuest(ctx, tenantID, phone, in.Name)
	if err != nil {
		return nil, err
	}

	car, err := s.findOrCreateCar(ctx, tenantID, customer.ID, plate)
	if err != nil {
		return nil, err
	}

	return s.Book(ctx, tenantID, customer.ID, BookInput{
		EmployeeID: in.EmployeeID,
		CarID:      car.ID.Hex(),
		ServiceID:  in.ServiceID,
		LocationID: in.LocationID,
		StartAt:    in.StartAt,
		Notes:      in.Notes,
	})
}

// findOrCreateGuest resolves the customer behind a phone number.
//
// An existing customer is REUSED whatever they are — a registered account
// with a password included. That is deliberate: if somebody registered last
// year and books as a guest today because they cannot remember their
// password, the booking belongs in their history, not in a duplicate record
// that splits it. The booking is a write they asked for, not a read of
// anything they would need to prove ownership of.
func (s *ReservationService) findOrCreateGuest(ctx context.Context, tenantID primitive.ObjectID, phone, name string) (*models.User, error) {
	existing, err := s.users.FindByContactKey(ctx, tenantID, phone)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return nil, apierr.Internal(err)
	}

	name = strings.TrimSpace(name)
	if name == "" {
		name = "Guest " + phone
	}

	u := &models.User{
		TenantID: tenantID,
		Role:     models.RoleCustomer,
		Name:     name,
		Phone:    phone,
		Status:   models.UserActive,
		// No Email and no PasswordHash. This account cannot be signed in to,
		// which is the point: it is a record of a customer, not a set of
		// credentials nobody chose. The sparse unique index on login_key is
		// what allows a user row with no email to exist at all.
	}
	if err := s.users.Create(ctx, u); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			// Two bookings from a number the business has never seen,
			// arriving together. The unique index on contact_key refused the
			// second insert, which is correct; the second caller simply
			// wanted the row that now exists.
			again, err := s.users.FindByContactKey(ctx, tenantID, phone)
			if err != nil {
				return nil, apierr.Internal(err)
			}
			return again, nil
		}
		return nil, apierr.Internal(err)
	}
	return u, nil
}

// findOrCreateCar resolves a plate to one of this customer's cars.
func (s *ReservationService) findOrCreateCar(ctx context.Context, tenantID, ownerID primitive.ObjectID, plate string) (*models.Car, error) {
	existing, err := s.cars.FindByPlateForOwner(ctx, tenantID, ownerID, plate)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return nil, apierr.Internal(err)
	}

	c := &models.Car{TenantID: tenantID, OwnerID: ownerID, Plate: plate}
	if err := s.cars.Create(ctx, c); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			// Same race as above, one level down.
			again, err := s.cars.FindByPlateForOwner(ctx, tenantID, ownerID, plate)
			if err != nil {
				return nil, apierr.Internal(err)
			}
			return again, nil
		}
		return nil, apierr.Internal(err)
	}
	return c, nil
}

// LookupByReference returns one booking to somebody holding its code.
//
// Both halves are required, and each covers the other's weakness. The code
// is the secret, but it is a secret that gets screenshotted, forwarded and
// left in chat threads. The phone number is not secret at all, but it is
// something only the person who booked and the business know to pair with
// that code.
//
// A wrong code and a right code with the wrong number give the SAME answer.
// That is not politeness: distinguishing them would turn this into an oracle
// for testing whether a code is real, and the reference space is only
// valuable while it cannot be probed.
func (s *ReservationService) LookupByReference(ctx context.Context, tenantID primitive.ObjectID, reference, phone string) (*models.Reservation, error) {
	ref := models.NormalizeReference(reference)
	if ref == "" {
		return nil, apierr.NotFound("booking")
	}
	phone = models.NormalizePhone(phone)
	if phone == "" {
		return nil, apierr.NotFound("booking")
	}

	customer, err := s.users.FindByContactKey(ctx, tenantID, phone)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// Not "no customer with that number" — that would confirm which
			// numbers the business has.
			return nil, apierr.NotFound("booking")
		}
		return nil, apierr.Internal(err)
	}

	res, err := s.bookings.FindByReference(ctx, tenantID, ref, customer.ID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apierr.NotFound("booking")
		}
		return nil, apierr.Internal(err)
	}
	return res, nil
}
