package repository

import (
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// TestSparseUniqueIndexesAreSingleField is here because this was got wrong,
// and the way it failed is worth keeping.
//
// A booking reference was given a unique index on (tenant_id, reference)
// with SetSparse(true), on the assumption that sparse would keep the
// bookings made before references existed out of it. It does not. A COMPOUND
// sparse index skips a document only when EVERY indexed field is missing,
// and tenant_id is never missing — so every one of those bookings was
// indexed at reference: null, the second collided with the first, the index
// could not be built, and the service refused to start.
//
// The fix is the shape slot_key and open_key already had: a single derived
// field carrying the tenant inside it. This test states the rule so the next
// person reaching for a compound sparse unique index finds out here, in a
// second, rather than from a failed deploy.
func TestSparseUniqueIndexesAreSingleField(t *testing.T) {
	for _, s := range indexSpecs() {
		opts := s.model.Options
		if opts == nil || opts.Unique == nil || !*opts.Unique {
			continue
		}
		if opts.Sparse == nil || !*opts.Sparse {
			continue
		}

		keys, ok := s.model.Keys.(bson.D)
		if !ok {
			t.Fatalf("%s: keys are %T, not bson.D — this test cannot read them", s.collection, s.model.Keys)
		}
		if len(keys) > 1 {
			names := make([]string, 0, len(keys))
			for _, k := range keys {
				names = append(names, k.Key)
			}
			t.Errorf("%s: index %v is unique+sparse over %d fields (%v). "+
				"Compound sparse skips a document only when ALL of its fields are missing, "+
				"so any always-present field here (tenant_id, for one) puts every row in the "+
				"index with the others null — and the second row collides. "+
				"Use a single derived key with the tenant inside it, as slot_key and open_key do.",
				s.collection, nameOf(opts), len(keys), names)
		}
	}
}

// TestUniqueIndexesAreScopedToATenant guards the other half: uniqueness that
// holds across businesses is not a stricter version of uniqueness within
// one, it is a different and wrong constraint. It would mean one business
// hiring somebody the business next door already employs gets a refusal it
// can neither explain nor fix.
func TestUniqueIndexesAreScopedToATenant(t *testing.T) {
	// Keys that carry the tenant inside the value rather than as a field.
	// Each is built by a function in internal/models that prefixes the
	// tenant id, which is what makes the single-field form safe.
	derived := map[string]bool{
		"slot_key":      true,
		"open_key":      true,
		"login_key":     true,
		"contact_key":   true,
		"reference_key": true,
	}

	for _, s := range indexSpecs() {
		opts := s.model.Options
		if opts == nil || opts.Unique == nil || !*opts.Unique {
			continue
		}
		keys := s.model.Keys.(bson.D)

		if len(keys) == 1 && derived[keys[0].Key] {
			continue
		}
		if keys[0].Key == "tenant_id" {
			continue
		}
		t.Errorf("%s: unique index %v leads with %q. A unique index must be scoped to one "+
			"business, either by leading with tenant_id or by using a derived key that "+
			"carries the tenant inside it.", s.collection, nameOf(opts), keys[0].Key)
	}
}

// nameOf is the index name for an error message. Unnamed indexes exist, and
// "" in a failure is less useful than saying so.
func nameOf(opts *options.IndexOptions) string {
	if opts == nil || opts.Name == nil {
		return "(unnamed)"
	}
	return *opts.Name
}
