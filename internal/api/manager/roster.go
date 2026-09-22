package manager

import (
	"time"

	"github.com/eandstravel/carwash/internal/api/apictx"
	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/internal/service"
	"github.com/eandstravel/carwash/internal/view"
	"github.com/eandstravel/carwash/pkg/apierr"
	"github.com/eandstravel/carwash/pkg/response"
	"github.com/gin-gonic/gin"
)

type rosterController struct {
	schedule *service.ScheduleService
	resolver *service.Resolver
	loc      *time.Location
}

type shiftRequest struct {
	EmployeeID string    `json:"employee_id"`
	LocationID string    `json:"location_id"`
	StartAt    time.Time `json:"start_at"`
	EndAt      time.Time `json:"end_at"`
}

func (h *rosterController) Create(c *gin.Context) error {
	var req shiftRequest
	if err := apictx.Bind(c, &req); err != nil {
		return err
	}
	if req.StartAt.IsZero() || req.EndAt.IsZero() {
		return apierr.ValidationFailed("start_at and end_at are required, as RFC 3339 timestamps")
	}

	sh, err := h.schedule.AddShift(c.Request.Context(), req.EmployeeID, req.LocationID, req.StartAt, req.EndAt)
	if err != nil {
		return err
	}

	resolved, err := h.resolver.ForShifts(c.Request.Context(), []*models.Shift{sh})
	if err != nil {
		return err
	}
	lookup := view.NewLookup(resolved.Users, nil, nil, resolved.Locations)

	response.Created(c, view.ShiftOf(sh, lookup))
	return nil
}

func (h *rosterController) List(c *gin.Context) error {
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

	rows, err := h.schedule.ListShifts(c.Request.Context(), repository.ShiftQuery{
		EmployeeID: employeeID,
		LocationID: locationID,
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

func (h *rosterController) Delete(c *gin.Context) error {
	id, err := apictx.IDParam(c, "id")
	if err != nil {
		return err
	}
	if err := h.schedule.DeleteShift(c.Request.Context(), id); err != nil {
		return err
	}
	response.NoContent(c)
	return nil
}
