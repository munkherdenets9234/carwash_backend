package service

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/pkg/apierr"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ScheduleService owns the roster and the availability derived from it.
type ScheduleService struct {
	shifts    *repository.ShiftRepo
	users     *repository.UserRepo
	bookings  *repository.ReservationRepo
	locations *repository.LocationRepo
	services  *repository.WashServiceRepo

	loc      *time.Location
	slotStep time.Duration

	// now is injectable so availability can be tested against a fixed
	// clock. Production wiring passes time.Now.
	now func() time.Time
}

func NewScheduleService(
	shifts *repository.ShiftRepo,
	users *repository.UserRepo,
	bookings *repository.ReservationRepo,
	locations *repository.LocationRepo,
	services *repository.WashServiceRepo,
	loc *time.Location,
	slotStepMin int,
) *ScheduleService {
	return &ScheduleService{
		shifts:    shifts,
		users:     users,
		bookings:  bookings,
		locations: locations,
		services:  services,
		loc:       loc,
		slotStep:  time.Duration(slotStepMin) * time.Minute,
		now:       func() time.Time { return time.Now().UTC() },
	}
}

// maxShiftHours refuses a roster entry that is almost certainly a typo —
// a wrong date on the end time produces a "shift" of several days, which
// would then offer bookable slots around the clock.
const maxShiftHours = 16

// AddShift rosters an employee at a location for a window.
func (s *ScheduleService) AddShift(ctx context.Context, employeeID, locationID string, start, end time.Time) (*models.Shift, error) {
	empID, err := primitive.ObjectIDFromHex(employeeID)
	if err != nil {
		return nil, apierr.BadRequest("employee_id is not a valid id")
	}
	locID, err := primitive.ObjectIDFromHex(locationID)
	if err != nil {
		return nil, apierr.BadRequest("location_id is not a valid id")
	}

	if !start.Before(end) {
		return nil, apierr.ValidationFailed("start_at must be before end_at")
	}
	if end.Sub(start) > maxShiftHours*time.Hour {
		return nil, apierr.ValidationFailed("a shift cannot be longer than 16 hours")
	}

	emp, err := s.users.FindByIDAndRole(ctx, empID, models.RoleEmployee)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apierr.NotFound("employee")
		}
		return nil, apierr.Internal(err)
	}
	if emp.Status != models.UserActive {
		return nil, apierr.ValidationFailed("that employee is suspended")
	}
	if _, err := s.locations.FindByID(ctx, locID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apierr.NotFound("location").In(apierr.DomainCatalog)
		}
		return nil, apierr.Internal(err)
	}

	// One person cannot be in two places at once, and a roster that says
	// otherwise produces availability at both.
	existing, err := s.shifts.List(ctx, repository.ShiftQuery{EmployeeID: &empID, From: start, To: end})
	if err != nil {
		return nil, apierr.Internal(err)
	}
	if len(existing) > 0 {
		return nil, apierr.Conflict("that employee already has a shift overlapping this window").
			In(apierr.DomainSchedule)
	}

	sh := &models.Shift{EmployeeID: empID, LocationID: locID, StartAt: start.UTC(), EndAt: end.UTC()}
	if err := s.shifts.Create(ctx, sh); err != nil {
		return nil, apierr.Internal(err)
	}
	return sh, nil
}

// ListShifts returns the roster for a window, optionally narrowed.
func (s *ScheduleService) ListShifts(ctx context.Context, q repository.ShiftQuery) ([]*models.Shift, error) {
	out, err := s.shifts.List(ctx, q)
	if err != nil {
		return nil, apierr.Internal(err)
	}
	return out, nil
}

func (s *ScheduleService) DeleteShift(ctx context.Context, id primitive.ObjectID) error {
	if err := s.shifts.Delete(ctx, id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return apierr.NotFound("shift").In(apierr.DomainSchedule)
		}
		return apierr.Internal(err)
	}
	return nil
}

// ── Availability ──────────────────────────────────────────────────────────

// Slot is one bookable window at one site.
type Slot struct {
	Start      time.Time
	End        time.Time
	LocationID primitive.ObjectID
}

// EmployeeAvailability is one employee and when they can take this service.
type EmployeeAvailability struct {
	Employee *models.User
	Slots    []Slot
}

// AvailabilityQuery asks "who can wash my car, and when".
type AvailabilityQuery struct {
	Day        time.Time
	ServiceID  primitive.ObjectID
	LocationID *primitive.ObjectID
	EmployeeID *primitive.ObjectID
}

// Availability answers the booking page's question in one pass: three reads
// regardless of how many employees are rostered, rather than one per person.
//
// Employees with no free time are returned with an empty slot list rather
// than dropped. "Bat is working today and fully booked" and "Bat is not in
// today" are different answers, and a customer choosing a specific person
// needs to be able to tell them apart.
func (s *ScheduleService) Availability(ctx context.Context, q AvailabilityQuery) ([]EmployeeAvailability, error) {
	ws, err := s.services.FindByID(ctx, q.ServiceID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apierr.NotFound("service").In(apierr.DomainCatalog)
		}
		return nil, apierr.Internal(err)
	}
	if !ws.Active {
		return nil, apierr.ValidationFailed("that service is not currently offered")
	}

	dayStart, dayEnd := DayRange(q.Day, s.loc)

	shifts, err := s.shifts.List(ctx, repository.ShiftQuery{
		EmployeeID: q.EmployeeID,
		LocationID: q.LocationID,
		From:       dayStart,
		To:         dayEnd,
	})
	if err != nil {
		return nil, apierr.Internal(err)
	}
	if len(shifts) == 0 {
		return []EmployeeAvailability{}, nil
	}

	byEmployee := map[primitive.ObjectID][]*models.Shift{}
	ids := make([]primitive.ObjectID, 0, len(shifts))
	for _, sh := range shifts {
		if _, seen := byEmployee[sh.EmployeeID]; !seen {
			ids = append(ids, sh.EmployeeID)
		}
		byEmployee[sh.EmployeeID] = append(byEmployee[sh.EmployeeID], sh)
	}

	employees, err := s.users.FindManyByIDs(ctx, ids)
	if err != nil {
		return nil, apierr.Internal(err)
	}

	booked, err := s.bookings.ListBlockingForEmployees(ctx, ids, dayStart, dayEnd)
	if err != nil {
		return nil, apierr.Internal(err)
	}
	busyBy := map[primitive.ObjectID][]Interval{}
	for _, b := range booked {
		busyBy[b.EmployeeID] = append(busyBy[b.EmployeeID], Interval{Start: b.StartAt, End: b.EndAt})
	}

	duration := time.Duration(ws.DurationMin) * time.Minute
	notBefore := s.now()

	out := make([]EmployeeAvailability, 0, len(ids))
	for _, id := range ids {
		emp, ok := employees[id]
		// A shift referencing a user who has been deleted, or who is no
		// longer an employee, is stale roster data. Skipped rather than
		// surfaced: offering a booking with them would fail at Book.
		if !ok || emp.Role != models.RoleEmployee || emp.Status != models.UserActive {
			continue
		}

		ea := EmployeeAvailability{Employee: emp, Slots: []Slot{}}
		for _, sh := range byEmployee[id] {
			// Clamp to the requested day so "today" never offers tomorrow
			// morning off the back of an overnight shift.
			window := Interval{Start: maxTime(sh.StartAt, dayStart), End: minTime(sh.EndAt, dayEnd)}
			for _, start := range FreeSlots(window, busyBy[id], duration, s.slotStep, notBefore) {
				ea.Slots = append(ea.Slots, Slot{
					Start:      start,
					End:        start.Add(duration),
					LocationID: sh.LocationID,
				})
			}
		}
		sort.Slice(ea.Slots, func(i, j int) bool { return ea.Slots[i].Start.Before(ea.Slots[j].Start) })
		out = append(out, ea)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Employee.Name < out[j].Employee.Name })
	return out, nil
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
