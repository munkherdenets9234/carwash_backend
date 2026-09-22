// Package employee holds the routes a washer uses on their own phone.
//
// Every route is scoped to the caller: the timesheet is their timesheet, the
// jobs are their jobs, and the clock-in is their own. None of them take an
// employee_id, so there is no parameter to point at a colleague.
package employee

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

	// AttendanceRateLimit guards clock-in and clock-out. They are cheap
	// individually and they are the endpoints somebody would hammer while
	// walking towards the site waiting for the fence to accept them.
	AttendanceRateLimit gin.HandlerFunc

	Loc *time.Location

	Attendance   *service.AttendanceService
	Schedule     *service.ScheduleService
	Reservations *service.ReservationService
	Resolver     *service.Resolver
}

func Register(base *gin.RouterGroup, d Deps) {
	attendance := &attendanceController{
		attendance: d.Attendance,
		resolver:   d.Resolver,
		loc:        d.Loc,
	}
	jobs := &jobsController{
		reservations: d.Reservations,
		schedule:     d.Schedule,
		resolver:     d.Resolver,
		loc:          d.Loc,
	}

	g := httpx.Wrap(base.Group("", d.Auth(models.RoleEmployee)))

	a := g.Group("/attendance")
	a.GET("/current", attendance.Current)
	limited := g.Group("/attendance", d.AttendanceRateLimit)
	limited.POST("/clock-in", attendance.ClockIn)
	limited.POST("/clock-out", attendance.ClockOut)

	g.GET("/timesheet", attendance.Timesheet)
	g.GET("/shifts", jobs.Shifts)

	j := g.Group("/jobs")
	j.GET("", jobs.List)
	j.PUT("/:id/status", jobs.SetStatus)
}
