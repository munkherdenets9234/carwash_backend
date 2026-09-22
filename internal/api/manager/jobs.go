package manager

import (
	"time"

	"github.com/eandstravel/carwash/internal/api/apictx"
	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/internal/service"
	"github.com/eandstravel/carwash/internal/view"
	"github.com/eandstravel/carwash/pkg/response"
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type jobsController struct {
	reservations *service.ReservationService
	resolver     *service.Resolver
	loc          *time.Location
}

// List is every booking in the window, filterable. Unlike the employee's
// own list this one takes employee_id, because seeing anyone's work is the
// manager's job.
func (h *jobsController) List(c *gin.Context) error {
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
	customerID, err := apictx.OptionalIDQuery(c, "customer_id")
	if err != nil {
		return err
	}

	q := repository.ReservationQuery{
		EmployeeID: employeeID,
		LocationID: locationID,
		CustomerID: customerID,
		From:       from,
		To:         to,
	}
	if raw := c.Query("status"); raw != "" {
		status := models.ReservationStatus(raw)
		q.Status = &status
	}

	rows, err := h.reservations.List(c.Request.Context(), q)
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

type assignRequest struct {
	EmployeeID string `json:"employee_id"`
}

// Assign hands an open job to a different washer.
//
// This is the point at which the bonus moves, so it is deliberately a
// manager-only route and deliberately refuses completed work: the person who
// did the job keeps what it paid.
func (h *jobsController) Assign(c *gin.Context) error {
	id, err := apictx.IDParam(c, "id")
	if err != nil {
		return err
	}
	var req assignRequest
	if err := apictx.Bind(c, &req); err != nil {
		return err
	}
	if err := h.reservations.Reassign(c.Request.Context(), id, req.EmployeeID); err != nil {
		return err
	}
	return h.respond(c, id)
}

type statusRequest struct {
	Status models.ReservationStatus `json:"status"`
}

// SetStatus moves any booking along. The acting-employee argument is nil: a
// manager is not confined to their own jobs.
func (h *jobsController) SetStatus(c *gin.Context) error {
	id, err := apictx.IDParam(c, "id")
	if err != nil {
		return err
	}
	var req statusRequest
	if err := apictx.Bind(c, &req); err != nil {
		return err
	}
	if err := h.reservations.SetStatus(c.Request.Context(), id, req.Status, nil); err != nil {
		return err
	}
	return h.respond(c, id)
}

func (h *jobsController) respond(c *gin.Context, id primitive.ObjectID) error {
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
