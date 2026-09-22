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
func EnsureIndexes(ctx context.Context, db *mongo.Database) error {
	specs := []struct {
		collection string
		model      mongo.IndexModel
	}{
		// One login per email address, across all three roles.
		{"users", mongo.IndexModel{
			Keys:    bson.D{{Key: "email", Value: 1}},
			Options: options.Index().SetUnique(true),
		}},
		{"users", mongo.IndexModel{Keys: bson.D{{Key: "role", Value: 1}, {Key: "status", Value: 1}}}},

		// A plate is unique to its owner, not globally — see models.Car.
		{"cars", mongo.IndexModel{
			Keys:    bson.D{{Key: "owner_id", Value: 1}, {Key: "plate", Value: 1}},
			Options: options.Index().SetUnique(true),
		}},

		// Availability reads shifts by employee and window; the roster view
		// reads them by location and window.
		{"shifts", mongo.IndexModel{Keys: bson.D{{Key: "employee_id", Value: 1}, {Key: "start_at", Value: 1}}}},
		{"shifts", mongo.IndexModel{Keys: bson.D{{Key: "location_id", Value: 1}, {Key: "start_at", Value: 1}}}},

		// The overlap check on booking is the hot path, and the daily report
		// scans a day by status.
		{"reservations", mongo.IndexModel{Keys: bson.D{{Key: "employee_id", Value: 1}, {Key: "start_at", Value: 1}}}},
		{"reservations", mongo.IndexModel{Keys: bson.D{{Key: "customer_id", Value: 1}, {Key: "start_at", Value: -1}}}},
		{"reservations", mongo.IndexModel{Keys: bson.D{{Key: "start_at", Value: 1}, {Key: "status", Value: 1}}}},
		{"reservations", mongo.IndexModel{Keys: bson.D{{Key: "completed_at", Value: 1}}, Options: options.Index().SetSparse(true)}},

		// One booking per employee per start instant. Sparse, so the many
		// cancelled rows — which drop the key — do not collide with each
		// other. See models.Reservation.SlotKey for what this does and does
		// not protect against.
		{"reservations", mongo.IndexModel{
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
		{"time_entries", mongo.IndexModel{
			Keys: bson.D{{Key: "open_key", Value: 1}},
			Options: options.Index().
				SetUnique(true).
				SetSparse(true).
				SetName("one_open_entry_per_employee"),
		}},
		{"time_entries", mongo.IndexModel{Keys: bson.D{{Key: "employee_id", Value: 1}, {Key: "clock_in_at", Value: -1}}}},
		{"time_entries", mongo.IndexModel{Keys: bson.D{{Key: "clock_in_at", Value: -1}}}},

		{"wash_services", mongo.IndexModel{Keys: bson.D{{Key: "active", Value: 1}, {Key: "price_mnt", Value: 1}}}},
		{"locations", mongo.IndexModel{Keys: bson.D{{Key: "active", Value: 1}}}},
	}

	for _, s := range specs {
		if _, err := db.Collection(s.collection).Indexes().CreateOne(ctx, s.model); err != nil {
			return err
		}
	}
	return nil
}
