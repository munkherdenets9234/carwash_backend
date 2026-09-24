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

type MediaRepo struct {
	col *mongo.Collection
}

func NewMediaRepo(db *mongo.Database) *MediaRepo {
	return &MediaRepo{col: db.Collection("media")}
}

// Create stores an uploaded photograph. m.TenantID must already be set.
func (r *MediaRepo) Create(ctx context.Context, m *models.Media) error {
	now := time.Now().UTC()
	m.ID = primitive.NewObjectID()
	m.CreatedAt = now
	m.UpdatedAt = now
	_, err := r.col.InsertOne(ctx, m)
	return translate(err)
}

// MediaQuery narrows a media read. A zero value returns every image the
// tenant has, in display order.
type MediaQuery struct {
	Role *models.MediaRole
	// ActiveOnly is what the public site asks for; the manager screen wants
	// everything, because the likeliest reason to open it is to bring a
	// withdrawn photograph back.
	ActiveOnly bool
}

// List returns a tenant's images, ordered the way they are displayed.
func (r *MediaRepo) List(ctx context.Context, tenantID primitive.ObjectID, q MediaQuery) ([]*models.Media, error) {
	filter := bson.M{}
	if q.Role != nil {
		filter["role"] = *q.Role
	}
	if q.ActiveOnly {
		filter["active"] = true
	}

	cur, err := r.col.Find(ctx, scoped(tenantID, filter),
		options.Find().SetSort(bson.D{{Key: "sort_order", Value: 1}, {Key: "created_at", Value: 1}}))
	if err != nil {
		return nil, translate(err)
	}
	defer cur.Close(ctx)

	out := []*models.Media{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *MediaRepo) FindByID(ctx context.Context, tenantID, id primitive.ObjectID) (*models.Media, error) {
	var m models.Media
	if err := r.col.FindOne(ctx, scopedID(tenantID, id)).Decode(&m); err != nil {
		return nil, translate(err)
	}
	return &m, nil
}

func (r *MediaRepo) Update(ctx context.Context, tenantID, id primitive.ObjectID, set bson.M) error {
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

// DemoteRole moves every image currently holding a singular role into the
// gallery, except the one being promoted.
//
// This is how "set this as the home cover" works. The alternative — refusing
// a second hero — turns a one-click change into: find the old cover, demote
// it, remember what you were doing, promote the new one. Replacing is what
// the person meant, so replacing is what it does.
func (r *MediaRepo) DemoteRole(ctx context.Context, tenantID primitive.ObjectID, role models.MediaRole, except primitive.ObjectID) error {
	_, err := r.col.UpdateMany(ctx,
		scoped(tenantID, bson.M{"role": role, "_id": bson.M{"$ne": except}}),
		bson.M{"$set": bson.M{"role": models.MediaRoleGallery, "updated_at": time.Now().UTC()}})
	return translate(err)
}

func (r *MediaRepo) Delete(ctx context.Context, tenantID, id primitive.ObjectID) error {
	res, err := r.col.DeleteOne(ctx, scopedID(tenantID, id))
	if err != nil {
		return translate(err)
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}

// CountForRole backs the plan limit on how many photographs a tenant may
// hold. Counted per role, so a cap on the gallery does not also count the
// two single-slot images.
func (r *MediaRepo) CountForRole(ctx context.Context, tenantID primitive.ObjectID, role models.MediaRole) (int64, error) {
	return r.col.CountDocuments(ctx, scoped(tenantID, bson.M{"role": role}))
}
