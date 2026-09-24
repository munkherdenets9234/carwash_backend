// Package manager holds the back office.
//
// This is the only audience that reads other people's data: staff records,
// customer contact details, every timesheet, and the money. One
// Auth(models.RoleManager) on the group is what keeps it that way, and it is
// applied once here rather than remembered in each of the twenty handlers
// below.
package manager

import (
	"time"

	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/service"
	"github.com/eandstravel/carwash/pkg/httpx"
	"github.com/gin-gonic/gin"
)

type RoleFunc func(roles ...models.Role) gin.HandlerFunc

type Deps struct {
	Auth RoleFunc

	// AuthRateLimit guards staff creation, which sets a password.
	AuthRateLimit gin.HandlerFunc

	Loc *time.Location

	Staff        *service.StaffService
	Catalog      *service.CatalogService
	Schedule     *service.ScheduleService
	Reservations *service.ReservationService
	Attendance   *service.AttendanceService
	Reports      *service.ReportService
	Resolver     *service.Resolver
	Media        *service.MediaService
}

func Register(base *gin.RouterGroup, d Deps) {
	staff := &staffController{svc: d.Staff}
	catalog := &catalogController{svc: d.Catalog}
	roster := &rosterController{schedule: d.Schedule, resolver: d.Resolver, loc: d.Loc}
	jobs := &jobsController{reservations: d.Reservations, resolver: d.Resolver, loc: d.Loc}
	sheets := &timesheetController{attendance: d.Attendance, resolver: d.Resolver, loc: d.Loc}
	reports := &reportController{reports: d.Reports, loc: d.Loc}
	media := &mediaController{svc: d.Media}

	g := httpx.Wrap(base.Group("", d.Auth(models.RoleManager)))

	// People.
	g.GET("/staff", staff.List)
	g.PUT("/staff/:id/status", staff.SetStatus)
	g.GET("/customers", staff.ListCustomers)
	httpx.Wrap(base.Group("", d.Auth(models.RoleManager), d.AuthRateLimit)).
		POST("/staff", staff.Create)

	// Price list and sites. These reads use the staff response type, which
	// includes the per-wash bonus and inactive rows — neither of which the
	// public catalogue returns.
	svc := g.Group("/services")
	svc.GET("", catalog.ListServices)
	svc.POST("", catalog.CreateService)
	svc.PUT("/:id", catalog.UpdateService)

	loc := g.Group("/locations")
	loc.GET("", catalog.ListLocations)
	loc.POST("", catalog.CreateLocation)
	loc.PUT("/:id", catalog.UpdateLocation)

	// Roster.
	sh := g.Group("/shifts")
	sh.GET("", roster.List)
	sh.POST("", roster.Create)
	sh.DELETE("/:id", roster.Delete)

	// Every booking, and the two things a manager does to one.
	r := g.Group("/reservations")
	r.GET("", jobs.List)
	r.PUT("/:id/assign", jobs.Assign)
	r.PUT("/:id/status", jobs.SetStatus)

	// The shopfront's photographs. Manager-only: a washer has no business
	// changing what the storefront looks like.
	md := g.Group("/media")
	md.GET("", media.List)
	md.POST("", media.Upload)
	md.PUT("/:id", media.Update)
	md.PUT("/order", media.Reorder)
	md.DELETE("/:id", media.Delete)

	g.GET("/timesheets", sheets.List)
	g.GET("/reports/daily", reports.Daily)
}
