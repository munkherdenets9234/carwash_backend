package manager

import (
	"github.com/eandstravel/carwash/internal/api/apictx"
	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/service"
	"github.com/eandstravel/carwash/internal/view"
	"github.com/eandstravel/carwash/pkg/response"
	"github.com/gin-gonic/gin"
)

type staffController struct {
	svc *service.StaffService
}

type createStaffRequest struct {
	Role           models.Role `json:"role"`
	Name           string      `json:"name"`
	Email          string      `json:"email"`
	Phone          string      `json:"phone"`
	Password       string      `json:"password"`
	HomeLocationID string      `json:"home_location_id"`
}

// Create adds an employee or another manager.
//
// The role IS a field here, unlike on public registration, because the
// caller is already a manager. The service still refuses "customer": a
// customer account created by someone else, with a password that person
// chose, is not an account that customer agreed to.
func (h *staffController) Create(c *gin.Context) error {
	var req createStaffRequest
	if err := apictx.Bind(c, &req); err != nil {
		return err
	}

	u, err := h.svc.Create(c.Request.Context(), service.NewStaffInput{
		Role:           req.Role,
		Name:           req.Name,
		Email:          req.Email,
		Phone:          req.Phone,
		Password:       req.Password,
		HomeLocationID: req.HomeLocationID,
	})
	if err != nil {
		return err
	}

	response.Created(c, view.StaffMemberOf(u))
	return nil
}

// List returns managers and employees together — the personnel list.
func (h *staffController) List(c *gin.Context) error {
	managers, err := h.svc.ListManagers(c.Request.Context())
	if err != nil {
		return err
	}
	employees, err := h.svc.ListEmployees(c.Request.Context())
	if err != nil {
		return err
	}
	response.OK(c, view.StaffMembers(append(managers, employees...)))
	return nil
}

// ListCustomers is the one route that returns a list of customers with their
// contact details. Manager-only, and rendered as StaffMember rather than
// through any type a customer route also uses.
func (h *staffController) ListCustomers(c *gin.Context) error {
	customers, err := h.svc.ListCustomers(c.Request.Context())
	if err != nil {
		return err
	}
	response.OK(c, view.StaffMembers(customers))
	return nil
}

type setStatusRequest struct {
	Status models.UserStatus `json:"status"`
}

func (h *staffController) SetStatus(c *gin.Context) error {
	var req setStatusRequest
	if err := apictx.Bind(c, &req); err != nil {
		return err
	}

	// The acting manager's id is passed so the service can refuse a
	// manager suspending themselves.
	err := h.svc.SetStatus(c.Request.Context(), apictx.UserID(c).Hex(), c.Param("id"), req.Status)
	if err != nil {
		return err
	}

	response.NoContent(c)
	return nil
}
