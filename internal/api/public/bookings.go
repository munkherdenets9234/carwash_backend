package public

import (
	"time"

	"github.com/eandstravel/carwash/internal/api/apictx"
	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/service"
	"github.com/eandstravel/carwash/internal/view"
	"github.com/eandstravel/carwash/pkg/apierr"
	"github.com/eandstravel/carwash/pkg/response"
	"github.com/gin-gonic/gin"
)

// bookingsController is booking a wash without an account.
//
// Choosing a person and a time requires seeing which people there are and
// when each is free, so ListEmployees and Availability moved here from the
// customer surface. That reverses a decision this package's doc comment used
// to state, and the reasoning is written out there rather than quietly
// deleted.
type bookingsController struct {
	reservations *service.ReservationService
	schedule     *service.ScheduleService
	staff        *service.StaffService
	resolver     *service.Resolver
	loc          *time.Location
}

// ListEmployees names the people a visitor may choose between.
func (h *bookingsController) ListEmployees(c *gin.Context) error {
	employees, err := h.staff.ListEmployees(c.Request.Context(), apictx.TenantID(c))
	if err != nil {
		return err
	}

	cards := make([]view.EmployeeCard, 0, len(employees))
	for _, e := range employees {
		// A suspended employee is not a choice. Filtered here rather than in
		// the query so the manager's listing, which must show them, uses the
		// same service call.
		if e.Status != models.UserActive {
			continue
		}
		cards = append(cards, view.EmployeeCardOf(e))
	}

	response.OK(c, cards)
	return nil
}

// Availability answers "who can do this service on this day, and when".
func (h *bookingsController) Availability(c *gin.Context) error {
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

	found, err := h.schedule.Availability(c.Request.Context(), apictx.TenantID(c), service.AvailabilityQuery{
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

type guestBookingBody struct {
	Name  string `json:"name"`
	Phone string `json:"phone"`
	Plate string `json:"plate"`

	EmployeeID string    `json:"employee_id"`
	ServiceID  string    `json:"service_id"`
	LocationID string    `json:"location_id"`
	StartAt    time.Time `json:"start_at"`
	Notes      string    `json:"notes"`
}

// Create books a wash for somebody who has not registered.
//
// The response includes the reference code, and that is the only time it is
// ever sent. It is not recoverable from the phone number alone — if it were,
// the number would be enough to read the booking, and a number is not a
// secret. So the client has to show it to the customer and mean it.
func (h *bookingsController) Create(c *gin.Context) error {
	var body guestBookingBody
	if err := c.ShouldBindJSON(&body); err != nil {
		return apierr.BadRequest("that request body could not be read")
	}

	res, err := h.reservations.BookAsGuest(c.Request.Context(), apictx.TenantID(c), service.GuestBookInput{
		Name:       body.Name,
		Phone:      body.Phone,
		Plate:      body.Plate,
		EmployeeID: body.EmployeeID,
		ServiceID:  body.ServiceID,
		LocationID: body.LocationID,
		StartAt:    body.StartAt,
		Notes:      body.Notes,
	})
	if err != nil {
		return err
	}

	out, err := h.render(c, res)
	if err != nil {
		return err
	}
	response.Created(c, out)
	return nil
}

// Lookup returns one booking to somebody holding its reference and the phone
// number it was booked with.
//
// Both are required. Neither alone is enough, and the service gives the same
// "not found" for a bad code and for a good code with the wrong number, so
// this cannot be used to discover which codes exist.
func (h *bookingsController) Lookup(c *gin.Context) error {
	reference := c.Query("reference")
	phone := c.Query("phone")
	if reference == "" || phone == "" {
		return apierr.ValidationFailed("both the booking reference and the phone number are required")
	}

	res, err := h.reservations.LookupByReference(c.Request.Context(), apictx.TenantID(c), reference, phone)
	if err != nil {
		return err
	}

	out, err := h.render(c, res)
	if err != nil {
		return err
	}
	response.OK(c, out)
	return nil
}

// render resolves the ids on a booking into the names a person reads.
func (h *bookingsController) render(c *gin.Context, res *models.Reservation) (view.GuestBooking, error) {
	resolved, err := h.resolver.ForReservations(c.Request.Context(), apictx.TenantID(c), []*models.Reservation{res})
	if err != nil {
		return view.GuestBooking{}, err
	}
	lookup := view.NewLookup(resolved.Users, resolved.Cars, resolved.Services, resolved.Locations)
	return view.GuestBookingOf(res, lookup), nil
}
