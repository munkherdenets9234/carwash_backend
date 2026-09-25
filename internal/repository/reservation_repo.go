package repository

import (
	"context"
	"time"

	"github.com/eandstravel/carwash/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type ReservationRepo struct {
	col *mongo.Collection
}

func NewReservationRepo(db *mongo.Database) *ReservationRepo {
	return &ReservationRepo{col: db.Collection("reservations")}
}

// blockingStatuses are the states that still hold a slot. Kept as a package
// variable so the overlap query and the availability read cannot disagree
// about what "busy" means.
var blockingStatuses = bson.A{
	models.ReservationBooked,
	models.ReservationInProgress,
	models.ReservationCompleted,
}

// FindByReference resolves one booking from the code a guest was given.
//
// The phone number is part of the FILTER rather than checked after the read.
// That is the difference between a lookup and an authorisation check
// somebody can forget to write: a mismatched number cannot return a row at
// all, so there is no path where the booking is loaded and then compared.
//
// Both halves are required because neither is sufficient. The code alone is
// a secret that may be forwarded, screenshotted or left in a chat; the phone
// number alone is not a secret at all.
func (r *ReservationRepo) FindByReference(ctx context.Context, tenantID primitive.ObjectID, reference string, customerID primitive.ObjectID) (*models.Reservation, error) {
	key := models.ReferenceKey(tenantID, reference)
	if key == "" {
		return nil, ErrNotFound
	}
	var res models.Reservation
	// Queried by the indexed key, so the lookup a stranger can drive is an
	// index hit rather than a scan — which matters on the one public route
	// where a wrong answer is worth retrying.
	err := r.col.FindOne(ctx, scoped(tenantID, bson.M{
		"reference_key": key,
		"customer_id":   customerID,
	})).Decode(&res)
	if err != nil {
		return nil, translate(err)
	}
	return &res, nil
}

func (r *ReservationRepo) Create(ctx context.Context, res *models.Reservation) error {
	now := time.Now().UTC()
	res.ID = primitive.NewObjectID()
	res.CreatedAt = now
	res.UpdatedAt = now
	_, err := r.col.InsertOne(ctx, res)
	return translate(err)
}

// ListBlockingForEmployees returns the reservations that occupy time for the
// given employees inside [from, to). This is what availability subtracts from
// the roster, and what the booking overlap check reads.
func (r *ReservationRepo) ListBlockingForEmployees(ctx context.Context, tenantID primitive.ObjectID, employeeIDs []primitive.ObjectID, from, to time.Time) ([]*models.Reservation, error) {
	if len(employeeIDs) == 0 {
		return nil, nil
	}
	filter := bson.M{
		"employee_id": bson.M{"$in": employeeIDs},
		"status":      bson.M{"$in": blockingStatuses},
		// Overlap, not containment — a 90-minute wash that started before
		// the window still occupies the start of it.
		"start_at": bson.M{"$lt": to},
		"end_at":   bson.M{"$gt": from},
	}
	cur, err := r.col.Find(ctx, scoped(tenantID, filter), options.Find().SetSort(bson.D{{Key: "start_at", Value: 1}}))
	if err != nil {
		return nil, translate(err)
	}
	defer cur.Close(ctx)

	var out []*models.Reservation
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ReservationQuery narrows a list read.
type ReservationQuery struct {
	CustomerID *primitive.ObjectID
	EmployeeID *primitive.ObjectID
	LocationID *primitive.ObjectID
	Status     *models.ReservationStatus
	From, To   time.Time
}

// List returns reservations overlapping [From, To), newest first.
func (r *ReservationRepo) List(ctx context.Context, tenantID primitive.ObjectID, q ReservationQuery) ([]*models.Reservation, error) {
	filter := bson.M{
		"start_at": bson.M{"$lt": q.To},
		"end_at":   bson.M{"$gt": q.From},
	}
	if q.CustomerID != nil {
		filter["customer_id"] = *q.CustomerID
	}
	if q.EmployeeID != nil {
		filter["employee_id"] = *q.EmployeeID
	}
	if q.LocationID != nil {
		filter["location_id"] = *q.LocationID
	}
	if q.Status != nil {
		filter["status"] = *q.Status
	}

	cur, err := r.col.Find(ctx, scoped(tenantID, filter), options.Find().SetSort(bson.D{{Key: "start_at", Value: 1}}))
	if err != nil {
		return nil, translate(err)
	}
	defer cur.Close(ctx)

	var out []*models.Reservation
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *ReservationRepo) FindByID(ctx context.Context, tenantID, id primitive.ObjectID) (*models.Reservation, error) {
	var res models.Reservation
	if err := r.col.FindOne(ctx, scopedID(tenantID, id)).Decode(&res); err != nil {
		return nil, translate(err)
	}
	return &res, nil
}

// FindByIDForCustomer scopes to the owner in the query, for the same reason
// CarRepo.FindByIDForOwner does.
func (r *ReservationRepo) FindByIDForCustomer(ctx context.Context, tenantID, id, customerID primitive.ObjectID) (*models.Reservation, error) {
	var res models.Reservation
	if err := r.col.FindOne(ctx, scoped(tenantID, bson.M{"_id": id, "customer_id": customerID})).Decode(&res); err != nil {
		return nil, translate(err)
	}
	return &res, nil
}

// UpdateFields applies set to one reservation, refusing a miss rather than
// reporting success for a row that does not exist.
func (r *ReservationRepo) UpdateFields(ctx context.Context, tenantID, id primitive.ObjectID, set bson.M) error {
	set["updated_at"] = time.Now().UTC()
	res, err := r.col.UpdateOne(ctx, scopedID(tenantID, id), bson.M{"$set": set})
	if err != nil {
		return translate(err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateAndReleaseSlot applies set and drops slot_key in one write.
//
// Separate from UpdateFields because the two must not come apart: a booking
// that is cancelled but keeps its key permanently blocks that start time for
// that employee, and one that drops its key while still active loses the
// guard against a double booking. Doing both in a single update makes the
// pairing structural.
func (r *ReservationRepo) UpdateAndReleaseSlot(ctx context.Context, tenantID, id primitive.ObjectID, set bson.M) error {
	set["updated_at"] = time.Now().UTC()
	res, err := r.col.UpdateOne(ctx,
		scopedID(tenantID, id),
		bson.M{"$set": set, "$unset": bson.M{"slot_key": ""}})
	if err != nil {
		return translate(err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

// CompletedBetween returns every completed wash whose completion falls in
// [from, to). This is the daily report's source.
//
// Note it filters on completed_at, not start_at: a wash booked for 17:45 and
// finished at 18:20 belongs to the day it was paid for, and a job that ran
// past midnight belongs to the day it finished. Reporting by booking time
// would put revenue on a day no money was taken.
func (r *ReservationRepo) CompletedBetween(ctx context.Context, tenantID primitive.ObjectID, from, to time.Time) ([]*models.Reservation, error) {
	filter := bson.M{
		"status":       models.ReservationCompleted,
		"completed_at": bson.M{"$gte": from, "$lt": to},
	}
	cur, err := r.col.Find(ctx, scoped(tenantID, filter), options.Find().SetSort(bson.D{{Key: "completed_at", Value: 1}}))
	if err != nil {
		return nil, translate(err)
	}
	defer cur.Close(ctx)

	var out []*models.Reservation
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}
