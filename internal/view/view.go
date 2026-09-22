// Package view maps stored documents onto the shapes that go on the wire.
//
// Its whole purpose is that those shapes differ by audience. A customer
// picking who washes their car needs an employee's name; they have no
// business with that employee's phone number, their hire date or what the
// company pays them per wash. A manager needs all of it. Two types, built by
// hand from the same model, is how that stays true — widening one shared
// type to satisfy a new screen is exactly how a field ends up somewhere it
// was never meant to go.
//
// Nothing here imports the service or repository packages, so a response can
// never accidentally trigger a query.
package view

import (
	"time"

	"github.com/eandstravel/carwash/internal/models"
)

// ── Identity ──────────────────────────────────────────────────────────────

// Me is the caller's own profile. Safe to include contact details: they are
// the caller's own.
type Me struct {
	ID    string      `json:"id"`
	Role  models.Role `json:"role"`
	Name  string      `json:"name"`
	Email string      `json:"email"`
	Phone string      `json:"phone,omitempty"`
	// HomeLocationID is present only for employees.
	HomeLocationID string `json:"home_location_id,omitempty"`
}

func MeOf(u *models.User) Me {
	m := Me{
		ID:    u.ID.Hex(),
		Role:  u.Role,
		Name:  u.Name,
		Email: u.Email,
		Phone: u.Phone,
	}
	if u.Employee != nil {
		m.HomeLocationID = u.Employee.HomeLocationID.Hex()
	}
	return m
}

// Session is a successful login or registration.
type Session struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	User      Me        `json:"user"`
}

// EmployeeCard is an employee as a CUSTOMER sees them: enough to choose a
// person, and nothing more. No email, no phone, no hire date, no bonus.
type EmployeeCard struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func EmployeeCardOf(u *models.User) EmployeeCard {
	return EmployeeCard{ID: u.ID.Hex(), Name: u.Name}
}

// StaffMember is a person as a MANAGER sees them — the full personnel record.
type StaffMember struct {
	ID             string            `json:"id"`
	Role           models.Role       `json:"role"`
	Name           string            `json:"name"`
	Email          string            `json:"email"`
	Phone          string            `json:"phone,omitempty"`
	Status         models.UserStatus `json:"status"`
	HomeLocationID string            `json:"home_location_id,omitempty"`
	HiredAt        *time.Time        `json:"hired_at,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
}

func StaffMemberOf(u *models.User) StaffMember {
	s := StaffMember{
		ID:        u.ID.Hex(),
		Role:      u.Role,
		Name:      u.Name,
		Email:     u.Email,
		Phone:     u.Phone,
		Status:    u.Status,
		CreatedAt: u.CreatedAt,
	}
	if u.Employee != nil {
		s.HomeLocationID = u.Employee.HomeLocationID.Hex()
		hired := u.Employee.HiredAt
		s.HiredAt = &hired
	}
	return s
}

func StaffMembers(us []*models.User) []StaffMember {
	out := make([]StaffMember, 0, len(us))
	for _, u := range us {
		out = append(out, StaffMemberOf(u))
	}
	return out
}

// ── Catalogue ─────────────────────────────────────────────────────────────

// Location as anyone may see it. The coordinate is public because the app
// needs to show the site on a map and to measure against it for clock-in.
type Location struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Address         string  `json:"address,omitempty"`
	Lat             float64 `json:"lat"`
	Lng             float64 `json:"lng"`
	GeofenceRadiusM float64 `json:"geofence_radius_m"`
	Active          bool    `json:"active"`
}

func LocationOf(l *models.Location) Location {
	return Location{
		ID:              l.ID.Hex(),
		Name:            l.Name,
		Address:         l.Address,
		Lat:             l.Point.Lat,
		Lng:             l.Point.Lng,
		GeofenceRadiusM: l.GeofenceRadiusM,
		Active:          l.Active,
	}
}

func Locations(ls []*models.Location) []Location {
	out := make([]Location, 0, len(ls))
	for _, l := range ls {
		out = append(out, LocationOf(l))
	}
	return out
}

// WashService as a CUSTOMER sees it: what it is, how long it takes, what it
// costs. The bonus is deliberately absent — what the company pays its staff
// per job is not part of a price list.
type WashService struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	DurationMin int        `json:"duration_min"`
	PriceMNT    models.MNT `json:"price_mnt"`
	Active      bool       `json:"active"`
}

func WashServiceOf(s *models.WashService) WashService {
	return WashService{
		ID:          s.ID.Hex(),
		Name:        s.Name,
		Description: s.Description,
		DurationMin: s.DurationMin,
		PriceMNT:    s.PriceMNT,
		Active:      s.Active,
	}
}

func WashServices(ss []*models.WashService) []WashService {
	out := make([]WashService, 0, len(ss))
	for _, s := range ss {
		out = append(out, WashServiceOf(s))
	}
	return out
}

// StaffWashService adds the bonus, for the manager's price-list screen and
// for an employee checking what a job is worth to them.
type StaffWashService struct {
	WashService
	BonusMNT models.MNT `json:"bonus_mnt"`
}

func StaffWashServiceOf(s *models.WashService) StaffWashService {
	return StaffWashService{WashService: WashServiceOf(s), BonusMNT: s.BonusMNT}
}

func StaffWashServices(ss []*models.WashService) []StaffWashService {
	out := make([]StaffWashService, 0, len(ss))
	for _, s := range ss {
		out = append(out, StaffWashServiceOf(s))
	}
	return out
}

// ── Cars ──────────────────────────────────────────────────────────────────

// Car as its owner sees it, notes included.
type Car struct {
	ID        string    `json:"id"`
	Plate     string    `json:"plate"`
	Make      string    `json:"make,omitempty"`
	Model     string    `json:"model,omitempty"`
	Color     string    `json:"color,omitempty"`
	Notes     string    `json:"notes,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func CarOf(c *models.Car) Car {
	return Car{
		ID:        c.ID.Hex(),
		Plate:     c.Plate,
		Make:      c.Make,
		Model:     c.Model,
		Color:     c.Color,
		Notes:     c.Notes,
		CreatedAt: c.CreatedAt,
	}
}

func Cars(cs []*models.Car) []Car {
	out := make([]Car, 0, len(cs))
	for _, c := range cs {
		out = append(out, CarOf(c))
	}
	return out
}

// CarBrief is the vehicle on a job card — what staff need in order to find
// it in the queue. The owner's free-text notes are left out: they are a note
// to self, not an instruction to the washer.
type CarBrief struct {
	ID    string `json:"id"`
	Plate string `json:"plate"`
	Make  string `json:"make,omitempty"`
	Model string `json:"model,omitempty"`
	Color string `json:"color,omitempty"`
}

func CarBriefOf(c *models.Car) CarBrief {
	if c == nil {
		return CarBrief{}
	}
	return CarBrief{
		ID:    c.ID.Hex(),
		Plate: c.Plate,
		Make:  c.Make,
		Model: c.Model,
		Color: c.Color,
	}
}
