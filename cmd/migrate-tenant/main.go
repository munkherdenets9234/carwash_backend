// Command migrate-tenant backfills the tenant on data that predates
// multi-tenancy.
//
// The car wash began as one business, so every row was implicitly that
// business's. Adoption into the platform made the tenant explicit and put it
// in the filter of every query — which means those original rows became
// invisible the moment the new code shipped. They are not lost, they belong
// to nobody, and nobody is who every scoped query returns them to.
//
// This assigns them to one tenant. Run the dry run first; it reports counts,
// changes nothing, and is the only way to find out what the real run would do
// before it does it.
//
//	go run ./cmd/migrate-tenant -dry-run
//	go run ./cmd/migrate-tenant
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/eandstravel/carwash/internal/config"
	"github.com/eandstravel/carwash/internal/models"
	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// collections is every collection the car wash owns. All seven, listed
// explicitly rather than discovered: a collection added later must be a
// deliberate edit here, because a backfill that silently skips one leaves
// exactly the kind of half-migrated database that is worst to debug.
var collections = []string{
	"users", "cars", "locations", "wash_services",
	"shifts", "reservations", "time_entries",
}

func main() {
	dryRun := flag.Bool("dry-run", false, "report what would change and write nothing")
	tenantFlag := flag.String("tenant", "", "tenant id to assign (defaults to BOOTSTRAP_TENANT_ID)")
	flag.Parse()

	_ = godotenv.Load()
	cfg := config.Load()

	raw := *tenantFlag
	if raw == "" {
		raw = cfg.BootstrapTenantID
	}
	if raw == "" {
		fail("no tenant given: pass -tenant <id> or set BOOTSTRAP_TENANT_ID")
	}
	tenantID, err := primitive.ObjectIDFromHex(raw)
	if err != nil {
		fail(fmt.Sprintf("%q is not a valid tenant id", raw))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.MongoURI))
	if err != nil {
		fail("mongo connect: " + err.Error())
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	if err := client.Ping(ctx, nil); err != nil {
		fail("mongo ping: " + err.Error())
	}
	db := client.Database(cfg.MongoDB)

	mode := "APPLY"
	if *dryRun {
		mode = "DRY RUN — nothing is written"
	}
	fmt.Printf("database %s · tenant %s · %s\n\n", cfg.MongoDB, tenantID.Hex(), mode)

	// ── 1. Backfill tenant_id ────────────────────────────────────────────
	//
	// Only rows that HAVE NO tenant at all are touched. A row already
	// carrying a different tenant is somebody else's and is left alone —
	// this command must be safe to run twice, and safe to run on a database
	// that is already partly migrated.
	fmt.Println("tenant_id backfill:")
	orphanFilter := bson.M{"tenant_id": bson.M{"$exists": false}}
	totalOrphans := int64(0)
	for _, col := range collections {
		n, err := db.Collection(col).CountDocuments(ctx, orphanFilter)
		if err != nil {
			fail(col + ": count: " + err.Error())
		}
		totalOrphans += n

		if *dryRun || n == 0 {
			fmt.Printf("  %-14s %d\n", col, n)
			continue
		}
		res, err := db.Collection(col).UpdateMany(ctx, orphanFilter, bson.M{"$set": bson.M{"tenant_id": tenantID}})
		if err != nil {
			fail(col + ": update: " + err.Error())
		}
		fmt.Printf("  %-14s %d assigned\n", col, res.ModifiedCount)
	}
	if totalOrphans == 0 {
		fmt.Println("  (nothing to assign — already migrated)")
	}

	// ── 2. Rebuild the derived keys ──────────────────────────────────────
	//
	// This is the half that is easy to forget and expensive to skip.
	//
	// slot_key and open_key back UNIQUE indexes, and both now begin with the
	// tenant id. A row backfilled in step 1 still carries the old key, which
	// no longer matches what the code writes — so the index stops protecting
	// what it was built to protect, and worse, a correct booking can collide
	// with a stale key and be refused with nothing to explain it.
	//
	// Both keys are sparse by design: absent means "not live". An absent key
	// must stay absent, or a cancelled booking starts blocking its old slot
	// forever.
	fmt.Println("\nderived keys:")
	if err := rebuildSlotKeys(ctx, db, tenantID, *dryRun); err != nil {
		fail("slot_key: " + err.Error())
	}
	if err := rebuildOpenKeys(ctx, db, tenantID, *dryRun); err != nil {
		fail("open_key: " + err.Error())
	}

	// ── 3. Report stale indexes ──────────────────────────────────────────
	//
	// Reported, never dropped. EnsureIndexes creates the new per-tenant
	// compound indexes at startup but has no business removing the old
	// global ones, and neither has this: dropping an index is instant and
	// rebuilding it on a large collection is not, so it stays a decision a
	// person makes while looking at the list.
	fmt.Println("\nindexes that are now too strict (drop by hand when ready):")
	reportStaleIndexes(ctx, db)

	fmt.Println()
	if *dryRun {
		fmt.Println("dry run complete — re-run without -dry-run to apply")
		return
	}
	fmt.Println("done")
}

// rebuildSlotKeys rewrites slot_key for live bookings.
func rebuildSlotKeys(ctx context.Context, db *mongo.Database, tenantID primitive.ObjectID, dry bool) error {
	col := db.Collection("reservations")
	// Only rows that HAVE a key: an absent one means the booking is no
	// longer holding its slot, and inventing a key for it would re-block a
	// time somebody has already been given back.
	filter := bson.M{"slot_key": bson.M{"$exists": true}}

	cur, err := col.Find(ctx, filter)
	if err != nil {
		return err
	}
	defer cur.Close(ctx)

	changed, already := 0, 0
	for cur.Next(ctx) {
		var r struct {
			ID         primitive.ObjectID `bson:"_id"`
			EmployeeID primitive.ObjectID `bson:"employee_id"`
			StartAt    time.Time          `bson:"start_at"`
			SlotKey    string             `bson:"slot_key"`
		}
		if err := cur.Decode(&r); err != nil {
			return err
		}
		want := models.SlotKey(tenantID, r.EmployeeID, r.StartAt)
		if r.SlotKey == want {
			already++
			continue
		}
		changed++
		if dry {
			continue
		}
		if _, err := col.UpdateByID(ctx, r.ID, bson.M{"$set": bson.M{"slot_key": want}}); err != nil {
			return err
		}
	}
	verb := "would rewrite"
	if !dry {
		verb = "rewrote"
	}
	fmt.Printf("  slot_key       %s %d (%d already correct)\n", verb, changed, already)
	return cur.Err()
}

// rebuildOpenKeys rewrites open_key for running time entries.
func rebuildOpenKeys(ctx context.Context, db *mongo.Database, tenantID primitive.ObjectID, dry bool) error {
	col := db.Collection("time_entries")
	filter := bson.M{"open_key": bson.M{"$exists": true}}

	cur, err := col.Find(ctx, filter)
	if err != nil {
		return err
	}
	defer cur.Close(ctx)

	changed, already := 0, 0
	for cur.Next(ctx) {
		var e struct {
			ID         primitive.ObjectID `bson:"_id"`
			EmployeeID primitive.ObjectID `bson:"employee_id"`
			OpenKey    string             `bson:"open_key"`
		}
		if err := cur.Decode(&e); err != nil {
			return err
		}
		want := models.OpenKey(tenantID, e.EmployeeID)
		if e.OpenKey == want {
			already++
			continue
		}
		changed++
		if dry {
			continue
		}
		if _, err := col.UpdateByID(ctx, e.ID, bson.M{"$set": bson.M{"open_key": want}}); err != nil {
			return err
		}
	}
	verb := "would rewrite"
	if !dry {
		verb = "rewrote"
	}
	fmt.Printf("  open_key       %s %d (%d already correct)\n", verb, changed, already)
	return cur.Err()
}

// reportStaleIndexes lists unique indexes that do not lead with tenant_id.
//
// A global unique index on users.email is the one that bites first: it means
// the second business to hire somebody the first one already employs gets a
// duplicate-key error it can neither explain nor fix.
func reportStaleIndexes(ctx context.Context, db *mongo.Database) {
	found := false
	for _, col := range collections {
		cur, err := db.Collection(col).Indexes().List(ctx)
		if err != nil {
			continue
		}
		var idx []bson.M
		if err := cur.All(ctx, &idx); err != nil {
			continue
		}
		for _, ix := range idx {
			name, _ := ix["name"].(string)
			if name == "_id_" {
				continue
			}
			unique, _ := ix["unique"].(bool)
			if !unique {
				continue
			}
			keys, ok := ix["key"].(bson.M)
			if !ok {
				continue
			}
			// The two derived keys carry the tenant inside the value, so a
			// unique index on them alone is correct and not stale.
			if _, isSlot := keys["slot_key"]; isSlot {
				continue
			}
			if _, isOpen := keys["open_key"]; isOpen {
				continue
			}
			if _, scoped := keys["tenant_id"]; scoped {
				continue
			}
			found = true
			fmt.Printf("  %-14s %s  keys=%v\n", col, name, keys)
		}
	}
	if !found {
		fmt.Println("  (none)")
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "migrate-tenant: "+msg)
	os.Exit(1)
}
