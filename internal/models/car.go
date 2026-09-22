package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Car is a vehicle a customer registered. Reservations reference a car rather
// than repeating its plate, so one vehicle's wash history survives the
// customer editing its details.
type Car struct {
	ID      primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	OwnerID primitive.ObjectID `bson:"owner_id" json:"owner_id"`

	// Plate is stored uppercase and is unique per owner, not globally. A
	// plate changes hands, and refusing the new owner's registration because
	// the previous owner's row still exists is a support ticket with no good
	// answer.
	Plate string `bson:"plate" json:"plate"`

	Make  string `bson:"make,omitempty" json:"make,omitempty"`
	Model string `bson:"model,omitempty" json:"model,omitempty"`
	Color string `bson:"color,omitempty" json:"color,omitempty"`
	Notes string `bson:"notes,omitempty" json:"notes,omitempty"`

	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}
