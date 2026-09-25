package manager

import (
	"time"

	"github.com/eandstravel/carwash/internal/api/apictx"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/internal/service"
	"github.com/eandstravel/carwash/internal/view"
	"github.com/eandstravel/carwash/pkg/response"
	"github.com/gin-gonic/gin"
)

type timesheetController struct {
	attendance *service.AttendanceService
	resolver   *service.Resolver
	loc        *time.Location
}

// List is the same timesheet an employee sees of themselves, across
// everybody, and filterable by person or site.
//
// It is the same view type as the employee's own, deliberately: a manager
// checking a disputed shift should be looking at exactly the record the
// employee is looking at, including the clock-in distance. A separate
// manager-only shape here would be the version that drifts.
func (h *timesheetController) List(c *gin.Context) error {
	from, to, err := apictx.DateRange(c, h.loc)
	if err != nil {
		return err
	}
	employeeID, err := apictx.OptionalIDQuery(c, "employee_id")
	if err != nil {
		return err
	}
	locationID, err := apictx.OptionalIDQuery(c, "location_id")
	if err != nil {
		return err
	}

	entries, err := h.attendance.Timesheet(c.Request.Context(), apictx.TenantID(c), repository.TimeEntryQuery{
		EmployeeID: employeeID,
		LocationID: locationID,
		From:       from,
		To:         to,
	})
	if err != nil {
		return err
	}

	resolved, err := h.resolver.ForTimeEntries(c.Request.Context(), apictx.TenantID(c), entries)
	if err != nil {
		return err
	}
	lookup := view.NewLookup(resolved.Users, nil, nil, resolved.Locations)
	sum := service.SummarizeTimesheet(entries)

	response.OK(c, view.Timesheet{
		From:          from,
		To:            to,
		Entries:       view.TimeEntries(entries, lookup),
		TotalEntries:  sum.Entries,
		WorkedMinutes: sum.WorkedMinutes,
		WorkedHours:   view.HoursOf(sum.WorkedMinutes),
		OpenEntries:   sum.OpenEntries,
	})
	return nil
}
