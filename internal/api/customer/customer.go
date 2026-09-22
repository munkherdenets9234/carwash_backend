// Package customer holds the routes a signed-in customer may use.
//
// Every group here is mounted behind Auth(models.RoleCustomer), applied to
// the group rather than checked inside a handler, so a route added to this
// package is scoped to customers by construction. There is no per-handler
// check to forget.
package customer

import (
	"time"

	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/service"
	"github.com/eandstravel/carwash/pkg/httpx"
	"github.com/gin-gonic/gin"
)

// RoleFunc builds the auth middleware for the given roles. Supplied by the
// caller so this package cannot choose its own authentication.
type RoleFunc func(roles ...models.Role) gin.HandlerFunc

type Deps struct {
	Auth RoleFunc

	// BookingRateLimit guards the writes that create rows.
	BookingRateLimit gin.HandlerFunc

	// Loc is the business timezone, used to read ?day=YYYY-MM-DD as the
	// calendar day the customer means rather than the server's.
	Loc *time.Location

	Cars         *service.CarService
	Schedule     *service.ScheduleService
	Reservations *service.ReservationService
	Staff        *service.StaffService
	Resolver     *service.Resolver
}

// Register mounts the customer surface on base.
func Register(base *gin.RouterGroup, d Deps) {
	cars := &carsController{svc: d.Cars}
	bookings := &reservationsController{
		reservations: d.Reservations,
		schedule:     d.Schedule,
		staff:        d.Staff,
		resolver:     d.Resolver,
		loc:          d.Loc,
	}

	g := httpx.Wrap(base.Group("", d.Auth(models.RoleCustomer)))

	// A customer's own garage.
	c := g.Group("/cars")
	c.GET("", cars.List)
	c.GET("/:id", cars.Get)
	c.PUT("/:id", cars.Update)
	c.DELETE("/:id", cars.Delete)

	// Registering a car and booking a wash both write a row per call, so
	// both sit behind the booking limiter.
	limited := g.Group("", d.BookingRateLimit)
	limited.POST("/cars", cars.Create)
	limited.POST("/reservations", bookings.Create)

	// Choosing who washes your car needs a list of who there is, and when
	// each of them is free.
	g.GET("/employees", bookings.ListEmployees)
	g.GET("/availability", bookings.Availability)

	r := g.Group("/reservations")
	r.GET("", bookings.List)
	r.GET("/:id", bookings.Get)
	r.POST("/:id/cancel", bookings.Cancel)
}
