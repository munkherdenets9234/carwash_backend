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

// Registering a car that is already on the forecourt.
//
// This is the desk, not the website. Somebody has driven in, the manager has
// thirty seconds and a queue behind them, and the only thing they can read
// off the car is the plate. So the form is a plate, a service and a washer,
// and everything else is inferred: the time is now, the customer is whoever
// owns that plate, and the job starts immediately.
//
// Three rules the online flow enforces are deliberately NOT enforced here:
//
//  1. The start time may be now rather than in the future. Online, a booking
//     in the past is a mistake; here it is the entire point.
//  2. The roster is not consulted. A walk-in arrives when it arrives, and
//     refusing to record work that is visibly happening because nobody
//     rostered that hour would send the manager to a paper notebook — and
//     the moment that happens the day report stops being true.
//  3. Overlap with the washer's other jobs is allowed. A manager standing in
//     front of both cars knows something the roster does not.
//
// What it does NOT relax: a suspended employee is still refused, an inactive
// service or location is still refused, and the price and bonus are still
// copied from the catalogue at registration time. Those are not scheduling
// rules — they are what makes the row correct afterwards.
type WalkInInput struct {
	Plate      string
	EmployeeID string
	ServiceID  string
	LocationID string
	Notes      string
}

// RegisterWalkIn records a wash that is starting now.
func (s *ReservationService) RegisterWalkIn(
	ctx context.Context,
	tenantID primitive.ObjectID,
	in WalkInInput,
) (*models.Reservation, error) {
	plate := repository.NormalizePlate(in.Plate)
	if plate == "" {
		return nil, apierr.ValidationFailed("a number plate is required")
	}

	empID, err := primitive.ObjectIDFromHex(in.EmployeeID)
	if err != nil {
		return nil, apierr.BadRequest("employee_id is not a valid id")
	}
	svcID, err := primitive.ObjectIDFromHex(in.ServiceID)
	if err != nil {
		return nil, apierr.BadRequest("service_id is not a valid id")
	}
	locID, err := primitive.ObjectIDFromHex(in.LocationID)
	if err != nil {
		return nil, apierr.BadRequest("location_id is not a valid id")
	}

	ws, err := s.services.FindByID(ctx, tenantID, svcID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apierr.NotFound("service").In(apierr.DomainCatalog)
		}
		return nil, apierr.Internal(err)
	}
	if !ws.Active {
		return nil, apierr.ValidationFailed("that service is not currently offered")
	}

	loc, err := s.locations.FindByID(ctx, tenantID, locID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apierr.NotFound("location").In(apierr.DomainCatalog)
		}
		return nil, apierr.Internal(err)
	}
	if !loc.Active {
		return nil, apierr.ValidationFailed("that location is closed")
	}

	emp, err := s.users.FindByIDAndRole(ctx, tenantID, empID, models.RoleEmployee)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apierr.NotFound("employee")
		}
		return nil, apierr.Internal(err)
	}
	if emp.Status != models.UserActive {
		return nil, apierr.ValidationFailed("that employee is not currently working here")
	}

	car, err := s.findOrCreateWalkInCar(ctx, tenantID, plate)
	if err != nil {
		return nil, err
	}

	start := s.now().UTC().Truncate(time.Second)
	end := start.Add(time.Duration(ws.DurationMin) * time.Minute)

	reference, err := models.NewReference()
	if err != nil {
		return nil, apierr.Internal(err)
	}

	res := &models.Reservation{
		TenantID:     tenantID,
		CustomerID:   car.OwnerID,
		Reference:    reference,
		ReferenceKey: models.ReferenceKey(tenantID, reference),
		EmployeeID:   empID,
		CarID:        car.ID,
		ServiceID:    svcID,
		LocationID:   locID,
		StartAt:      start,
		EndAt:        end,
		// The car is here and the washer is taking it now. Anything else
		// would mean the manager registers the job and then somebody has to
		// remember to start it, which is the step that gets skipped.
		Status:   models.ReservationInProgress,
		PriceMNT: ws.PriceMNT,
		BonusMNT: ws.BonusMNT,
		SlotKey:  models.SlotKey(tenantID, empID, start),
		Notes:    strings.TrimSpace(in.Notes),
	}

	if err := s.bookings.Create(ctx, res); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			// Overlap is allowed here, so this is not a scheduling clash: the
			// slot key is per SECOND, and two rows for one washer in the same
			// second is a double-tap on the button, not two cars.
			return nil, apierr.ValidationFailed("that car has just been registered — check the job list before trying again")
		}
		return nil, apierr.Internal(err)
	}
	return res, nil
}

// findOrCreateWalkInCar resolves a plate to a vehicle and an owner.
//
// A plate the business has seen before keeps its existing owner, whoever that
// is — a registered customer who happens to have driven in today gets this
// wash in the history they already have, rather than a second identity that
// splits it.
//
// A plate nobody has seen gets a customer of its own, named after the plate.
// A reservation needs a customer, and the alternative — one shared "Walk-in"
// account holding every stranger's car — would make each returning driver
// indistinguishable from the others in the very list a manager uses to
// recognise them. A row per plate is the smallest honest record of "one car
// we washed", and it costs nothing: it has no email, no password and no way
// to sign in, exactly like a guest booked by phone.
func (s *ReservationService) findOrCreateWalkInCar(
	ctx context.Context,
	tenantID primitive.ObjectID,
	plate string,
) (*models.Car, error) {
	existing, err := s.cars.FindByPlate(ctx, tenantID, plate)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return nil, apierr.Internal(err)
	}

	owner := &models.User{
		TenantID: tenantID,
		Role:     models.RoleCustomer,
		Name:     plate,
		Status:   models.UserActive,
	}
	if err := s.users.Create(ctx, owner); err != nil {
		return nil, apierr.Internal(err)
	}

	car := &models.Car{TenantID: tenantID, OwnerID: owner.ID, Plate: plate}
	if err := s.cars.Create(ctx, car); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			// Two registrations of one new plate at once. The second caller
			// wants the row the first just made.
			again, err := s.cars.FindByPlate(ctx, tenantID, plate)
			if err != nil {
				return nil, apierr.Internal(err)
			}
			return again, nil
		}
		return nil, apierr.Internal(err)
	}
	return car, nil
}
