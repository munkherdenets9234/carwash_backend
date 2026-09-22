package view

import (
	"time"

	"github.com/eandstravel/carwash/internal/models"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Lookup carries the rows a list of reservations refers to, fetched once by
// the controller instead of once per row. Any map may be nil; every read
// goes through the helpers below, which return a zero value rather than
// panicking, so a reference to something since deleted renders as a blank
// field instead of a 500.
type Lookup struct {
	Employees map[primitive.ObjectID]*models.User
	Customers map[primitive.ObjectID]*models.User
	Cars      map[primitive.ObjectID]*models.Car
	Services  map[primitive.ObjectID]*models.WashService
	Locations map[primitive.ObjectID]*models.Location
}

// NewLookup builds a Lookup from the maps a resolver produced. Employees and
// customers come from the same users map: they are the same collection, and
// which role a given id holds is already decided by where it appeared.
func NewLookup(
	users map[primitive.ObjectID]*models.User,
	cars map[primitive.ObjectID]*models.Car,
	services map[primitive.ObjectID]*models.WashService,
	locations map[primitive.ObjectID]*models.Location,
) Lookup {
	return Lookup{
		Employees: users,
		Customers: users,
		Cars:      cars,
		Services:  services,
		Locations: locations,
	}
}

func (l Lookup) employee(id primitive.ObjectID) *models.User     { return l.Employees[id] }
func (l Lookup) customer(id primitive.ObjectID) *models.User     { return l.Customers[id] }
func (l Lookup) car(id primitive.ObjectID) *models.Car           { return l.Cars[id] }
func (l Lookup) location(id primitive.ObjectID) *models.Location { return l.Locations[id] }

func (l Lookup) service(id primitive.ObjectID) *models.WashService { return l.Services[id] }

func nameOf(u *models.User) string {
	if u == nil {
		return ""
	}
	return u.Name
}

// NamedRef is an id with the label a UI shows for it, so a client never has
// to hold a second map just to render a list.
type NamedRef struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// ── Customer-facing ───────────────────────────────────────────────────────

// Reservation is a booking as its CUSTOMER sees it.
//
// BonusMNT is absent, and that is the point of this type existing separately
// from StaffReservation. What the company pays the washer for this job is
// between the company and the washer; putting it in the customer's response
// invites the conversation nobody wants to have at the counter.
type Reservation struct {
	ID       string                   `json:"id"`
	Status   models.ReservationStatus `json:"status"`
	StartAt  time.Time                `json:"start_at"`
	EndAt    time.Time                `json:"end_at"`
	Service  NamedRef                 `json:"service"`
	Employee NamedRef                 `json:"employee"`
	Location NamedRef                 `json:"location"`
	Car      CarBrief                 `json:"car"`
	PriceMNT models.MNT               `json:"price_mnt"`
	Notes    string                   `json:"notes,omitempty"`

	CompletedAt *time.Time `json:"completed_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

func ReservationOf(r *models.Reservation, l Lookup) Reservation {
	out := Reservation{
		ID:          r.ID.Hex(),
		Status:      r.Status,
		StartAt:     r.StartAt,
		EndAt:       r.EndAt,
		Employee:    NamedRef{ID: r.EmployeeID.Hex(), Name: nameOf(l.employee(r.EmployeeID))},
		Location:    NamedRef{ID: r.LocationID.Hex()},
		Service:     NamedRef{ID: r.ServiceID.Hex()},
		Car:         CarBriefOf(l.car(r.CarID)),
		PriceMNT:    r.PriceMNT,
		Notes:       r.Notes,
		CompletedAt: r.CompletedAt,
		CreatedAt:   r.CreatedAt,
	}
	if svc := l.service(r.ServiceID); svc != nil {
		out.Service.Name = svc.Name
	}
	if loc := l.location(r.LocationID); loc != nil {
		out.Location.Name = loc.Name
	}
	return out
}

func Reservations(rs []*models.Reservation, l Lookup) []Reservation {
	out := make([]Reservation, 0, len(rs))
	for _, r := range rs {
		out = append(out, ReservationOf(r, l))
	}
	return out
}

// ── Staff-facing ──────────────────────────────────────────────────────────

// StaffReservation is a booking as an EMPLOYEE or MANAGER sees it: the same
// job, plus what it pays and who to call.
type StaffReservation struct {
	Reservation
	BonusMNT models.MNT `json:"bonus_mnt"`
	// Customer is here because staff need to reach the person whose car it
	// is. It is the reason this type is never returned on a customer route:
	// it carries another customer's phone number.
	Customer StaffCustomer `json:"customer"`
}

// StaffCustomer is the contact detail on a job card.
type StaffCustomer struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Phone string `json:"phone,omitempty"`
}

func StaffReservationOf(r *models.Reservation, l Lookup) StaffReservation {
	out := StaffReservation{
		Reservation: ReservationOf(r, l),
		BonusMNT:    r.BonusMNT,
		Customer:    StaffCustomer{ID: r.CustomerID.Hex()},
	}
	if c := l.customer(r.CustomerID); c != nil {
		out.Customer.Name = c.Name
		out.Customer.Phone = c.Phone
	}
	return out
}

func StaffReservations(rs []*models.Reservation, l Lookup) []StaffReservation {
	out := make([]StaffReservation, 0, len(rs))
	for _, r := range rs {
		out = append(out, StaffReservationOf(r, l))
	}
	return out
}

// ── Availability ──────────────────────────────────────────────────────────

// Slot is one bookable window.
type Slot struct {
	StartAt    time.Time `json:"start_at"`
	EndAt      time.Time `json:"end_at"`
	LocationID string    `json:"location_id"`
}

// EmployeeAvailability is one employee and when they are free.
//
// Slots is always an array, never null, even when the person is fully
// booked: "working, nothing left" and "not in today" are different answers,
// and the second is simply absent from the list.
type EmployeeAvailability struct {
	Employee EmployeeCard `json:"employee"`
	Slots    []Slot       `json:"slots"`
}

// ── Roster ────────────────────────────────────────────────────────────────

type Shift struct {
	ID       string    `json:"id"`
	Employee NamedRef  `json:"employee"`
	Location NamedRef  `json:"location"`
	StartAt  time.Time `json:"start_at"`
	EndAt    time.Time `json:"end_at"`
}

func ShiftOf(s *models.Shift, l Lookup) Shift {
	out := Shift{
		ID:       s.ID.Hex(),
		Employee: NamedRef{ID: s.EmployeeID.Hex(), Name: nameOf(l.employee(s.EmployeeID))},
		Location: NamedRef{ID: s.LocationID.Hex()},
		StartAt:  s.StartAt,
		EndAt:    s.EndAt,
	}
	if loc := l.location(s.LocationID); loc != nil {
		out.Location.Name = loc.Name
	}
	return out
}

func Shifts(ss []*models.Shift, l Lookup) []Shift {
	out := make([]Shift, 0, len(ss))
	for _, s := range ss {
		out = append(out, ShiftOf(s, l))
	}
	return out
}

// ── Timesheet ─────────────────────────────────────────────────────────────

// TimeEntry is one row of a timesheet.
//
// The distances are included for both staff audiences. An employee seeing
// "you clocked in 12 m from the site" can tell a healthy record from a
// marginal one before a manager ever asks about it, and a manager reviewing
// a disputed shift has the evidence in the same row rather than in a log.
type TimeEntry struct {
	ID               string     `json:"id"`
	Employee         NamedRef   `json:"employee"`
	Location         NamedRef   `json:"location"`
	ClockInAt        time.Time  `json:"clock_in_at"`
	ClockInDistanceM float64    `json:"clock_in_distance_m"`
	ClockOutAt       *time.Time `json:"clock_out_at,omitempty"`
	// ClockOutDistanceM is -1 when the device gave no usable fix at
	// clock-out. Not 0, which would read as "standing on the site".
	ClockOutDistanceM *float64 `json:"clock_out_distance_m,omitempty"`
	WorkedMinutes     int      `json:"worked_minutes"`
	Running           bool     `json:"running"`
}

func TimeEntryOf(e *models.TimeEntry, l Lookup) TimeEntry {
	out := TimeEntry{
		ID:                e.ID.Hex(),
		Employee:          NamedRef{ID: e.EmployeeID.Hex(), Name: nameOf(l.employee(e.EmployeeID))},
		Location:          NamedRef{ID: e.LocationID.Hex()},
		ClockInAt:         e.ClockInAt,
		ClockInDistanceM:  e.ClockInDistanceM,
		ClockOutAt:        e.ClockOutAt,
		ClockOutDistanceM: e.ClockOutDistanceM,
		WorkedMinutes:     e.WorkedMinutes,
		Running:           e.Running(),
	}
	if loc := l.location(e.LocationID); loc != nil {
		out.Location.Name = loc.Name
	}
	return out
}

func TimeEntries(es []*models.TimeEntry, l Lookup) []TimeEntry {
	out := make([]TimeEntry, 0, len(es))
	for _, e := range es {
		out = append(out, TimeEntryOf(e, l))
	}
	return out
}

// Timesheet is a window of entries with its total.
type Timesheet struct {
	From          time.Time   `json:"from"`
	To            time.Time   `json:"to"`
	Entries       []TimeEntry `json:"entries"`
	TotalEntries  int         `json:"total_entries"`
	WorkedMinutes int         `json:"worked_minutes"`
	WorkedHours   float64     `json:"worked_hours"`
	OpenEntries   int         `json:"open_entries"`
}

// ── Daily report ──────────────────────────────────────────────────────────

type EmployeeDaily struct {
	EmployeeID    string     `json:"employee_id"`
	Name          string     `json:"name"`
	Washes        int        `json:"washes"`
	RevenueMNT    models.MNT `json:"revenue_mnt"`
	BonusMNT      models.MNT `json:"bonus_mnt"`
	WorkedMinutes int        `json:"worked_minutes"`
	WorkedHours   float64    `json:"worked_hours"`
}

type DailyReport struct {
	Day        string          `json:"day"`
	Washes     int             `json:"washes"`
	RevenueMNT models.MNT      `json:"revenue_mnt"`
	BonusMNT   models.MNT      `json:"bonus_mnt"`
	NetMNT     models.MNT      `json:"net_mnt"`
	Employees  []EmployeeDaily `json:"employees"`
}

// HoursOf renders minutes as hours to one decimal. Kept in one place so the
// report and the timesheet cannot round differently and appear to disagree.
func HoursOf(minutes int) float64 {
	return float64(minutes) / 60
}
