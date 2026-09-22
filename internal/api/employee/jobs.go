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

type jobsController struct {
	reservations *service.ReservationService
	schedule     *service.ScheduleService
	resolver     *service.Resolver
	loc          *time.Location
}

// List returns the caller's own jobs for a window, as StaffReservation —
// with the bonus each one pays and the customer's number to call if they are
// late. Both are appropriate here and neither appears on the customer's own
// view of the same booking.
func (h *jobsController) List(c *gin.Context) error {
	from, to, err := apictx.DateRange(c, h.loc)
	if err != nil {
		return err
	}
	me := apictx.UserID(c)

	rows, err := h.reservations.List(c.Request.Context(), repository.ReservationQuery{
		EmployeeID: &me,
		From:       from,
		To:         to,
	})
	if err != nil {
		return err
	}

	resolved, err := h.resolver.ForReservations(c.Request.Context(), rows)
	if err != nil {
		return err
	}
	lookup := view.NewLookup(resolved.Users, resolved.Cars, resolved.Services, resolved.Locations)

	response.OK(c, view.StaffReservations(rows, lookup))
	return nil
}

type statusRequest struct {
	Status models.ReservationStatus `json:"status"`
}

// SetStatus moves one of the caller's own jobs along.
//
// The caller's id is passed to the service as the acting employee, which is
// what confines this to their own work: another employee's booking answers
// 404, the same as one that does not exist.
func (h *jobsController) SetStatus(c *gin.Context) error {
	id, err := apictx.IDParam(c, "id")
	if err != nil {
		return err
	}
	var req statusRequest
	if err := apictx.Bind(c, &req); err != nil {
		return err
	}

	me := apictx.UserID(c)
	if err := h.reservations.SetStatus(c.Request.Context(), id, req.Status, &me); err != nil {
		return err
	}

	res, err := h.reservations.Get(c.Request.Context(), id)
	if err != nil {
		return err
	}
	resolved, err := h.resolver.ForReservations(c.Request.Context(), []*models.Reservation{res})
	if err != nil {
		return err
	}
	lookup := view.NewLookup(resolved.Users, resolved.Cars, resolved.Services, resolved.Locations)

	response.OK(c, view.StaffReservationOf(res, lookup))
	return nil
}

// Shifts returns the caller's own roster for a window.
func (h *jobsController) Shifts(c *gin.Context) error {
	from, to, err := apictx.DateRange(c, h.loc)
	if err != nil {
		return err
	}
	me := apictx.UserID(c)

	rows, err := h.schedule.ListShifts(c.Request.Context(), repository.ShiftQuery{
		EmployeeID: &me,
		From:       from,
		To:         to,
	})
	if err != nil {
		return err
	}

	resolved, err := h.resolver.ForShifts(c.Request.Context(), rows)
	if err != nil {
		return err
	}
	lookup := view.NewLookup(resolved.Users, nil, nil, resolved.Locations)

	response.OK(c, view.Shifts(rows, lookup))
	return nil
}
