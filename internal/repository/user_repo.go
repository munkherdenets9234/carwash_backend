package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/eandstravel/carwash/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ErrNotFound is returned by every repository for a miss, so services can
// branch on it without importing mongo and without each one inventing its own
// sentinel.
var ErrNotFound = errors.New("not found")

// ErrDuplicate is returned when a unique index rejects a write.
var ErrDuplicate = errors.New("duplicate")

// translate maps driver errors onto the two sentinels above. Anything else is
// passed through untouched: an unexpected driver error must not be flattened
// into "not found", which is how a connection failure becomes a 404.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mongo.ErrNoDocuments):
		return ErrNotFound
	case mongo.IsDuplicateKeyError(err):
		return ErrDuplicate
	default:
		return err
	}
}

type UserRepo struct {
	col *mongo.Collection
}

func NewUserRepo(db *mongo.Database) *UserRepo {
	return &UserRepo{col: db.Collection("users")}
}

// NormalizeEmail is exported because the unique index is on the stored form:
// anything that looks up or creates a user must apply the same rule, or two
// rows differing only in case will both be accepted.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (r *UserRepo) Create(ctx context.Context, u *models.User) error {
	now := time.Now().UTC()
	u.ID = primitive.NewObjectID()
	u.Email = NormalizeEmail(u.Email)
	u.CreatedAt = now
	u.UpdatedAt = now
	_, err := r.col.InsertOne(ctx, u)
	return translate(err)
}

func (r *UserRepo) FindByEmail(ctx context.Context, email string) (*models.User, error) {
	var u models.User
	err := r.col.FindOne(ctx, bson.M{"email": NormalizeEmail(email)}).Decode(&u)
	if err != nil {
		return nil, translate(err)
	}
	return &u, nil
}

func (r *UserRepo) FindByID(ctx context.Context, id primitive.ObjectID) (*models.User, error) {
	var u models.User
	if err := r.col.FindOne(ctx, bson.M{"_id": id}).Decode(&u); err != nil {
		return nil, translate(err)
	}
	return &u, nil
}

// FindByIDAndRole is FindByID with the role asserted in the query rather than
// after it. Used wherever an id arrives from a request body — "employee_id"
// on a booking, say — so pointing it at a customer is a 404 rather than a
// booking assigned to someone who does not wash cars.
func (r *UserRepo) FindByIDAndRole(ctx context.Context, id primitive.ObjectID, role models.Role) (*models.User, error) {
	var u models.User
	if err := r.col.FindOne(ctx, bson.M{"_id": id, "role": role}).Decode(&u); err != nil {
		return nil, translate(err)
	}
	return &u, nil
}

// ListByRole returns every user holding role, newest first.
func (r *UserRepo) ListByRole(ctx context.Context, role models.Role) ([]*models.User, error) {
	cur, err := r.col.Find(ctx,
		bson.M{"role": role},
		options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
	if err != nil {
		return nil, translate(err)
	}
	defer cur.Close(ctx)

	var out []*models.User
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// FindManyByIDs returns the named users keyed by id. Used to attach names to
// report rows and timesheets in one query instead of one per row.
func (r *UserRepo) FindManyByIDs(ctx context.Context, ids []primitive.ObjectID) (map[primitive.ObjectID]*models.User, error) {
	out := make(map[primitive.ObjectID]*models.User, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	cur, err := r.col.Find(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return nil, translate(err)
	}
	defer cur.Close(ctx)

	var users []*models.User
	if err := cur.All(ctx, &users); err != nil {
		return nil, err
	}
	for _, u := range users {
		out[u.ID] = u
	}
	return out, nil
}

func (r *UserRepo) UpdateStatus(ctx context.Context, id primitive.ObjectID, status models.UserStatus) error {
	res, err := r.col.UpdateOne(ctx,
		bson.M{"_id": id},
		bson.M{"$set": bson.M{"status": status, "updated_at": time.Now().UTC()}})
	if err != nil {
		return translate(err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

// CountByRole is used by the manager bootstrap to decide whether a first
// account is needed.
func (r *UserRepo) CountByRole(ctx context.Context, role models.Role) (int64, error) {
	return r.col.CountDocuments(ctx, bson.M{"role": role})
}
