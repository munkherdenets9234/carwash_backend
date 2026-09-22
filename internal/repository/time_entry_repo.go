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

type TimeEntryRepo struct {
	col *mongo.Collection
}

func NewTimeEntryRepo(db *mongo.Database) *TimeEntryRepo {
	return &TimeEntryRepo{col: db.Collection("time_entries")}
}

// Create inserts an open entry. A second open entry for the same employee is
// rejected by the partial unique index (see EnsureIndexes) and surfaces here
// as ErrDuplicate.
func (r *TimeEntryRepo) Create(ctx context.Context, e *models.TimeEntry) error {
	now := time.Now().UTC()
	e.ID = primitive.NewObjectID()
	// Set here rather than by the caller: the invariant belongs to the
	// collection, so the one place that inserts into it is the one place
	// that must not forget.
	e.OpenKey = e.EmployeeID.Hex()
	e.CreatedAt = now
	e.UpdatedAt = now
	_, err := r.col.InsertOne(ctx, e)
	return translate(err)
}

// FindOpen returns the employee's running entry, or ErrNotFound when they are
// clocked out.
//
// Queried by open_key, which the unique index covers, so this is a single
// index hit and can match at most one document by construction.
func (r *TimeEntryRepo) FindOpen(ctx context.Context, employeeID primitive.ObjectID) (*models.TimeEntry, error) {
	var e models.TimeEntry
	err := r.col.FindOne(ctx, bson.M{"open_key": employeeID.Hex()}).Decode(&e)
	if err != nil {
		return nil, translate(err)
	}
	return &e, nil
}

// Close writes the clock-out half of an entry.
//
// The filter repeats the "still open" condition rather than trusting the read
// that found it. Two clock-out requests arriving together would otherwise
// both pass the service's check and the second would overwrite the first,
// changing a finished shift's length after the fact. With the condition in
// the filter the second update matches nothing and is reported as such.
func (r *TimeEntryRepo) Close(ctx context.Context, id primitive.ObjectID, at time.Time, point models.GeoPoint, distanceM float64, workedMinutes int) error {
	res, err := r.col.UpdateOne(ctx,
		// open_key must still be present, and is removed in the same write.
		// Pairing them makes the entry's open state one fact rather than
		// two that can disagree.
		bson.M{"_id": id, "open_key": bson.M{"$exists": true}},
		bson.M{"$unset": bson.M{"open_key": ""}, "$set": bson.M{
			"clock_out_at":         at,
			"clock_out_point":      point,
			"clock_out_distance_m": distanceM,
			"worked_minutes":       workedMinutes,
			"updated_at":           time.Now().UTC(),
		}})
	if err != nil {
		return translate(err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

// TimeEntryQuery narrows a timesheet read.
type TimeEntryQuery struct {
	EmployeeID *primitive.ObjectID
	LocationID *primitive.ObjectID
	From, To   time.Time
}

// List returns entries whose clock-in falls in [From, To), oldest first.
//
// Filtered on clock_in_at rather than on overlap: a timesheet is a list of
// shifts as they were started, and an entry left open across midnight should
// appear once, on the day work began, not on both days.
func (r *TimeEntryRepo) List(ctx context.Context, q TimeEntryQuery) ([]*models.TimeEntry, error) {
	filter := bson.M{"clock_in_at": bson.M{"$gte": q.From, "$lt": q.To}}
	if q.EmployeeID != nil {
		filter["employee_id"] = *q.EmployeeID
	}
	if q.LocationID != nil {
		filter["location_id"] = *q.LocationID
	}

	cur, err := r.col.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "clock_in_at", Value: 1}}))
	if err != nil {
		return nil, translate(err)
	}
	defer cur.Close(ctx)

	var out []*models.TimeEntry
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}
