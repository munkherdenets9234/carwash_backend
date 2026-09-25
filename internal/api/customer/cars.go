package customer

import (
	"github.com/eandstravel/carwash/internal/api/apictx"
	"github.com/eandstravel/carwash/internal/service"
	"github.com/eandstravel/carwash/internal/view"
	"github.com/eandstravel/carwash/pkg/response"
	"github.com/gin-gonic/gin"
)

type carsController struct {
	svc *service.CarService
}

type carRequest struct {
	Plate string `json:"plate"`
	Make  string `json:"make"`
	Model string `json:"model"`
	Color string `json:"color"`
	Notes string `json:"notes"`
}

func (r carRequest) input() service.CarInput {
	return service.CarInput{Plate: r.Plate, Make: r.Make, Model: r.Model, Color: r.Color, Notes: r.Notes}
}

// Create registers a vehicle to the caller.
//
// The owner is the authenticated caller, never a field in the body. Taking
// an owner_id from the request would make this endpoint a way to attach a
// car to somebody else's account.
func (h *carsController) Create(c *gin.Context) error {
	var req carRequest
	if err := apictx.Bind(c, &req); err != nil {
		return err
	}

	car, err := h.svc.Register(c.Request.Context(), apictx.TenantID(c), apictx.UserID(c), req.input())
	if err != nil {
		return err
	}

	response.Created(c, view.CarOf(car))
	return nil
}

func (h *carsController) List(c *gin.Context) error {
	cars, err := h.svc.ListMine(c.Request.Context(), apictx.TenantID(c), apictx.UserID(c))
	if err != nil {
		return err
	}
	response.OK(c, view.Cars(cars))
	return nil
}

func (h *carsController) Get(c *gin.Context) error {
	id, err := apictx.IDParam(c, "id")
	if err != nil {
		return err
	}
	car, err := h.svc.GetMine(c.Request.Context(), apictx.TenantID(c), id, apictx.UserID(c))
	if err != nil {
		return err
	}
	response.OK(c, view.CarOf(car))
	return nil
}

func (h *carsController) Update(c *gin.Context) error {
	id, err := apictx.IDParam(c, "id")
	if err != nil {
		return err
	}
	var req carRequest
	if err := apictx.Bind(c, &req); err != nil {
		return err
	}
	if err := h.svc.Update(c.Request.Context(), apictx.TenantID(c), id, apictx.UserID(c), req.input()); err != nil {
		return err
	}

	car, err := h.svc.GetMine(c.Request.Context(), apictx.TenantID(c), id, apictx.UserID(c))
	if err != nil {
		return err
	}
	response.OK(c, view.CarOf(car))
	return nil
}

func (h *carsController) Delete(c *gin.Context) error {
	id, err := apictx.IDParam(c, "id")
	if err != nil {
		return err
	}
	if err := h.svc.Delete(c.Request.Context(), apictx.TenantID(c), id, apictx.UserID(c)); err != nil {
		return err
	}
	response.NoContent(c)
	return nil
}
