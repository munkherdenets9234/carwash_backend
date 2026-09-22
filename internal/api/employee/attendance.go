package employee

import (
	"time"

	"github.com/eandstravel/carwash/internal/api/apictx"
	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/internal/service"
	"github.com/eandstravel/carwash/internal/view"
	"github.com/eandstravel/carwash/pkg/response"
	"github.com/gin-gonic/gin"
)

type attendanceController struct {
	attendance *service.AttendanceService
	resolver   *service.Resolver
	loc        *time.Location
}

type clockInRequest struct {
	LocationID string  `json:"location_id"`
	Lat        float64 `json:"lat"`
	Lng        float64 `json:"lng"`
}

// ClockIn opens a time entry if the caller is inside the site's geofence.
//
// A refusal comes back as 422 OUTSIDE_GEOFENCE with the measured distance in
// the message, which is a different thing for the app to render than a
// validation error: "you are 240 m away, move closer" rather than "check
// your input".
func (h *attendanceController) ClockIn(c *gin.Context) error {
	var req clockInRequest
	if err := apictx.Bind(c, &req); err != nil {
		return err
	}

	entry, err := h.attendance.ClockIn(c.Request.Context(), apictx.UserID(c), req.LocationID, req.Lat, req.Lng)
	if err != nil {
		return err
	}

	rendered, err := h.render(c, entry)
	if err != nil {
		return err
	}
	response.Created(c, rendered)
	return nil
}

type clockOutRequest struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// ClockOut closes the running entry. The coordinates are recorded but do not
// gate the call — see AttendanceService.ClockOut for why.
func (h *attendanceController) ClockOut(c *gin.Context) error {
	var req clockOutRequest
	if err := apictx.Bind(c, &req); err != nil {
		return err
	}

	entry, err := h.attendance.ClockOut(c.Request.Context(), apictx.UserID(c), req.Lat, req.Lng)
	if err != nil {
		return err
	}

	rendered, err := h.render(c, entry)
	if err != nil {
		return err
	}
	response.OK(c, rendered)
	return nil
}

// Current answers "am I clocked in".
//
// Not clocked in is 200 with a null body, not 404. It is an ordinary state
// the app renders a button for, and making the normal case an error status
// means every client has to special-case it.
func (h *attendanceController) Current(c *gin.Context) error {
	entry, err := h.attendance.Current(c.Request.Context(), apictx.UserID(c))
	if err != nil {
		return err
	}
	if entry == nil {
		response.OK(c, nil)
		return nil
	}

	rendered, err := h.render(c, entry)
	if err != nil {
		return err
	}
	response.OK(c, rendered)
	return nil
}

// Timesheet returns the caller's own entries for a window.
func (h *attendanceController) Timesheet(c *gin.Context) error {
	from, to, err := apictx.DateRange(c, h.loc)
	if err != nil {
		return err
	}
	me := apictx.UserID(c)

	entries, err := h.attendance.Timesheet(c.Request.Context(), repository.TimeEntryQuery{
		EmployeeID: &me,
		From:       from,
		To:         to,
	})
	if err != nil {
		return err
	}

	resolved, err := h.resolver.ForTimeEntries(c.Request.Context(), entries)
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

func (h *attendanceController) render(c *gin.Context, e *models.TimeEntry) (view.TimeEntry, error) {
	resolved, err := h.resolver.ForTimeEntries(c.Request.Context(), []*models.TimeEntry{e})
	if err != nil {
		return view.TimeEntry{}, err
	}
	return view.TimeEntryOf(e, view.NewLookup(resolved.Users, nil, nil, resolved.Locations)), nil
}
