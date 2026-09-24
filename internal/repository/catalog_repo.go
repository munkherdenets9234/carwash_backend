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

// LocationRepo and WashServiceRepo share a file because they are the same
// shape — small, manager-owned reference data that the booking flow reads and
// almost never writes.

type LocationRepo struct {
	col *mongo.Collection
}

func NewLocationRepo(db *mongo.Database) *LocationRepo {
	return &LocationRepo{col: db.Collection("locations")}
}

func (r *LocationRepo) Create(ctx context.Context, l *models.Location) error {
	now := time.Now().UTC()
	l.ID = primitive.NewObjectID()
	l.CreatedAt = now
	l.UpdatedAt = now
	_, err := r.col.InsertOne(ctx, l)
	return translate(err)
}

// List returns locations, optionally only the active ones. Customers see
// active only; a manager sees all, because a site that was switched off is
// exactly what they need to find in order to switch it back on.
func (r *LocationRepo) List(ctx context.Context, tenantID primitive.ObjectID, activeOnly bool) ([]*models.Location, error) {
	filter := bson.M{}
	if activeOnly {
		filter["active"] = true
	}
	cur, err := r.col.Find(ctx, scoped(tenantID, filter), options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
	if err != nil {
		return nil, translate(err)
	}
	defer cur.Close(ctx)

	var out []*models.Location
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *LocationRepo) FindByID(ctx context.Context, tenantID, id primitive.ObjectID) (*models.Location, error) {
	var l models.Location
	if err := r.col.FindOne(ctx, scopedID(tenantID, id)).Decode(&l); err != nil {
		return nil, translate(err)
	}
	return &l, nil
}

func (r *LocationRepo) Update(ctx context.Context, tenantID, id primitive.ObjectID, set bson.M) error {
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

// FindManyByIDs returns the named locations keyed by id, so a list response
// can label every row from one query instead of one per row.
func (r *LocationRepo) FindManyByIDs(ctx context.Context, tenantID primitive.ObjectID, ids []primitive.ObjectID) (map[primitive.ObjectID]*models.Location, error) {
	out := make(map[primitive.ObjectID]*models.Location, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	cur, err := r.col.Find(ctx, scoped(tenantID, bson.M{"_id": bson.M{"$in": ids}}))
	if err != nil {
		return nil, translate(err)
	}
	defer cur.Close(ctx)

	var rows []*models.Location
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	for _, l := range rows {
		out[l.ID] = l
	}
	return out, nil
}

type WashServiceRepo struct {
	col *mongo.Collection
}

func NewWashServiceRepo(db *mongo.Database) *WashServiceRepo {
	return &WashServiceRepo{col: db.Collection("wash_services")}
}

func (r *WashServiceRepo) Create(ctx context.Context, s *models.WashService) error {
	now := time.Now().UTC()
	s.ID = primitive.NewObjectID()
	s.CreatedAt = now
	s.UpdatedAt = now
	_, err := r.col.InsertOne(ctx, s)
	return translate(err)
}

func (r *WashServiceRepo) List(ctx context.Context, tenantID primitive.ObjectID, activeOnly bool) ([]*models.WashService, error) {
	filter := bson.M{}
	if activeOnly {
		filter["active"] = true
	}
	cur, err := r.col.Find(ctx, scoped(tenantID, filter), options.Find().SetSort(bson.D{{Key: "price_mnt", Value: 1}}))
	if err != nil {
		return nil, translate(err)
	}
	defer cur.Close(ctx)

	var out []*models.WashService
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *WashServiceRepo) FindByID(ctx context.Context, tenantID, id primitive.ObjectID) (*models.WashService, error) {
	var s models.WashService
	if err := r.col.FindOne(ctx, scopedID(tenantID, id)).Decode(&s); err != nil {
		return nil, translate(err)
	}
	return &s, nil
}

// FindManyByIDs returns the named services keyed by id.
func (r *WashServiceRepo) FindManyByIDs(ctx context.Context, tenantID primitive.ObjectID, ids []primitive.ObjectID) (map[primitive.ObjectID]*models.WashService, error) {
	out := make(map[primitive.ObjectID]*models.WashService, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	cur, err := r.col.Find(ctx, scoped(tenantID, bson.M{"_id": bson.M{"$in": ids}}))
	if err != nil {
		return nil, translate(err)
	}
	defer cur.Close(ctx)

	var rows []*models.WashService
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	for _, s := range rows {
		out[s.ID] = s
	}
	return out, nil
}

func (r *WashServiceRepo) Update(ctx context.Context, tenantID, id primitive.ObjectID, set bson.M) error {
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
