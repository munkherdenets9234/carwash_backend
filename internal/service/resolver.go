package service

import (
	"context"

	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/pkg/apierr"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Resolved holds the rows a list response refers to, keyed by id.
type Resolved struct {
	Users     map[primitive.ObjectID]*models.User
	Cars      map[primitive.ObjectID]*models.Car
	Services  map[primitive.ObjectID]*models.WashService
	Locations map[primitive.ObjectID]*models.Location
}

// Resolver turns the ids on a list of rows into the rows they point at, in
// one query per collection.
//
// It exists to keep N+1 out of the controllers. A list of forty bookings
// naming a service, a location, a car, a customer and an employee is five
// queries through here and two hundred without — and the version without is
// what gets written when each controller solves it alone.
type Resolver struct {
	users     *repository.UserRepo
	cars      *repository.CarRepo
	services  *repository.WashServiceRepo
	locations *repository.LocationRepo
}

func NewResolver(
	users *repository.UserRepo,
	cars *repository.CarRepo,
	services *repository.WashServiceRepo,
	locations *repository.LocationRepo,
) *Resolver {
	return &Resolver{users: users, cars: cars, services: services, locations: locations}
}

// ForReservations resolves everything a booking list refers to.
func (r *Resolver) ForReservations(ctx context.Context, tenantID primitive.ObjectID, rows []*models.Reservation) (Resolved, error) {
	var users, cars, services, locations idSet
	for _, row := range rows {
		users.add(row.EmployeeID)
		users.add(row.CustomerID)
		cars.add(row.CarID)
		services.add(row.ServiceID)
		locations.add(row.LocationID)
	}
	return r.load(ctx, tenantID, users, cars, services, locations)
}

// ForShifts resolves the employees and locations on a roster.
func (r *Resolver) ForShifts(ctx context.Context, tenantID primitive.ObjectID, rows []*models.Shift) (Resolved, error) {
	var users, locations idSet
	for _, row := range rows {
		users.add(row.EmployeeID)
		locations.add(row.LocationID)
	}
	return r.load(ctx, tenantID, users, idSet{}, idSet{}, locations)
}

// ForTimeEntries resolves the employees and locations on a timesheet.
func (r *Resolver) ForTimeEntries(ctx context.Context, tenantID primitive.ObjectID, rows []*models.TimeEntry) (Resolved, error) {
	var users, locations idSet
	for _, row := range rows {
		users.add(row.EmployeeID)
		locations.add(row.LocationID)
	}
	return r.load(ctx, tenantID, users, idSet{}, idSet{}, locations)
}

func (r *Resolver) load(ctx context.Context, tenantID primitive.ObjectID, users, cars, services, locations idSet) (Resolved, error) {
	out := Resolved{}
	var err error

	if out.Users, err = r.users.FindManyByIDs(ctx, tenantID, users.list()); err != nil {
		return out, apierr.Internal(err)
	}
	if out.Cars, err = r.cars.FindManyByIDs(ctx, tenantID, cars.list()); err != nil {
		return out, apierr.Internal(err)
	}
	if out.Services, err = r.services.FindManyByIDs(ctx, tenantID, services.list()); err != nil {
		return out, apierr.Internal(err)
	}
	if out.Locations, err = r.locations.FindManyByIDs(ctx, tenantID, locations.list()); err != nil {
		return out, apierr.Internal(err)
	}
	return out, nil
}

// idSet collects distinct ids in first-seen order. A plain slice would send
// the same id to the database once per row that mentions it.
type idSet struct {
	seen map[primitive.ObjectID]struct{}
	ids  []primitive.ObjectID
}

func (s *idSet) add(id primitive.ObjectID) {
	if id.IsZero() {
		return
	}
	if s.seen == nil {
		s.seen = map[primitive.ObjectID]struct{}{}
	}
	if _, dup := s.seen[id]; dup {
		return
	}
	s.seen[id] = struct{}{}
	s.ids = append(s.ids, id)
}

func (s idSet) list() []primitive.ObjectID { return s.ids }
