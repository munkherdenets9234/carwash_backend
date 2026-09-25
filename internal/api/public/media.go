package public

import (
	"github.com/eandstravel/carwash/internal/api/apictx"
	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/internal/service"
	"github.com/eandstravel/carwash/internal/view"
	"github.com/eandstravel/carwash/pkg/apierr"
	"github.com/eandstravel/carwash/pkg/response"
	"github.com/gin-gonic/gin"
)

// mediaController serves the photographs the storefront renders.
//
// Public, because the shopfront is. There is nothing private in a photograph
// a business chose to put on its own home page, and requiring a token to read
// one would mean the marketing site cannot render before anybody signs in —
// which is every visitor, every time.
type mediaController struct {
	svc *service.MediaService
}

// List returns the tenant's ACTIVE photographs, in display order.
//
// Active-only, unlike the manager's list. A photograph switched off is one
// the business has withdrawn; the back office still shows it so it can be
// brought back, and the shopfront must not.
func (h *mediaController) List(c *gin.Context) error {
	q := repository.MediaQuery{ActiveOnly: true}

	if raw := c.Query("role"); raw != "" {
		role := models.MediaRole(raw)
		if !role.Valid() {
			return apierr.BadRequest("role must be hero, about or gallery")
		}
		q.Role = &role
	}

	items, err := h.svc.List(c.Request.Context(), apictx.TenantID(c), q)
	if err != nil {
		return err
	}
	response.OK(c, view.MediaListOf(items))
	return nil
}
