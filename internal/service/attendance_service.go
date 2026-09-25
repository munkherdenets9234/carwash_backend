package service

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/pkg/apierr"
	"github.com/eandstravel/carwash/pkg/geo"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// AttendanceService is the clock: who is on site, since when, and the
// timesheet that falls out of it.
type AttendanceService struct {
	entries   *repository.TimeEntryRepo
	locations *repository.LocationRepo
	users     *repository.UserRepo

	now func() time.Time
}

func NewAttendanceService(
	entries *repository.TimeEntryRepo,
	locations *repository.LocationRepo,
	users *repository.UserRepo,
) *AttendanceService {
	return &AttendanceService{
		entries:   entries,
		locations: locations,
		users:     users,
		now:       func() time.Time { return time.Now().UTC() },
	}
}

// ClockIn opens a time entry, if the employee is where they say they are.
func (s *AttendanceService) ClockIn(ctx context.Context, tenantID primitive.ObjectID, employeeID primitive.ObjectID, locationID string, lat, lng float64) (*models.TimeEntry, error) {
	locID, err := primitive.ObjectIDFromHex(locationID)
	if err != nil {
		return nil, apierr.BadRequest("location_id is not a valid id")
	}
	if !geo.ValidCoordinate(lat, lng) {
		// Separated from the distance failure below on purpose: "your phone
		// has no fix yet" and "you are at the wrong branch" need different
		// things from the person reading the message.
		return nil, apierr.ValidationFailed("no usable location from your device — wait for a GPS fix and try again").
			In(apierr.DomainAttendance)
	}

	loc, err := s.locations.FindByID(ctx, tenantID, locID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apierr.NotFound("location").In(apierr.DomainCatalog)
		}
		return nil, apierr.Internal(err)
	}
	if !loc.Active {
		return nil, apierr.ValidationFailed("that location is closed").In(apierr.DomainAttendance)
	}

	distance := geo.DistanceM(lat, lng, loc.Point.Lat, loc.Point.Lng)
	if distance > loc.GeofenceRadiusM {
		return nil, apierr.OutsideGeofence(distance, loc.GeofenceRadiusM)
	}

	entry := &models.TimeEntry{
		TenantID:         tenantID,
		EmployeeID:       employeeID,
		LocationID:       locID,
		ClockInAt:        s.now(),
		ClockInPoint:     models.GeoPoint{Lat: lat, Lng: lng},
		ClockInDistanceM: math.Round(distance),
	}
	if err := s.entries.Create(ctx, entry); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			// The partial unique index refused a second open entry. This is
			// the authoritative answer, not the service's own check — two
			// taps arriving together both pass a read-then-write.
			return nil, apierr.Conflict("you are already clocked in").In(apierr.DomainAttendance)
		}
		return nil, apierr.Internal(err)
	}
	return entry, nil
}

// ClockOut closes the running entry.
//
// Deliberately NOT geofenced. The distance is measured and stored, but being
// far away does not block it: refusing would leave someone who has already
// gone home clocked in indefinitely, inflating their hours and requiring a
// manager to fix by hand. A clock-out from the wrong place is a question for
// a person to look at, which is what recording the distance makes possible;
// it is not a reason to jam the timesheet.
func (s *AttendanceService) ClockOut(ctx context.Context, tenantID primitive.ObjectID, employeeID primitive.ObjectID, lat, lng float64) (*models.TimeEntry, error) {
	entry, err := s.entries.FindOpen(ctx, tenantID, employeeID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apierr.Conflict("you are not clocked in").In(apierr.DomainAttendance)
		}
		return nil, apierr.Internal(err)
	}

	var point models.GeoPoint
	distance := math.NaN()
	if geo.ValidCoordinate(lat, lng) {
		point = models.GeoPoint{Lat: lat, Lng: lng}
		if loc, err := s.locations.FindByID(ctx, tenantID, entry.LocationID); err == nil {
			distance = math.Round(geo.DistanceM(lat, lng, loc.Point.Lat, loc.Point.Lng))
		}
	}
	if math.IsNaN(distance) {
		// No fix, or the location has since been removed. Stored as -1
		// rather than 0, because 0 would read as "standing exactly on the
		// site" — the most misleading value available.
		distance = -1
	}

	at := s.now()
	worked := int(math.Round(at.Sub(entry.ClockInAt).Minutes()))
	if worked < 0 {
		worked = 0
	}

	if err := s.entries.Close(ctx, tenantID, entry.ID, at, point, distance, worked); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// Someone else closed it between the read and the write.
			return nil, apierr.Conflict("you are not clocked in").In(apierr.DomainAttendance)
		}
		return nil, apierr.Internal(err)
	}

	entry.ClockOutAt = &at
	entry.ClockOutPoint = &point
	entry.ClockOutDistanceM = &distance
	entry.WorkedMinutes = worked
	return entry, nil
}

// Current returns the employee's running entry, or nil when clocked out.
// Nil is not an error: "not clocked in" is a normal state the app renders.
func (s *AttendanceService) Current(ctx context.Context, tenantID primitive.ObjectID, employeeID primitive.ObjectID) (*models.TimeEntry, error) {
	entry, err := s.entries.FindOpen(ctx, tenantID, employeeID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, nil
		}
		return nil, apierr.Internal(err)
	}
	return entry, nil
}

// Timesheet returns the entries in a window, narrowed as the query says.
func (s *AttendanceService) Timesheet(ctx context.Context, tenantID primitive.ObjectID, q repository.TimeEntryQuery) ([]*models.TimeEntry, error) {
	out, err := s.entries.List(ctx, tenantID, q)
	if err != nil {
		return nil, apierr.Internal(err)
	}
	return out, nil
}

// TimesheetSummary is the total under a timesheet.
type TimesheetSummary struct {
	Entries       int
	WorkedMinutes int
	// OpenEntries is surfaced rather than folded into the total. An open
	// entry contributes zero minutes, so a timesheet with one reads low for
	// a reason the reader cannot otherwise see — and the usual cause is
	// somebody who forgot to clock out, which is exactly what a manager
	// needs pointed at.
	OpenEntries int
}

// SummarizeTimesheet totals a set of entries. Pure, so the arithmetic under
// a payroll figure is testable without a database.
func SummarizeTimesheet(entries []*models.TimeEntry) TimesheetSummary {
	sum := TimesheetSummary{Entries: len(entries)}
	for _, e := range entries {
		if e.Running() {
			sum.OpenEntries++
			continue
		}
		sum.WorkedMinutes += e.WorkedMinutes
	}
	return sum
}
