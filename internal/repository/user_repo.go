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

// Create stores a login profile. u.TenantID must already be set.
func (r *UserRepo) Create(ctx context.Context, u *models.User) error {
	now := time.Now().UTC()
	u.ID = primitive.NewObjectID()
	u.Email = NormalizeEmail(u.Email)
	u.Phone = models.NormalizePhone(u.Phone)

	// The derived keys are set HERE and not by callers.
	//
	// Three separate services build a models.User — registration, staff
	// creation and the bootstrap manager — and a fourth now creates guests.
	// Each one would have to remember these two lines, and a unique index
	// whose key the writer forgot is not a constraint that fails loudly, it
	// is a constraint that silently stops existing: duplicate emails would
	// simply start being accepted, and nothing would report it.
	//
	// There is exactly one insert path, so deriving them at the insert is
	// the only version nobody can forget.
	u.LoginKey = models.LoginKey(u.TenantID, u.Email)
	if u.Role == models.RoleCustomer {
		// Customers only: an employee sharing a phone number with a customer
		// would otherwise take the contact key, and the customer's next
		// booking would be refused for a reason invisible from the booking
		// screen. See models.ContactKey.
		u.ContactKey = models.ContactKey(u.TenantID, u.Phone)
	}

	u.CreatedAt = now
	u.UpdatedAt = now
	_, err := r.col.InsertOne(ctx, u)
	return translate(err)
}

// FindByContactKey resolves the customer who books with a phone number.
//
// It answers "have we seen this number before", never "is this person who
// they say they are". Anyone can type anyone's number; what this makes
// possible is recognising a returning customer, not admitting one. Reading a
// booking back requires its reference code as well.
func (r *UserRepo) FindByContactKey(ctx context.Context, tenantID primitive.ObjectID, phone string) (*models.User, error) {
	key := models.ContactKey(tenantID, phone)
	if key == "" {
		return nil, ErrNotFound
	}
	var u models.User
	// tenant_id is in the filter as well as in the key. The key already
	// carries it, so this is redundant — and it stays, because every other
	// query in this file scopes the same way and an exception is something a
	// reader has to stop and verify.
	err := r.col.FindOne(ctx, scoped(tenantID, bson.M{"contact_key": key})).Decode(&u)
	if err != nil {
		return nil, translate(err)
	}
	return &u, nil
}

// UpgradeGuest turns a guest customer record into an account with a login.
//
// The row is NOT recreated. That is the whole point: the guest already owns
// bookings, and those bookings point at this id, so creating a second user
// and copying an email across would leave the history behind on a row nobody
// can sign in to. Upgrading in place means the account arrives with
// everything that person has ever booked already attached.
//
// The filter carries the guard, not a read-then-write: a row that already
// holds a password is somebody's ACCOUNT, and taking it over is exactly the
// thing that must not happen. Checking that in Go would leave a window
// between the check and the update; putting it in the filter means the
// database refuses it. A caller that matched nothing gets ErrNotFound and
// must not treat it as success.
func (r *UserRepo) UpgradeGuest(ctx context.Context, tenantID, id primitive.ObjectID, name, email, passwordHash string) error {
	email = NormalizeEmail(email)

	filter := scoped(tenantID, bson.M{
		"_id":  id,
		"role": models.RoleCustomer,
		// Empty or absent. Guests store "" because Email and PasswordHash
		// carry no omitempty, but a row written by an older version may have
		// neither field at all.
		"password_hash": bson.M{"$in": bson.A{"", nil}},
	})

	set := bson.M{
		"email":         email,
		"login_key":     models.LoginKey(tenantID, email),
		"password_hash": passwordHash,
		"updated_at":    time.Now().UTC(),
	}
	if strings.TrimSpace(name) != "" {
		// A guest booking with no name was stored as "Guest 99112233". Now
		// that there is a real one, it replaces the placeholder — but an
		// empty field on the form must not wipe a name they already gave.
		set["name"] = strings.TrimSpace(name)
	}

	res, err := r.col.UpdateOne(ctx, filter, bson.M{"$set": set})
	if err != nil {
		return translate(err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

// FindByEmail resolves a login WITHIN one tenant.
//
// This is the single most important scope in the service. Unscoped, a person
// registered at business A could sign in through business B's storefront and
// arrive authenticated inside a business they have no relationship with. The
// email is unique per tenant precisely so that the same address at two
// businesses is two separate accounts, and this query is where that becomes
// true rather than merely stated in an index.
func (r *UserRepo) FindByEmail(ctx context.Context, tenantID primitive.ObjectID, email string) (*models.User, error) {
	var u models.User
	err := r.col.FindOne(ctx, scoped(tenantID, bson.M{"email": NormalizeEmail(email)})).Decode(&u)
	if err != nil {
		return nil, translate(err)
	}
	return &u, nil
}

func (r *UserRepo) FindByID(ctx context.Context, tenantID, id primitive.ObjectID) (*models.User, error) {
	var u models.User
	if err := r.col.FindOne(ctx, scopedID(tenantID, id)).Decode(&u); err != nil {
		return nil, translate(err)
	}
	return &u, nil
}

// FindByIDAndRole is FindByID with the role asserted in the query rather than
// after it. Used wherever an id arrives from a request body — "employee_id"
// on a booking, say — so pointing it at a customer is a 404 rather than a
// booking assigned to someone who does not wash cars.
//
// Pointing it at another TENANT's employee is a 404 for the same reason, and
// that is the case that matters: an employee id is an opaque 24-character
// string, and without the tenant in the filter a guessed one from another
// business would resolve.
func (r *UserRepo) FindByIDAndRole(ctx context.Context, tenantID, id primitive.ObjectID, role models.Role) (*models.User, error) {
	var u models.User
	if err := r.col.FindOne(ctx, scoped(tenantID, bson.M{"_id": id, "role": role})).Decode(&u); err != nil {
		return nil, translate(err)
	}
	return &u, nil
}

// ListByRole returns every user in this tenant holding role, by name.
func (r *UserRepo) ListByRole(ctx context.Context, tenantID primitive.ObjectID, role models.Role) ([]*models.User, error) {
	cur, err := r.col.Find(ctx,
		scoped(tenantID, bson.M{"role": role}),
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
func (r *UserRepo) FindManyByIDs(ctx context.Context, tenantID primitive.ObjectID, ids []primitive.ObjectID) (map[primitive.ObjectID]*models.User, error) {
	out := make(map[primitive.ObjectID]*models.User, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	cur, err := r.col.Find(ctx, scoped(tenantID, bson.M{"_id": bson.M{"$in": ids}}))
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

func (r *UserRepo) UpdateStatus(ctx context.Context, tenantID, id primitive.ObjectID, status models.UserStatus) error {
	res, err := r.col.UpdateOne(ctx,
		scopedID(tenantID, id),
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
// account is needed — per tenant, since every tenant needs its own first
// manager.
func (r *UserRepo) CountByRole(ctx context.Context, tenantID primitive.ObjectID, role models.Role) (int64, error) {
	return r.col.CountDocuments(ctx, scoped(tenantID, bson.M{"role": role}))
}
