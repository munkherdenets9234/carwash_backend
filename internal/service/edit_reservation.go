package service

import (
	"context"
	"errors"
	"time"

	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/pkg/apierr"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Correcting a booking that is already in the book.
//
// Three fields, and each one exists because it is a mistake somebody actually
// makes at a desk: the plate was mistyped, the wrong service was tapped, or
// the car went in half an hour before anyone got round to registering it.
//
// Who may do it is the manager, and WHEN is deliberately wider than
// Reassign allows. Reassign refuses a completed job because moving a bonus
// somebody has already been told they earned is a different act from fixing a
// typo. Here the manager has decided a finished job is wrong, and a correction
// that cannot be applied to yesterday is not a correction — it is a second
// wrong number sitting next to the first. The day report follows: it is
// computed from these rows, so yesterday's figure changes when yesterday's row
// does. That is the intended behaviour and the reason this is manager-only.
//
// A cancelled or no-show booking is NOT editable. Nothing happened, so there
// is nothing to correct; registering it again is both easier to do and easier
// to read afterwards than resurrecting a row that says it never took place.
//
// Like the walk-in desk, this does not consult the roster and does not check
// for overlap. The manager is the one holding the information here.
type EditReservationInput struct {
	// Each is optional. A nil field is left alone, so a screen that edits one
	// thing does not have to send — and risk clobbering — the other two.
	Plate     *string
	ServiceID *string
	StartAt   *time.Time
}

// EditReservation applies a manager's correction.
func (s *ReservationService) EditReservation(
	ctx context.Context,
	tenantID primitive.ObjectID,
	id primitive.ObjectID,
	in EditReservationInput,
) (*models.Reservation, error) {
	if in.Plate == nil && in.ServiceID == nil && in.StartAt == nil {
		return nil, apierr.ValidationFailed("nothing to change")
	}

	res, err := s.Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if res.Status == models.ReservationCancelled || res.Status == models.ReservationNoShow {
		return nil, apierr.Conflict("a cancelled booking cannot be edited — register the wash again instead").
			In(apierr.DomainReservation)
	}

	set := bson.M{}

	// The duration in force after this edit. The end time is derived from it
	// whenever either half of the pair moves, so a service change alone still
	// corrects an end time that was computed from the old one.
	duration := res.EndAt.Sub(res.StartAt)

	if in.ServiceID != nil {
		svcID, err := primitive.ObjectIDFromHex(*in.ServiceID)
		if err != nil {
			return nil, apierr.BadRequest("service_id is not a valid id")
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

		duration = time.Duration(ws.DurationMin) * time.Minute
		res.ServiceID = svcID
		res.PriceMNT = ws.PriceMNT
		res.BonusMNT = ws.BonusMNT
		// Re-copied, not re-referenced. The price on a booking is what was
		// charged, so correcting the service corrects both numbers with it —
		// and the day report, which reads these two fields and not the
		// catalogue.
		set["service_id"] = svcID
		set["price_mnt"] = ws.PriceMNT
		set["bonus_mnt"] = ws.BonusMNT
	}

	if in.Plate != nil {
		plate := repository.NormalizePlate(*in.Plate)
		if plate == "" {
			return nil, apierr.ValidationFailed("a number plate is required")
		}
		// Resolved first and compared by id afterwards: the plate stored
		// against this booking lives on the car, not on the booking, so
		// "has it changed" is a question about which car row the plate
		// belongs to rather than about the two strings.
		car, err := s.findOrCreateWalkInCar(ctx, tenantID, plate)
		if err != nil {
			return nil, err
		}
		if car.ID != res.CarID {
			// The customer moves with the car. A booking belongs to whoever
			// owns the vehicle being washed, and a plate corrected from one
			// car to another is a correction about which customer this was.
			res.CarID = car.ID
			res.CustomerID = car.OwnerID
			set["car_id"] = car.ID
			set["customer_id"] = car.OwnerID
		}
	}

	if in.StartAt != nil {
		start := in.StartAt.UTC().Truncate(time.Second)
		res.StartAt = start
		set["start_at"] = start
		// The slot key is what stops two jobs landing on one washer at one
		// instant, and it exists only while a booking is live — completing or
		// cancelling one releases it. Rewriting it on a finished job would put
		// a released key back into the unique index and block a real booking
		// later, so it moves only while the row is still open.
		if res.Open() {
			set["slot_key"] = models.SlotKey(tenantID, res.EmployeeID, start)
		}
	}

	if in.StartAt != nil || in.ServiceID != nil {
		res.EndAt = res.StartAt.Add(duration)
		set["end_at"] = res.EndAt
	}

	if len(set) == 0 {
		// Everything sent matched what is already stored.
		return res, nil
	}

	if err := s.bookings.UpdateFields(ctx, tenantID, id, set); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			return nil, apierr.SlotUnavailable("that washer already has a job starting at that moment")
		}
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apierr.NotFound("reservation").In(apierr.DomainReservation)
		}
		return nil, apierr.Internal(err)
	}
	return res, nil
}
