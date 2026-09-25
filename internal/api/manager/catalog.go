package manager

import (
	"github.com/eandstravel/carwash/internal/api/apictx"
	"github.com/eandstravel/carwash/internal/service"
	"github.com/eandstravel/carwash/internal/view"
	"github.com/eandstravel/carwash/pkg/response"
	"github.com/gin-gonic/gin"
)

type catalogController struct {
	svc *service.CatalogService
}

// ── Services ──────────────────────────────────────────────────────────────

type washServiceRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	DurationMin int    `json:"duration_min"`
	PriceMNT    int64  `json:"price_mnt"`
	BonusMNT    int64  `json:"bonus_mnt"`
	Active      *bool  `json:"active"`
}

func (r washServiceRequest) input() service.WashServiceInput {
	return service.WashServiceInput{
		Name:        r.Name,
		Description: r.Description,
		DurationMin: r.DurationMin,
		PriceMNT:    r.PriceMNT,
		BonusMNT:    r.BonusMNT,
		Active:      r.Active,
	}
}

// ListServices returns the whole price list, withdrawn entries included: a
// manager's most likely reason to open this screen is to bring one back.
func (h *catalogController) ListServices(c *gin.Context) error {
	items, err := h.svc.ListWashServices(c.Request.Context(), apictx.TenantID(c), false)
	if err != nil {
		return err
	}
	response.OK(c, view.StaffWashServices(items))
	return nil
}

func (h *catalogController) CreateService(c *gin.Context) error {
	var req washServiceRequest
	if err := apictx.Bind(c, &req); err != nil {
		return err
	}
	ws, err := h.svc.CreateWashService(c.Request.Context(), apictx.TenantID(c), req.input())
	if err != nil {
		return err
	}
	response.Created(c, view.StaffWashServiceOf(ws))
	return nil
}

func (h *catalogController) UpdateService(c *gin.Context) error {
	id, err := apictx.IDParam(c, "id")
	if err != nil {
		return err
	}
	var req washServiceRequest
	if err := apictx.Bind(c, &req); err != nil {
		return err
	}
	if err := h.svc.UpdateWashService(c.Request.Context(), apictx.TenantID(c), id, req.input()); err != nil {
		return err
	}

	ws, err := h.svc.GetWashService(c.Request.Context(), apictx.TenantID(c), id)
	if err != nil {
		return err
	}
	response.OK(c, view.StaffWashServiceOf(ws))
	return nil
}

// ── Locations ─────────────────────────────────────────────────────────────

type locationRequest struct {
	Name            string  `json:"name"`
	Address         string  `json:"address"`
	Lat             float64 `json:"lat"`
	Lng             float64 `json:"lng"`
	GeofenceRadiusM float64 `json:"geofence_radius_m"`
	Active          *bool   `json:"active"`
}

func (r locationRequest) input() service.LocationInput {
	return service.LocationInput{
		Name:            r.Name,
		Address:         r.Address,
		Lat:             r.Lat,
		Lng:             r.Lng,
		GeofenceRadiusM: r.GeofenceRadiusM,
		Active:          r.Active,
	}
}

func (h *catalogController) ListLocations(c *gin.Context) error {
	items, err := h.svc.ListLocations(c.Request.Context(), apictx.TenantID(c), false)
	if err != nil {
		return err
	}
	response.OK(c, view.Locations(items))
	return nil
}

func (h *catalogController) CreateLocation(c *gin.Context) error {
	var req locationRequest
	if err := apictx.Bind(c, &req); err != nil {
		return err
	}
	l, err := h.svc.CreateLocation(c.Request.Context(), apictx.TenantID(c), req.input())
	if err != nil {
		return err
	}
	response.Created(c, view.LocationOf(l))
	return nil
}

func (h *catalogController) UpdateLocation(c *gin.Context) error {
	id, err := apictx.IDParam(c, "id")
	if err != nil {
		return err
	}
	var req locationRequest
	if err := apictx.Bind(c, &req); err != nil {
		return err
	}
	if err := h.svc.UpdateLocation(c.Request.Context(), apictx.TenantID(c), id, req.input()); err != nil {
		return err
	}

	l, err := h.svc.GetLocation(c.Request.Context(), apictx.TenantID(c), id)
	if err != nil {
		return err
	}
	response.OK(c, view.LocationOf(l))
	return nil
}
