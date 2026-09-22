package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/pkg/apierr"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ReservationService books washes and moves them through their lifecycle.
type ReservationService struct {
	bookings  *repository.ReservationRepo
	shifts    *repository.ShiftRepo
	users     *repository.UserRepo
	cars      *repository.CarRepo
	services  *repository.WashServiceRepo
	locations *repository.LocationRepo

	maxDaysAhead int
	now          func() time.Time
}

func NewReservationService(
	bookings *repository.ReservationRepo,
	shifts *repository.ShiftRepo,
	users *repository.UserRepo,
	cars *repository.CarRepo,
	services *repository.WashServiceRepo,
	locations *repository.LocationRepo,
	maxDaysAhead int,
) *ReservationService {
	return &ReservationService{
		bookings:     bookings,
		shifts:       shifts,
		users:        users,
		cars:         cars,
		services:     services,
		locations:    locations,
		maxDaysAhead: maxDaysAhead,
		now:          func() time.Time { return time.Now().UTC() },
	}
}

// BookInput is a customer's booking request. The employee is part of it:
// choosing who washes your car is a feature here, not an internal detail.
type BookInput struct {
	EmployeeID string
	CarID      string
	ServiceID  string
	LocationID string
	StartAt    time.Time
	Notes      string
}

// slotKey is the value behind the unique index that stops two customers
// taking the same start time with the same employee. Second-precision UTC so
// the same instant always produces the same string.
func slotKey(employeeID primitive.ObjectID, start time.Time) string {
	return employeeID.Hex() + "|" + start.UTC().Format(time.RFC3339)
}

func (s *ReservationService) Book(ctx context.Context, customerID primitive.ObjectID, in BookInput) (*models.Reservation, error) {
	empID, err := primitive.ObjectIDFromHex(in.EmployeeID)
	if err != nil {
		return nil, apierr.BadRequest("employee_id is not a valid id")
	}
	carID, err := primitive.ObjectIDFromHex(in.CarID)
	if err != nil {
		return nil, apierr.BadRequest("car_id is not a valid id")
	}
	svcID, err := primitive.ObjectIDFromHex(in.ServiceID)
	if err != nil {
		return nil, apierr.BadRequest("service_id is not a valid id")
	}
	locID, err := primitive.ObjectIDFromHex(in.LocationID)
	if err != nil {
		return nil, apierr.BadRequest("location_id is not a valid id")
	}

	now := s.now()
	start := in.StartAt.UTC().Truncate(time.Second)
	if !start.After(now) {
		return nil, apierr.ValidationFailed("start_at must be in the future")
	}
	if start.After(now.AddDate(0, 0, s.maxDaysAhead)) {
		return nil, apierr.ValidationFailed("that date is too far ahead")
	}

	// The car is looked up scoped to the caller, so booking a wash for
	// someone else's vehicle is a 404 rather than an authorisation check
	// somebody could forget to write.
	if _, err := s.cars.FindByIDForOwner(ctx, carID, customerID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apierr.NotFound("car")
		}
		return nil, apierr.Internal(err)
	}

	ws, err := s.services.FindByID(ctx, svcID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apierr.NotFound("service").In(apierr.DomainCatalog)
		}
		return nil, apierr.Internal(err)
	}
	if !ws.Active {
		return nil, apierr.ValidationFailed("that service is not currently offered")
	}

	loc, err := s.locations.FindByID(ctx, locID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apierr.NotFound("location").In(apierr.DomainCatalog)
		}
		return nil, apierr.Internal(err)
	}
	if !loc.Active {
		return nil, apierr.ValidationFailed("that location is closed")
	}

	emp, err := s.users.FindByIDAndRole(ctx, empID, models.RoleEmployee)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apierr.NotFound("employee")
		}
		return nil, apierr.Internal(err)
	}
	if emp.Status != models.UserActive {
		return nil, apierr.SlotUnavailable("that employee is not taking bookings")
	}

	end := start.Add(time.Duration(ws.DurationMin) * time.Minute)
	if err := s.assertFree(ctx, empID, locID, Interval{Start: start, End: end}, nil); err != nil {
		return nil, err
	}

	res := &models.Reservation{
		CustomerID: customerID,
		EmployeeID: empID,
		CarID:      carID,
		ServiceID:  svcID,
		LocationID: locID,
		StartAt:    start,
		EndAt:      end,
		Status:     models.ReservationBooked,
		// Copied, not referenced — see models.Reservation.
		PriceMNT: ws.PriceMNT,
		BonusMNT: ws.BonusMNT,
		SlotKey:  slotKey(empID, start),
		Notes:    strings.TrimSpace(in.Notes),
	}

	if err := s.bookings.Create(ctx, res); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			// Lost the race against another booking for the same start.
			return nil, apierr.SlotUnavailable("someone just took that time")
		}
		return nil, apierr.Internal(err)
	}
	return res, nil
}

// assertFree checks the roster and the existing bookings. exclude is the id
// of a reservation to ignore, used when moving an existing booking so it does
// not collide with itself.
func (s *ReservationService) assertFree(ctx context.Context, empID, locID primitive.ObjectID, job Interval, exclude *primitive.ObjectID) error {
	rostered, err := s.shifts.List(ctx, repository.ShiftQuery{
		EmployeeID: &empID,
		LocationID: &locID,
		From:       job.Start,
		To:         job.End,
	})
	if err != nil {
		return apierr.Internal(err)
	}
	windows := make([]Interval, 0, len(rostered))
	for _, sh := range rostered {
		windows = append(windows, Interval{Start: sh.StartAt, End: sh.EndAt})
	}

	taken, err := s.bookings.ListBlockingForEmployees(ctx, []primitive.ObjectID{empID}, job.Start, job.End)
	if err != nil {
		return apierr.Internal(err)
	}
	busy := make([]Interval, 0, len(taken))
	for _, b := range taken {
		if exclude != nil && b.ID == *exclude {
			continue
		}
		busy = append(busy, Interval{Start: b.StartAt, End: b.EndAt})
	}

	if !FitsInShift(windows, busy, job) {
		return apierr.SlotUnavailable("")
	}
	return nil
}

// ── Reads ─────────────────────────────────────────────────────────────────

func (s *ReservationService) List(ctx context.Context, q repository.ReservationQuery) ([]*models.Reservation, error) {
	out, err := s.bookings.List(ctx, q)
	if err != nil {
		return nil, apierr.Internal(err)
	}
	return out, nil
}

func (s *ReservationService) GetForCustomer(ctx context.Context, id, customerID primitive.ObjectID) (*models.Reservation, error) {
	res, err := s.bookings.FindByIDForCustomer(ctx, id, customerID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apierr.NotFound("reservation").In(apierr.DomainReservation)
		}
		return nil, apierr.Internal(err)
	}
	return res, nil
}

func (s *ReservationService) Get(ctx context.Context, id primitive.ObjectID) (*models.Reservation, error) {
	res, err := s.bookings.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apierr.NotFound("reservation").In(apierr.DomainReservation)
		}
		return nil, apierr.Internal(err)
	}
	return res, nil
}

// ── Lifecycle ─────────────────────────────────────────────────────────────

// CancelByCustomer withdraws a booking the caller owns.
func (s *ReservationService) CancelByCustomer(ctx context.Context, id, customerID primitive.ObjectID) error {
	res, err := s.GetForCustomer(ctx, id, customerID)
	if err != nil {
		return err
	}
	if !res.Open() {
		return apierr.Conflict("that booking can no longer be cancelled").In(apierr.DomainReservation)
	}
	return s.release(ctx, res.ID, models.ReservationCancelled)
}

// SetStatus moves a booking on. actorID is the employee acting, or nil for a
// manager, who may act on anyone's job.
//
// An employee may only touch their own work. Without that check any employee
// could mark any other employee's wash complete, which credits the bonus to
// the wrong person and is invisible until payroll.
func (s *ReservationService) SetStatus(ctx context.Context, id primitive.ObjectID, status models.ReservationStatus, actorEmployeeID *primitive.ObjectID) error {
	res, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if actorEmployeeID != nil && res.EmployeeID != *actorEmployeeID {
		return apierr.NotFound("reservation").In(apierr.DomainReservation)
	}

	if !allowedTransition(res.Status, status) {
		return apierr.Conflict("cannot move a " + string(res.Status) + " booking to " + string(status)).
			In(apierr.DomainReservation)
	}

	now := s.now()
	switch status {
	case models.ReservationInProgress:
		return s.update(ctx, id, bson.M{"status": status})

	case models.ReservationCompleted:
		// completed_at is what the daily report groups on, so it is written
		// here and never back-dated: the report is a record of when work
		// finished, not of when someone got round to pressing the button.
		return s.update(ctx, id, bson.M{"status": status, "completed_at": now})

	case models.ReservationCancelled, models.ReservationNoShow:
		return s.release(ctx, id, status)

	default:
		return apierr.ValidationFailed("unknown status")
	}
}

// Reassign moves an open job to a different employee — the manager's tool for
// covering an absence, and the point at which the bonus changes hands.
func (s *ReservationService) Reassign(ctx context.Context, id primitive.ObjectID, newEmployeeID string) error {
	res, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	// Completed work is frozen. Reassigning it would move a bonus that has
	// already been earned, and silently change a report that was already
	// read.
	if !res.Open() {
		return apierr.Conflict("only an open booking can be reassigned").In(apierr.DomainReservation)
	}

	empID, err := primitive.ObjectIDFromHex(newEmployeeID)
	if err != nil {
		return apierr.BadRequest("employee_id is not a valid id")
	}
	if empID == res.EmployeeID {
		return apierr.ValidationFailed("that booking is already assigned to this employee")
	}

	emp, err := s.users.FindByIDAndRole(ctx, empID, models.RoleEmployee)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return apierr.NotFound("employee")
		}
		return apierr.Internal(err)
	}
	if emp.Status != models.UserActive {
		return apierr.SlotUnavailable("that employee is not taking bookings")
	}

	job := Interval{Start: res.StartAt, End: res.EndAt}
	if err := s.assertFree(ctx, empID, res.LocationID, job, &res.ID); err != nil {
		return err
	}

	if err := s.update(ctx, id, bson.M{
		"employee_id": empID,
		"slot_key":    slotKey(empID, res.StartAt),
	}); err != nil {
		return err
	}
	return nil
}

func (s *ReservationService) update(ctx context.Context, id primitive.ObjectID, set bson.M) error {
	if err := s.bookings.UpdateFields(ctx, id, set); err != nil {
		switch {
		case errors.Is(err, repository.ErrNotFound):
			return apierr.NotFound("reservation").In(apierr.DomainReservation)
		case errors.Is(err, repository.ErrDuplicate):
			return apierr.SlotUnavailable("that employee already has a booking at that time")
		default:
			return apierr.Internal(err)
		}
	}
	return nil
}

func (s *ReservationService) release(ctx context.Context, id primitive.ObjectID, status models.ReservationStatus) error {
	set := bson.M{"status": status}
	if status == models.ReservationCancelled {
		set["cancelled_at"] = s.now()
	}
	if err := s.bookings.UpdateAndReleaseSlot(ctx, id, set); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return apierr.NotFound("reservation").In(apierr.DomainReservation)
		}
		return apierr.Internal(err)
	}
	return nil
}

// allowedTransition is the state machine, written out rather than implied by
// a series of if-statements spread through the handlers. A booking that has
// finished, been cancelled or been marked a no-show is terminal: there is no
// path back, because each of those has already affected a report.
func allowedTransition(from, to models.ReservationStatus) bool {
	switch from {
	case models.ReservationBooked:
		return to == models.ReservationInProgress ||
			to == models.ReservationCompleted ||
			to == models.ReservationCancelled ||
			to == models.ReservationNoShow
	case models.ReservationInProgress:
		return to == models.ReservationCompleted || to == models.ReservationCancelled
	default:
		return false
	}
}
