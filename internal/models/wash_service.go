package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// WashService is one item on the price list: what it costs, how long it
// occupies a bay, and what the employee earns for doing it.
type WashService struct {
	ID primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	// TenantID is the business this row belongs to. Every query filters on
	// it, in the filter itself rather than as a check after the read, so a
	// forgotten scope is an empty result rather than another business data.
	//
	// json:"-" because it is never on the wire: a client already proved
	// which tenant it is by presenting the API key, and echoing the id back
	// tells it nothing it can use.
	TenantID    primitive.ObjectID `bson:"tenant_id" json:"-"`
	Name        string             `bson:"name" json:"name"`
	Description string             `bson:"description,omitempty" json:"description,omitempty"`

	// DurationMin drives availability: a slot is offered only when the whole
	// duration fits inside the employee's shift and overlaps no other
	// booking.
	DurationMin int `bson:"duration_min" json:"duration_min"`

	PriceMNT MNT `bson:"price_mnt" json:"price_mnt"`

	// BonusMNT is what the assigned employee earns for completing one of
	// these. A flat amount per wash rather than a percentage of the price:
	// the employee does the same work whether or not the customer had a
	// discount, and a percentage would make every price change silently a
	// pay change.
	BonusMNT MNT `bson:"bonus_mnt" json:"bonus_mnt"`

	Active    bool      `bson:"active" json:"active"`
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}
