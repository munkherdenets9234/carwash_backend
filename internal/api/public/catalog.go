package public

import (
	"github.com/eandstravel/carwash/internal/service"
	"github.com/eandstravel/carwash/internal/view"
	"github.com/eandstravel/carwash/pkg/response"
	"github.com/gin-gonic/gin"
)

type catalogController struct {
	svc *service.CatalogService
}

// ListServices returns the active price list.
//
// activeOnly is hard-coded true rather than taken from a query parameter:
// on this surface there is no caller entitled to see a service that has
// been withdrawn, and an ?active=false switch would hand one to anybody who
// guessed it. The manager's own listing is a different route with a
// different response type.
func (h *catalogController) ListServices(c *gin.Context) error {
	items, err := h.svc.ListWashServices(c.Request.Context(), true)
	if err != nil {
		return err
	}
	response.OK(c, view.WashServices(items))
	return nil
}

func (h *catalogController) ListLocations(c *gin.Context) error {
	items, err := h.svc.ListLocations(c.Request.Context(), true)
	if err != nil {
		return err
	}
	response.OK(c, view.Locations(items))
	return nil
}
