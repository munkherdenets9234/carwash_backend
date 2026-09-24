package public

import (
	"strings"

	"github.com/eandstravel/carwash/internal/api/apictx"
	"github.com/eandstravel/carwash/internal/service"
	"github.com/eandstravel/carwash/internal/view"
	"github.com/eandstravel/carwash/pkg/response"
	"github.com/gin-gonic/gin"
)

type authController struct {
	svc *service.AuthService
	// reservations is here only to verify a booking reference during signup.
	// It is the same call the public lookup makes, deliberately.
	reservations *service.ReservationService
}

type registerRequest struct {
	// Reference is optional: the code from a booking made as a guest. With
	// the phone number it proves that those bookings belong to the person
	// signing up, and the account is created ON their existing customer
	// record so the history comes with it.
	Reference string `json:"reference"`

	Name     string `json:"name"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	Password string `json:"password"`
}

// Register creates a CUSTOMER account and logs it straight in.
//
// Note what it cannot do: the role is not a field on the request. An
// endpoint that takes a role from the body is one typo in a validation
// branch away from letting anyone mint a manager.
func (h *authController) Register(c *gin.Context) error {
	var req registerRequest
	if err := apictx.Bind(c, &req); err != nil {
		return err
	}

	in := service.RegisterInput{
		Name:     req.Name,
		Email:    req.Email,
		Phone:    req.Phone,
		Password: req.Password,
	}

	// A booking reference on a signup means "I have booked here before, as a
	// guest, and these are my bookings". Proving that is the SAME check the
	// public lookup makes — the code and the phone number together — and it
	// is made by calling that path rather than by a second implementation
	// here. Two implementations of one security check is how they diverge.
	//
	// The reference is optional. Somebody who has never booked signs up with
	// nothing extra; somebody who has, and leaves it out, is told what it is
	// for rather than being silently given an empty account.
	if strings.TrimSpace(req.Reference) != "" {
		res, err := h.reservations.LookupByReference(c.Request.Context(), apictx.TenantID(c), req.Reference, req.Phone)
		if err != nil {
			// Passed through unchanged, which means a wrong code and a right
			// code with the wrong number are still the same 404. Signing up
			// must not become the oracle that the lookup refuses to be.
			return err
		}
		in.ClaimCustomerID = &res.CustomerID
	}

	session, err := h.svc.RegisterCustomer(c.Request.Context(), apictx.TenantID(c), in)
	if err != nil {
		return err
	}

	response.Created(c, sessionView(session))
	return nil
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *authController) Login(c *gin.Context) error {
	var req loginRequest
	if err := apictx.Bind(c, &req); err != nil {
		return err
	}

	session, err := h.svc.Login(c.Request.Context(), apictx.TenantID(c), req.Email, req.Password)
	if err != nil {
		return err
	}

	response.OK(c, sessionView(session))
	return nil
}

func sessionView(s *service.Session) view.Session {
	return view.Session{
		Token:     s.Token,
		ExpiresAt: s.ExpiresAt,
		User:      view.MeOf(s.User),
	}
}
