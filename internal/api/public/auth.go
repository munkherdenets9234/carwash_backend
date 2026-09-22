package public

import (
	"github.com/eandstravel/carwash/internal/api/apictx"
	"github.com/eandstravel/carwash/internal/service"
	"github.com/eandstravel/carwash/internal/view"
	"github.com/eandstravel/carwash/pkg/response"
	"github.com/gin-gonic/gin"
)

type authController struct {
	svc *service.AuthService
}

type registerRequest struct {
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

	session, err := h.svc.RegisterCustomer(c.Request.Context(), req.Name, req.Email, req.Phone, req.Password)
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

	session, err := h.svc.Login(c.Request.Context(), req.Email, req.Password)
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
