package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Role is the closed set of things a person can be here. Authorisation is
// role-based and applied to whole route groups (see internal/api), so adding
// a role means deciding which groups it may enter — it is not a free-text
// field.
type Role string

const (
	RoleManager  Role = "manager"
	RoleEmployee Role = "employee"
	RoleCustomer Role = "customer"
)

// Valid reports whether r is one of the three known roles. Used wherever a
// role arrives from outside (staff creation, seeding): an unrecognised role
// is refused at the door rather than stored, where it would instead fail
// every later authorisation check for reasons nobody can see.
func (r Role) Valid() bool {
	switch r {
	case RoleManager, RoleEmployee, RoleCustomer:
		return true
	}
	return false
}

type UserStatus string

const (
	UserActive    UserStatus = "active"
	UserSuspended UserStatus = "suspended"
)

// EmployeeProfile is the extra data only an employee carries. It is a pointer
// on User rather than a separate collection because every read of an employee
// wants it, and a nil value is a precise statement: this person is not staff.
type EmployeeProfile struct {
	// HomeLocationID is the branch this employee is normally rostered to.
	// Clock-in is checked against the location on the shift, not against
	// this one — this is only the default used when building a roster.
	HomeLocationID primitive.ObjectID `bson:"home_location_id" json:"home_location_id"`
	HiredAt        time.Time          `bson:"hired_at" json:"hired_at"`
}

// User is one login profile. Managers, employees and customers share a
// collection because they share an authentication flow; what differs is the
// role, which decides the route groups their token may enter.
type User struct {
	ID primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	// TenantID is the business this row belongs to. Every query filters on
	// it, in the filter itself rather than as a check after the read, so a
	// forgotten scope is an empty result rather than another business data.
	//
	// json:"-" because it is never on the wire: a client already proved
	// which tenant it is by presenting the API key, and echoing the id back
	// tells it nothing it can use.
	TenantID primitive.ObjectID `bson:"tenant_id" json:"-"`
	Role     Role               `bson:"role" json:"role"`

	Name  string `bson:"name" json:"name"`
	Email string `bson:"email" json:"email"`
	Phone string `bson:"phone,omitempty" json:"phone,omitempty"`

	// LoginKey is tenant + email, and is absent for a customer who has never
	// registered. The sparse unique index on it is what enforces one login
	// per email per business while allowing any number of guests, who have
	// no email at all. See models.LoginKey.
	LoginKey string `bson:"login_key,omitempty" json:"-"`

	// ContactKey is tenant + normalised phone, and is what makes a guest
	// booking twice one customer rather than two. See models.ContactKey.
	ContactKey string `bson:"contact_key,omitempty" json:"-"`

	// PasswordHash must never reach a response. The json:"-" here is a
	// backstop, not the mechanism: nothing serialises a models.User
	// directly, because every response is built by hand in internal/view.
	PasswordHash string `bson:"password_hash" json:"-"`

	Status   UserStatus       `bson:"status" json:"status"`
	Employee *EmployeeProfile `bson:"employee,omitempty" json:"employee,omitempty"`

	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}
