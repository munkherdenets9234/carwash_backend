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

type ShiftRepo struct {
	col *mongo.Collection
}

func NewShiftRepo(db *mongo.Database) *ShiftRepo {
	return &ShiftRepo{col: db.Collection("shifts")}
}

func (r *ShiftRepo) Create(ctx context.Context, s *models.Shift) error {
	now := time.Now().UTC()
	s.ID = primitive.NewObjectID()
	s.CreatedAt = now
	s.UpdatedAt = now
	_, err := r.col.InsertOne(ctx, s)
	return translate(err)
}

// ShiftQuery narrows a roster read. A zero value matches every shift in the
// window, which is what the manager's roster view wants.
type ShiftQuery struct {
	EmployeeID *primitive.ObjectID
	LocationID *primitive.ObjectID
	From, To   time.Time
}

// List returns shifts that OVERLAP [From, To), not shifts that start inside
// it.
//
// The difference shows up on the shift that starts at 22:00 and ends at
// 02:00. Asking "which shifts start today" drops it from tomorrow's roster
// even though tomorrow's first two hours are covered by it — and availability
// is built from this list, so the employee would appear unbookable for hours
// they are actually working.
func (r *ShiftRepo) List(ctx context.Context, tenantID primitive.ObjectID, q ShiftQuery) ([]*models.Shift, error) {
	filter := bson.M{
		"start_at": bson.M{"$lt": q.To},
		"end_at":   bson.M{"$gt": q.From},
	}
	if q.EmployeeID != nil {
		filter["employee_id"] = *q.EmployeeID
	}
	if q.LocationID != nil {
		filter["location_id"] = *q.LocationID
	}

	cur, err := r.col.Find(ctx, scoped(tenantID, filter), options.Find().SetSort(bson.D{{Key: "start_at", Value: 1}}))
	if err != nil {
		return nil, translate(err)
	}
	defer cur.Close(ctx)

	var out []*models.Shift
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *ShiftRepo) FindByID(ctx context.Context, tenantID, id primitive.ObjectID) (*models.Shift, error) {
	var s models.Shift
	if err := r.col.FindOne(ctx, scopedID(tenantID, id)).Decode(&s); err != nil {
		return nil, translate(err)
	}
	return &s, nil
}

func (r *ShiftRepo) Delete(ctx context.Context, tenantID, id primitive.ObjectID) error {
	res, err := r.col.DeleteOne(ctx, scopedID(tenantID, id))
	if err != nil {
		return translate(err)
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}
