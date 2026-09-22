package customer

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

type reservationsController struct {
	reservations *service.ReservationService
	schedule     *service.ScheduleService
	staff        *service.StaffService
	resolver     *service.Resolver
	loc          *time.Location
}

// ListEmployees returns the washers a customer may choose from.
//
// Rendered as view.EmployeeCard — id and name only. The same people are
// returned to a manager as view.StaffMember, with email, phone, hire date
// and status. Two types rather than one with fields omitted: the shape a
// customer receives should not be one conditional away from the shape
// personnel records have.
func (h *reservationsController) ListEmployees(c *gin.Context) error {
	employees, err := h.staff.ListEmployees(c.Request.Context())
	if err != nil {
		return err
	}

	cards := make([]view.EmployeeCard, 0, len(employees))
	for _, e := range employees {
		// A suspended employee is not a choice. Filtered here rather than
		// in the query so the manager's listing, which must show them, uses
		// the same service call.
		if e.Status != models.UserActive {
			continue
		}
		cards = append(cards, view.EmployeeCardOf(e))
	}

	response.OK(c, cards)
	return nil
}

// Availability answers "who can do this service on this day, and when".
func (h *reservationsController) Availability(c *gin.Context) error {
	serviceID, err := apictx.RequiredIDQuery(c, "service_id")
	if err != nil {
		return err
	}
	locationID, err := apictx.OptionalIDQuery(c, "location_id")
	if err != nil {
		return err
	}
	employeeID, err := apictx.OptionalIDQuery(c, "employee_id")
	if err != nil {
		return err
	}
	day, err := apictx.Day(c, "day", h.loc)
	if err != nil {
		return err
	}

	found, err := h.schedule.Availability(c.Request.Context(), service.AvailabilityQuery{
		Day:        day,
		ServiceID:  serviceID,
		LocationID: locationID,
		EmployeeID: employeeID,
	})
	if err != nil {
		return err
	}

	out := make([]view.EmployeeAvailability, 0, len(found))
	for _, ea := range found {
		slots := make([]view.Slot, 0, len(ea.Slots))
		for _, s := range ea.Slots {
			slots = append(slots, view.Slot{
				StartAt:    s.Start,
				EndAt:      s.End,
				LocationID: s.LocationID.Hex(),
			})
		}
		out = append(out, view.EmployeeAvailability{
			Employee: view.EmployeeCardOf(ea.Employee),
			Slots:    slots,
		})
	}

	response.OK(c, out)
	return nil
}

type bookRequest struct {
	EmployeeID string    `json:"employee_id"`
	CarID      string    `json:"car_id"`
	ServiceID  string    `json:"service_id"`
	LocationID string    `json:"location_id"`
	StartAt    time.Time `json:"start_at"`
	Notes      string    `json:"notes"`
}

// Create books a wash with a chosen employee.
func (h *reservationsController) Create(c *gin.Context) error {
	var req bookRequest
	if err := apictx.Bind(c, &req); err != nil {
		return err
	}
	if req.StartAt.IsZero() {
		return apierr.ValidationFailed("start_at is required, as an RFC 3339 timestamp")
	}

	res, err := h.reservations.Book(c.Request.Context(), apictx.UserID(c), service.BookInput{
		EmployeeID: req.EmployeeID,
		CarID:      req.CarID,
		ServiceID:  req.ServiceID,
		LocationID: req.LocationID,
		StartAt:    req.StartAt,
		Notes:      req.Notes,
	})
	if err != nil {
		return err
	}

	single, err := h.render(c, res)
	if err != nil {
		return err
	}
	response.Created(c, single)
	return nil
}

// List returns the caller's own bookings in a window.
func (h *reservationsController) List(c *gin.Context) error {
	from, to, err := apictx.DateRange(c, h.loc)
	if err != nil {
		return err
	}
	me := apictx.UserID(c)

	rows, err := h.reservations.List(c.Request.Context(), repository.ReservationQuery{
		// Scoped to the caller in the query. There is no customer_id
		// parameter on this route, so there is nothing to tamper with.
		CustomerID: &me,
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

	response.OK(c, view.Reservations(rows, lookup))
	return nil
}

func (h *reservationsController) Get(c *gin.Context) error {
	id, err := apictx.IDParam(c, "id")
	if err != nil {
		return err
	}
	res, err := h.reservations.GetForCustomer(c.Request.Context(), id, apictx.UserID(c))
	if err != nil {
		return err
	}
	single, err := h.render(c, res)
	if err != nil {
		return err
	}
	response.OK(c, single)
	return nil
}

func (h *reservationsController) Cancel(c *gin.Context) error {
	id, err := apictx.IDParam(c, "id")
	if err != nil {
		return err
	}
	if err := h.reservations.CancelByCustomer(c.Request.Context(), id, apictx.UserID(c)); err != nil {
		return err
	}

	res, err := h.reservations.GetForCustomer(c.Request.Context(), id, apictx.UserID(c))
	if err != nil {
		return err
	}
	single, err := h.render(c, res)
	if err != nil {
		return err
	}
	response.OK(c, single)
	return nil
}

func (h *reservationsController) render(c *gin.Context, res *models.Reservation) (view.Reservation, error) {
	resolved, err := h.resolver.ForReservations(c.Request.Context(), []*models.Reservation{res})
	if err != nil {
		return view.Reservation{}, err
	}
	lookup := view.NewLookup(resolved.Users, resolved.Cars, resolved.Services, resolved.Locations)
	return view.ReservationOf(res, lookup), nil
}
