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
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type jobsController struct {
	reservations *service.ReservationService
	resolver     *service.Resolver
	loc          *time.Location
}

// walkInRequest is the whole form at the desk.
//
// Four fields, three of them ids the screen already holds. There is no
// customer name, no telephone and no start time: a manager with a queue will
// not type them, and a field that gets skipped or filled with rubbish is
// worse than no field, because a report built on it looks true.
type walkInRequest struct {
	Plate      string `json:"plate" binding:"required"`
	EmployeeID string `json:"employee_id" binding:"required"`
	ServiceID  string `json:"service_id" binding:"required"`
	LocationID string `json:"location_id" binding:"required"`
	Notes      string `json:"notes"`
}

// RegisterWalkIn records a car that is on the forecourt now.
//
// Manager-only, and separate from the public booking route on purpose. That
// one is rate limited per IP — correctly, it is open to the internet — and a
// busy Saturday at one desk would trip the limiter from a single address. It
// also insists on a telephone number, which is the field this exists to
// avoid asking for.
func (h *jobsController) RegisterWalkIn(c *gin.Context) error {
	var req walkInRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		return apierr.BadRequest("plate, employee_id, service_id and location_id are all required")
	}

	res, err := h.reservations.RegisterWalkIn(c.Request.Context(), apictx.TenantID(c), service.WalkInInput{
		Plate:      req.Plate,
		EmployeeID: req.EmployeeID,
		ServiceID:  req.ServiceID,
		LocationID: req.LocationID,
		Notes:      req.Notes,
	})
	if err != nil {
		return err
	}

	resolved, err := h.resolver.ForReservations(c.Request.Context(), apictx.TenantID(c), []*models.Reservation{res})
	if err != nil {
		return err
	}
	lookup := view.NewLookup(resolved.Users, resolved.Cars, resolved.Services, resolved.Locations)

	response.Created(c, view.StaffReservationOf(res, lookup))
	return nil
}

// Show is one booking, with everything the manager may see on it.
//
// It exists so a detail screen can be reached by its own URL — from a phone
// list, from a link in a message, from a bookmark — rather than only by
// finding the row again in whichever day happened to be on screen.
func (h *jobsController) Show(c *gin.Context) error {
	id, err := apictx.IDParam(c, "id")
	if err != nil {
		return err
	}

	res, err := h.reservations.Get(c.Request.Context(), apictx.TenantID(c), id)
	if err != nil {
		return err
	}

	resolved, err := h.resolver.ForReservations(c.Request.Context(), apictx.TenantID(c), []*models.Reservation{res})
	if err != nil {
		return err
	}
	lookup := view.NewLookup(resolved.Users, resolved.Cars, resolved.Services, resolved.Locations)

	response.OK(c, view.StaffReservationOf(res, lookup))
	return nil
}

// editRequest carries only what the manager changed.
//
// Pointers, not values: a screen that edits the plate alone sends the plate
// alone, and an absent field is left as it is rather than being overwritten
// with a zero. With plain strings there is no way to tell "clear this" from
// "I did not touch it", and the second one is what every caller means.
type editRequest struct {
	Plate     *string    `json:"plate"`
	ServiceID *string    `json:"service_id"`
	StartAt   *time.Time `json:"start_at"`
}

// Edit corrects a booking that is already recorded.
func (h *jobsController) Edit(c *gin.Context) error {
	id, err := apictx.IDParam(c, "id")
	if err != nil {
		return err
	}

	var req editRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		return apierr.BadRequest("send at least one of plate, service_id or start_at")
	}

	res, err := h.reservations.EditReservation(c.Request.Context(), apictx.TenantID(c), id, service.EditReservationInput{
		Plate:     req.Plate,
		ServiceID: req.ServiceID,
		StartAt:   req.StartAt,
	})
	if err != nil {
		return err
	}

	resolved, err := h.resolver.ForReservations(c.Request.Context(), apictx.TenantID(c), []*models.Reservation{res})
	if err != nil {
		return err
	}
	lookup := view.NewLookup(resolved.Users, resolved.Cars, resolved.Services, resolved.Locations)

	response.OK(c, view.StaffReservationOf(res, lookup))
	return nil
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

	rows, err := h.reservations.List(c.Request.Context(), apictx.TenantID(c), q)
	if err != nil {
		return err
	}

	resolved, err := h.resolver.ForReservations(c.Request.Context(), apictx.TenantID(c), rows)
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
	if err := h.reservations.Reassign(c.Request.Context(), apictx.TenantID(c), id, req.EmployeeID); err != nil {
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
	if err := h.reservations.SetStatus(c.Request.Context(), apictx.TenantID(c), id, req.Status, nil); err != nil {
		return err
	}
	return h.respond(c, id)
}

func (h *jobsController) respond(c *gin.Context, id primitive.ObjectID) error {
	res, err := h.reservations.Get(c.Request.Context(), apictx.TenantID(c), id)
	if err != nil {
		return err
	}
	resolved, err := h.resolver.ForReservations(c.Request.Context(), apictx.TenantID(c), []*models.Reservation{res})
	if err != nil {
		return err
	}
	lookup := view.NewLookup(resolved.Users, resolved.Cars, resolved.Services, resolved.Locations)

	response.OK(c, view.StaffReservationOf(res, lookup))
	return nil
}
