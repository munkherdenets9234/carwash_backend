// Package repository is the only place that talks to MongoDB. Services take
// repositories, never a *mongo.Database, so a query cannot be written into a
// handler and quietly bypass the constraints below.
package repository

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// EnsureIndexes creates every index this service relies on.
//
// Failure here stops startup rather than degrading (see internal/bootstrap).
// These are not a performance nicety: the unique ones are correctness
// constraints. Two users sharing an email means a login that resolves to
// whichever row Mongo returned first, and two open time entries for one
// employee means a timesheet that silently double-counts a shift.
//
// Every one of them is now scoped by tenant, and that changes what "unique"
// protects. A global unique index on email would mean one business hiring
// somebody the business next door already employs gets a refusal it cannot
// explain and cannot fix. Uniqueness has to hold WITHIN a tenant and must
// not hold ACROSS tenants; those are different constraints, and only the
// compound index expresses the one we want.
//
// tenant_id leads every compound key. Mongo can use a prefix of a compound
// index, so a key of (tenant_id, x) also serves a query on tenant_id alone,
// and since every query this service makes is tenant-scoped, leading with it
// means one index serves both the scoped lookup and the scoped scan.
func EnsureIndexes(ctx context.Context, db *mongo.Database) error {
	for _, s := range indexSpecs() {
		if _, err := db.Collection(s.collection).Indexes().CreateOne(ctx, s.model); err != nil {
			return err
		}
	}
	return nil
}

// indexSpec is one index and where it lives.
type indexSpec struct {
	collection string
	model      mongo.IndexModel
}

// indexSpecs is the list, split out from EnsureIndexes so a test can check
// the SHAPE of these without a database. One rule in particular is checked
// there, and it is checked because it was already broken once: see
// TestSparseUniqueIndexesAreSingleField.
func indexSpecs() []indexSpec {
	return []indexSpec{
		// One login per email address per tenant, across all three roles.
		//
		// Per tenant, not global. The same person may hold an account at two
		// businesses on this platform; those are two accounts, because the
		// businesses are unrelated and neither should learn the other has
		// that customer. A global unique index would also mean one business
		// hiring somebody the business next door already employs gets a
		// refusal it can neither explain nor fix.
		//
		// The key is login_key, a derived tenant|email, and NOT (tenant_id,
		// email) as it was. That earlier index was correct for exactly as
		// long as every customer had an account. Guest booking ended that: a
		// guest has no email, Email carries no omitempty so it stores "",
		// and a plain unique index would have refused the SECOND guest of
		// any business with a duplicate-key error describing none of this.
		//
		// Sparse is what makes the difference — a guest has no login_key at
		// all, so guests are simply not in this index, while everyone who
		// can sign in still gets exactly one row per address. See
		// models.LoginKey.
		{collection: "users", model: mongo.IndexModel{
			Keys:    bson.D{{Key: "login_key", Value: 1}},
			Options: options.Index().SetUnique(true).SetSparse(true).SetName("one_login_per_email_per_tenant_v2"),
		}},

		// One customer row per phone number per tenant.
		//
		// This is what makes a guest who books a second time the same
		// customer rather than a new one, so a business sees one person with
		// three visits instead of three people with one each.
		//
		// It constrains identity, never authority. Holding this key proves
		// nothing about whose phone it is; reading a booking back needs the
		// reference code as well. See models.ContactKey.
		{collection: "users", model: mongo.IndexModel{
			Keys:    bson.D{{Key: "contact_key", Value: 1}},
			Options: options.Index().SetUnique(true).SetSparse(true).SetName("one_customer_per_phone_per_tenant"),
		}},
		{collection: "users", model: mongo.IndexModel{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "role", Value: 1}, {Key: "status", Value: 1}}}},

		// A plate is unique to its owner, not globally — see models.Car.
		// owner_id already implies the tenant, so this one would be correct
		// without the prefix; it leads with tenant_id anyway so every index
		// on the collection scopes the same way and no reader has to work
		// out which ones are the exception.
		{collection: "cars", model: mongo.IndexModel{
			Keys:    bson.D{{Key: "tenant_id", Value: 1}, {Key: "owner_id", Value: 1}, {Key: "plate", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("one_plate_per_owner_per_tenant"),
		}},

		// Availability reads shifts by employee and window; the roster view
		// reads them by location and window.
		{collection: "shifts", model: mongo.IndexModel{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "employee_id", Value: 1}, {Key: "start_at", Value: 1}}}},
		{collection: "shifts", model: mongo.IndexModel{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "location_id", Value: 1}, {Key: "start_at", Value: 1}}}},

		// The overlap check on booking is the hot path, and the daily report
		// scans a day by status.
		{collection: "reservations", model: mongo.IndexModel{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "employee_id", Value: 1}, {Key: "start_at", Value: 1}}}},
		{collection: "reservations", model: mongo.IndexModel{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "customer_id", Value: 1}, {Key: "start_at", Value: -1}}}},
		{collection: "reservations", model: mongo.IndexModel{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "start_at", Value: 1}, {Key: "status", Value: 1}}}},
		{collection: "reservations", model: mongo.IndexModel{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "completed_at", Value: 1}}, Options: options.Index().SetSparse(true)}},

		// The guest lookup path, and the constraint that makes a reference
		// mean one booking.
		//
		// Keyed on the derived reference_key, NOT compound on (tenant_id,
		// reference). The compound form was written first and could not be
		// built: a compound sparse index skips a document only when every
		// indexed field is missing, tenant_id never is, so every booking
		// predating references was indexed at reference: null and the second
		// one collided with the first. Startup failed outright.
		//
		// Single-field sparse has no such edge: a booking with no reference
		// has no reference_key and is not in the index. Same shape as
		// slot_key and open_key, and now for a third recorded reason.
		//
		// The tenant is inside the key, so uniqueness holds per business:
		// two businesses issuing the same code is not a collision anybody
		// can observe, because a lookup always arrives with the tenant
		// already resolved from the API key.
		{collection: "reservations", model: mongo.IndexModel{
			Keys:    bson.D{{Key: "reference_key", Value: 1}},
			Options: options.Index().SetUnique(true).SetSparse(true).SetName("one_booking_per_reference_per_tenant_v2"),
		}},

		// One booking per employee per start instant. Sparse, so the many
		// cancelled rows — which drop the key — do not collide with each
		// other. See models.Reservation.SlotKey for what this does and does
		// not protect against.
		//
		// slot_key now carries the tenant id as its first segment. The key
		// was already correct without it - an employee belongs to exactly
		// one tenant, so two tenants cannot produce the same employee id -
		// but the INDEX is global, and a reader checking whether that is
		// safe would have to reason about employee ownership to find out.
		// With the prefix the reasoning stays local to the key.
		{collection: "reservations", model: mongo.IndexModel{
			Keys:    bson.D{{Key: "slot_key", Value: 1}},
			Options: options.Index().SetUnique(true).SetSparse(true).SetName("one_booking_per_employee_start"),
		}},

		// At most one open entry per employee.
		//
		// open_key holds the employee id while the entry is running and is
		// unset at clock-out, so a sparse unique index on it forbids a
		// second clock-in while allowing an employee any number of closed
		// entries. The database refuses the duplicate, not only the service
		// check — that check races with itself under two concurrent taps,
		// the index does not.
		//
		// The direct form, unique on employee_id with a partial filter of
		// {clock_out_at: {$exists: false}}, is not available: the server
		// answers "Expression not supported in partial index: $not". See
		// models.TimeEntry.OpenKey.
		//
		// open_key carries the tenant id as its first segment too, for the
		// same reason as slot_key above.
		{collection: "time_entries", model: mongo.IndexModel{
			Keys: bson.D{{Key: "open_key", Value: 1}},
			Options: options.Index().
				SetUnique(true).
				SetSparse(true).
				SetName("one_open_entry_per_employee"),
		}},
		{collection: "time_entries", model: mongo.IndexModel{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "employee_id", Value: 1}, {Key: "clock_in_at", Value: -1}}}},
		{collection: "time_entries", model: mongo.IndexModel{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "clock_in_at", Value: -1}}}},

		{collection: "wash_services", model: mongo.IndexModel{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "active", Value: 1}, {Key: "price_mnt", Value: 1}}}},
		{collection: "locations", model: mongo.IndexModel{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "active", Value: 1}}}},

		// Media is read in display order on every page load of the site, and
		// always for one tenant and usually one role.
		//
		// There is deliberately NO unique index enforcing one hero per
		// tenant. It would need a partial filter over a set of roles, and
		// this server has already refused one partial expression here (see
		// the time_entries note above) — a startup-fatal index is a poor
		// place to discover the second. The single-slot rule is enforced in
		// the service instead, by demoting the previous holder rather than
		// refusing the new one, which is the behaviour a person wants
		// anyway.
		{collection: "media", model: mongo.IndexModel{Keys: bson.D{
			{Key: "tenant_id", Value: 1},
			{Key: "role", Value: 1},
			{Key: "sort_order", Value: 1},
		}}},
	}
}
