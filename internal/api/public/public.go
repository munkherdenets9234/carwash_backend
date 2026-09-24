// Package public holds the routes that need no credentials at all.
//
// What is in it is a decision, not a leftover, and one of those decisions
// was reversed. This comment used to say that availability lived on the
// customer surface and not here, because it names members of staff and says
// when each of them is working, and that a roster was not something an
// anonymous caller should page through.
//
// Booking without an account ended that position, because it cannot survive
// the requirement: choosing a person and a time means seeing which people
// there are and when each is free, and a customer who has not registered has
// no other way to be shown it. Keeping the old rule would have meant keeping
// registration, which is the thing the change was for.
//
// What it does NOT mean is that the exposure went unexamined. An anonymous
// caller sees a staff member's id and display name — view.EmployeeCard, not
// view.StaffMember, so no address, no telephone number, no employment status
// and no home site — together with free slots on a day they asked about for
// a service they named. That is what is written on the booking page of every
// business that takes appointments. The roster in full, who is on shift
// where and when, stays on the manager surface.
//
// The catalogue is the price list on the wall: no personal data, nothing an
// ordinary visitor could not read anyway.
package public

import (
	"time"

	"github.com/eandstravel/carwash/internal/service"
	"github.com/eandstravel/carwash/pkg/httpx"
	"github.com/gin-gonic/gin"
)

type Deps struct {
	Auth    *service.AuthService
	Catalog *service.CatalogService
	Media   *service.MediaService

	Schedule     *service.ScheduleService
	Reservations *service.ReservationService
	Staff        *service.StaffService
	Resolver     *service.Resolver

	// Loc is the business timezone, used to read ?day=YYYY-MM-DD as the
	// calendar day the visitor means rather than the server's.
	Loc *time.Location

	// AuthRateLimit guards the two endpoints that take a password.
	AuthRateLimit gin.HandlerFunc

	// BookingRateLimit guards the one unauthenticated route that WRITES.
	//
	// It matters more here than on the customer surface. A signed-in
	// customer creating nonsense bookings has an account to suspend; an
	// anonymous caller has nothing, so the limiter is the only thing between
	// a business's calendar and whoever decides to fill it.
	BookingRateLimit gin.HandlerFunc
}

// Register mounts the unauthenticated routes on base.
func Register(base *gin.RouterGroup, d Deps) {
	auth := &authController{svc: d.Auth, reservations: d.Reservations}
	catalog := &catalogController{svc: d.Catalog}
	media := &mediaController{svc: d.Media}
	bookings := &bookingsController{
		reservations: d.Reservations,
		schedule:     d.Schedule,
		staff:        d.Staff,
		resolver:     d.Resolver,
		loc:          d.Loc,
	}

	// Both of these tell an anonymous caller something about whether an
	// account exists — login by refusing, registration by conflicting — and
	// both hash a password on every call, which is CPU somebody else can
	// spend for free. Rate limited, not as a formality but because these
	// two are the reason the limiter exists.
	a := httpx.Wrap(base.Group("/auth", d.AuthRateLimit))
	a.POST("/register", auth.Register)
	a.POST("/login", auth.Login)

	g := httpx.Wrap(base.Group(""))
	g.GET("/services", catalog.ListServices)
	g.GET("/locations", catalog.ListLocations)

	// Whose car wash this is. The storefront needs a trading name to render
	// a header, and it has no token at that point — a visitor reading the
	// price list has not signed in. The API key already says which business;
	// this says what it is called.
	g.GET("/tenant", (&tenantController{}).Show)

	// The photographs the storefront renders. Public for the same reason the
	// price list is: a visitor has no token and the page has to draw.
	g.GET("/media", media.List)

	// Booking without an account.
	//
	// The two reads name staff and show when they are free, which is what a
	// person needs in order to choose. See the package comment for what that
	// does and does not expose.
	g.GET("/employees", bookings.ListEmployees)
	g.GET("/availability", bookings.Availability)

	// Looking a booking up needs the reference AND the phone it was booked
	// with. Rate limited despite being a read: it is the one route where
	// guessing has a prize, and without a limit the reference space can be
	// walked at whatever rate the network allows.
	g.Group("", d.BookingRateLimit).GET("/bookings/lookup", bookings.Lookup)

	// The write. Anonymous callers can create rows here, so the limiter is
	// not optional — see Deps.BookingRateLimit.
	g.Group("", d.BookingRateLimit).POST("/bookings", bookings.Create)
}
