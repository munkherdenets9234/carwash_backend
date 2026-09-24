package repository

import (
	"context"
	"strings"
	"time"

	"github.com/eandstravel/carwash/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type CarRepo struct {
	col *mongo.Collection
}

func NewCarRepo(db *mongo.Database) *CarRepo {
	return &CarRepo{col: db.Collection("cars")}
}

// NormalizePlate matches the stored form the unique index is built on.
// Exported for the same reason as NormalizeEmail: every writer must agree.
func NormalizePlate(plate string) string {
	return strings.ToUpper(strings.Join(strings.Fields(plate), ""))
}

// Create stores a vehicle. c.TenantID must already be set by the service —
// it comes from the resolved tenant, never from the request body.
func (r *CarRepo) Create(ctx context.Context, c *models.Car) error {
	now := time.Now().UTC()
	c.ID = primitive.NewObjectID()
	c.Plate = NormalizePlate(c.Plate)
	c.CreatedAt = now
	c.UpdatedAt = now
	_, err := r.col.InsertOne(ctx, c)
	return translate(err)
}

// FindByPlateForOwner resolves one owner's car by its plate.
//
// Guest booking needs this: a returning customer types the same plate rather
// than picking from a garage they cannot see without an account, and without
// this lookup every visit would register the car again — which the unique
// index refuses, turning a repeat customer's second booking into an error.
//
// Scoped to the owner, like FindByIDForOwner, so it cannot be used to ask
// which customer a plate belongs to.
func (r *CarRepo) FindByPlateForOwner(ctx context.Context, tenantID, ownerID primitive.ObjectID, plate string) (*models.Car, error) {
	plate = NormalizePlate(plate)
	if plate == "" {
		return nil, ErrNotFound
	}
	var c models.Car
	err := r.col.FindOne(ctx, scoped(tenantID, bson.M{
		"owner_id": ownerID,
		"plate":    plate,
	})).Decode(&c)
	if err != nil {
		return nil, translate(err)
	}
	return &c, nil
}

func (r *CarRepo) ListByOwner(ctx context.Context, tenantID, ownerID primitive.ObjectID) ([]*models.Car, error) {
	cur, err := r.col.Find(ctx,
		scoped(tenantID, bson.M{"owner_id": ownerID}),
		options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}))
	if err != nil {
		return nil, translate(err)
	}
	defer cur.Close(ctx)

	var out []*models.Car
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// FindByIDForOwner scopes the lookup to the tenant AND the owner in the query
// itself.
//
// The alternative — fetch by id, then compare owner_id in the service — works
// until one call site forgets the comparison, and that call site is an IDOR:
// any customer reading any other customer's vehicle by guessing an id. Making
// ownership part of the query means the unsafe version is not available.
//
// The tenant is there for the same reason one layer out. Without it the
// worst case is not a customer reading a neighbour's car, it is one business
// reading another business's entire fleet.
func (r *CarRepo) FindByIDForOwner(ctx context.Context, tenantID, id, ownerID primitive.ObjectID) (*models.Car, error) {
	var c models.Car
	if err := r.col.FindOne(ctx, scoped(tenantID, bson.M{"_id": id, "owner_id": ownerID})).Decode(&c); err != nil {
		return nil, translate(err)
	}
	return &c, nil
}

// FindByID ignores ownership and is for staff paths only — the job card an
// employee sees names the car they are about to wash. It is still scoped by
// tenant: "staff may see any car" means any car belonging to the business
// they work for.
func (r *CarRepo) FindByID(ctx context.Context, tenantID, id primitive.ObjectID) (*models.Car, error) {
	var c models.Car
	if err := r.col.FindOne(ctx, scopedID(tenantID, id)).Decode(&c); err != nil {
		return nil, translate(err)
	}
	return &c, nil
}

func (r *CarRepo) FindManyByIDs(ctx context.Context, tenantID primitive.ObjectID, ids []primitive.ObjectID) (map[primitive.ObjectID]*models.Car, error) {
	out := make(map[primitive.ObjectID]*models.Car, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	cur, err := r.col.Find(ctx, scoped(tenantID, bson.M{"_id": bson.M{"$in": ids}}))
	if err != nil {
		return nil, translate(err)
	}
	defer cur.Close(ctx)

	var cars []*models.Car
	if err := cur.All(ctx, &cars); err != nil {
		return nil, err
	}
	for _, c := range cars {
		out[c.ID] = c
	}
	return out, nil
}

func (r *CarRepo) Update(ctx context.Context, tenantID, id, ownerID primitive.ObjectID, set bson.M) error {
	if plate, ok := set["plate"].(string); ok {
		set["plate"] = NormalizePlate(plate)
	}
	set["updated_at"] = time.Now().UTC()
	res, err := r.col.UpdateOne(ctx,
		scoped(tenantID, bson.M{"_id": id, "owner_id": ownerID}),
		bson.M{"$set": set})
	if err != nil {
		return translate(err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *CarRepo) Delete(ctx context.Context, tenantID, id, ownerID primitive.ObjectID) error {
	res, err := r.col.DeleteOne(ctx, scoped(tenantID, bson.M{"_id": id, "owner_id": ownerID}))
	if err != nil {
		return translate(err)
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}
