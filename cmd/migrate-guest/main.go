// Command migrate-guest prepares the user collection for booking without an
// account.
//
// Guest booking removes the requirement that a customer register before
// booking, and that breaks an assumption the schema was built on. Uniqueness
// of a login used to be enforced by a plain unique index on (tenant_id,
// email). A guest has no email, and models.User.Email carries no omitempty,
// so every guest would store email "" — and the SECOND guest of any business
// would be refused by the database with a duplicate-key error that describes
// none of this.
//
// The replacement is a sparse unique index on a derived login_key, so people
// who can sign in still get exactly one row per address and guests, having no
// key, are simply not in the index. A second sparse unique index on
// contact_key makes a guest who books again the same customer rather than a
// new one.
//
// This command fills both keys in and then, once it has verified the new
// index is real, drops the old one. That ORDER is the whole point. Dropping
// first would leave a window with no uniqueness constraint on email at all,
// and the rows written during a window like that are the ones you find months
// later.
//
// Run the dry run first. It reports what it would write, including any
// conflicts that would make the new indexes impossible, and changes nothing.
//
//	go run ./cmd/migrate-guest -dry-run
//	go run ./cmd/migrate-guest
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/eandstravel/carwash/internal/config"
	"github.com/eandstravel/carwash/internal/models"
	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// oldIndex is the index this migration retires. Named as a constant because
// it is dropped by name, and a typo in a drop is silent — it reports success
// for having removed nothing.
const oldIndex = "one_login_per_email_per_tenant"

// staleGlobalIndex is an index from BEFORE this service was multi-tenant.
//
// It is a global unique index on email, left behind when the tenant-scoped
// one was added. Nothing creates it any more, so reading the source gives no
// hint that it is there. It surfaced by breaking guest booking: a guest has
// no email, models.User.Email carries no omitempty so every guest stores "",
// the first guest took that value globally, and the SECOND was refused with
// a duplicate-key error naming an index that appears nowhere in the code.
//
// Dropping it is a correctness fix, not a tidy-up. Global uniqueness on
// email is the wrong constraint for this service anyway: it means one
// business cannot have a customer the business next door already has.
// one_login_per_email_per_tenant_v2 is the constraint that was wanted, it
// exists, and it holds.
const staleGlobalIndex = "email_1"

type userRow struct {
	ID       primitive.ObjectID `bson:"_id"`
	TenantID primitive.ObjectID `bson:"tenant_id"`
	Role     string             `bson:"role"`
	Email    string             `bson:"email"`
	Phone    string             `bson:"phone"`
}

func main() {
	dryRun := flag.Bool("dry-run", false, "report what would change and write nothing")
	flag.Parse()

	_ = godotenv.Load()
	cfg := config.Load()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.MongoURI))
	if err != nil {
		fail("connect: %v", err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	if err := client.Ping(ctx, nil); err != nil {
		fail("ping: %v", err)
	}
	db := client.Database(cfg.MongoDB)
	users := db.Collection("users")

	cur, err := users.Find(ctx, bson.M{})
	if err != nil {
		fail("read users: %v", err)
	}
	var rows []userRow
	if err := cur.All(ctx, &rows); err != nil {
		fail("decode users: %v", err)
	}
	fmt.Printf("%d users\n", len(rows))

	// Conflicts are found BEFORE anything is written. A unique index cannot
	// be created over data that already violates it, and discovering that
	// halfway through a backfill leaves the collection in a state where some
	// rows have keys and some do not — which is harder to reason about than
	// either end state.
	logins := map[string][]primitive.ObjectID{}
	contacts := map[string][]primitive.ObjectID{}
	for _, r := range rows {
		if k := models.LoginKey(r.TenantID, r.Email); k != "" {
			logins[k] = append(logins[k], r.ID)
		}
		// contact_key is a CUSTOMER identity. Staff phone numbers stay out
		// of it: an employee who shares a number with a customer would
		// otherwise take the key and the customer's second booking would be
		// refused for reasons nobody could see from the booking screen.
		if r.Role == string(models.RoleCustomer) {
			if k := models.ContactKey(r.TenantID, r.Phone); k != "" {
				contacts[k] = append(contacts[k], r.ID)
			}
		}
	}

	conflicts := 0
	conflicts += report("login_key", logins)
	conflicts += report("contact_key", contacts)
	if conflicts > 0 {
		fail("%d conflicting key(s) — resolve these before migrating, or the unique "+
			"index cannot be created and guest booking stays broken", conflicts)
	}

	fmt.Printf("would set login_key on %d user(s), contact_key on %d customer(s)\n",
		len(logins), len(contacts))

	if *dryRun {
		fmt.Println("\ndry run — nothing written.")
		fmt.Printf("the real run would then drop the %q and %q indexes.\n", oldIndex, staleGlobalIndex)
		return
	}

	written := 0
	for _, r := range rows {
		set := bson.M{}
		if k := models.LoginKey(r.TenantID, r.Email); k != "" {
			set["login_key"] = k
		}
		if r.Role == string(models.RoleCustomer) {
			if k := models.ContactKey(r.TenantID, r.Phone); k != "" {
				set["contact_key"] = k
			}
		}
		if len(set) == 0 {
			continue
		}
		if _, err := users.UpdateByID(ctx, r.ID, bson.M{"$set": set}); err != nil {
			fail("update %s: %v", r.ID.Hex(), err)
		}
		written++
	}
	fmt.Printf("keys written on %d user(s)\n", written)

	// The new index is created here rather than left to the next startup,
	// because the drop below is only safe once it exists. Creating it is
	// idempotent, so it does not matter that EnsureIndexes will also try.
	if _, err := users.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "login_key", Value: 1}},
		Options: options.Index().SetUnique(true).SetSparse(true).SetName("one_login_per_email_per_tenant_v2"),
	}); err != nil {
		fail("create replacement index: %v — the old index is untouched, so email "+
			"uniqueness is still enforced and nothing is worse than before", err)
	}
	fmt.Println("replacement index one_login_per_email_per_tenant_v2 present")

	if _, err := users.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "contact_key", Value: 1}},
		Options: options.Index().SetUnique(true).SetSparse(true).SetName("one_customer_per_phone_per_tenant"),
	}); err != nil {
		fail("create contact index: %v", err)
	}
	fmt.Println("index one_customer_per_phone_per_tenant present")

	// Only now is the old one redundant.
	if _, err := users.Indexes().DropOne(ctx, oldIndex); err != nil {
		if strings.Contains(err.Error(), "index not found") {
			fmt.Printf("%q was already gone\n", oldIndex)
		} else {
			fail("drop %s: %v — the replacement is in place, so this is safe to "+
				"retry, but until it succeeds a guest booking will be refused", oldIndex, err)
		}
	} else {
		fmt.Printf("dropped %q\n", oldIndex)
	}

	// Dropped last, after the replacements above are confirmed present, for
	// the same reason as oldIndex: never remove a constraint before the one
	// that supersedes it is real.
	if _, err := users.Indexes().DropOne(ctx, staleGlobalIndex); err != nil {
		if strings.Contains(err.Error(), "index not found") {
			fmt.Printf("%q was already gone\n", staleGlobalIndex)
		} else {
			fail("drop %s: %v - until this succeeds the SECOND guest of this business "+
				"will be refused, because both store an empty email and this index is global",
				staleGlobalIndex, err)
		}
	} else {
		fmt.Printf("dropped %q (pre-tenancy global unique index on email)\n", staleGlobalIndex)
	}

	fmt.Println("\ndone. guest booking can now write a customer with no email.")
}

// report prints every key held by more than one row and returns how many
// there were. These are not hypothetical: two customer rows sharing a phone
// number is exactly what a business ends up with after years of staff typing
// numbers by hand.
func report(name string, byKey map[string][]primitive.ObjectID) int {
	n := 0
	for k, ids := range byKey {
		if len(ids) < 2 {
			continue
		}
		n++
		shown := make([]string, 0, len(ids))
		for _, id := range ids {
			shown = append(shown, id.Hex())
		}
		// The key is printed with the tenant prefix trimmed, because the
		// readable half is what identifies the problem to a person.
		readable := k
		if i := strings.Index(k, "|"); i >= 0 {
			readable = k[i+1:]
		}
		fmt.Printf("  CONFLICT %s %q held by %d rows: %s\n",
			name, readable, len(ids), strings.Join(shown, ", "))
	}
	return n
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "migrate-guest: "+format+"\n", args...)
	os.Exit(1)
}
