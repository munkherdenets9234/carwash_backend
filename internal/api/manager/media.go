package manager

import (
	"strconv"

	"github.com/eandstravel/carwash/internal/api/apictx"
	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/internal/service"
	"github.com/eandstravel/carwash/internal/view"
	"github.com/eandstravel/carwash/pkg/apierr"
	"github.com/eandstravel/carwash/pkg/response"
	"github.com/gin-gonic/gin"
)

// mediaController is the back office's photograph management.
//
// Manager-only, like the rest of this package. A washer has no business
// changing what the shopfront looks like, and a customer certainly does not.
type mediaController struct {
	svc *service.MediaService
}

// maxUploadMemory is how much of a multipart body gin keeps in memory before
// spilling to a temp file. Deliberately small: the upload is streamed to the
// image host rather than buffered whole, so there is nothing to gain from
// holding a large photograph in RAM.
const maxUploadMemory = 4 << 20

// List returns every photograph the tenant has, active or not.
//
// Not active-only: the likeliest reason to open this screen is to bring a
// withdrawn photograph back, and a list that hides them makes that
// impossible.
func (h *mediaController) List(c *gin.Context) error {
	var q repository.MediaQuery
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

// Upload accepts one photograph.
//
// Everything about it except the bytes arrives as form fields alongside the
// file, rather than as a second "now describe it" request. A two-step upload
// leaves a described-by-nobody image behind every time somebody closes the
// tab, and the description is the only thing a screen reader has.
func (h *mediaController) Upload(c *gin.Context) error {
	if !h.svc.UploadsEnabled() {
		return apierr.FeatureUnavailable("image uploads")
	}

	if err := c.Request.ParseMultipartForm(maxUploadMemory); err != nil {
		return apierr.BadRequest("expected a multipart form with a file field")
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		return apierr.BadRequest("a file is required")
	}
	file, err := fileHeader.Open()
	if err != nil {
		return apierr.BadRequest("could not read the file")
	}
	defer file.Close()

	role := models.MediaRole(c.PostForm("role"))
	if role == "" {
		role = models.MediaRoleGallery
	}

	m, err := h.svc.Upload(c.Request.Context(), apictx.TenantID(c), apictx.Entitlement(c), file, service.MediaInput{
		Role:    role,
		AltMN:   c.PostForm("alt_mn"),
		AltEN:   c.PostForm("alt_en"),
		Tag:     c.PostForm("tag"),
		Feature: c.PostForm("feature") == "true",
	})
	if err != nil {
		return err
	}
	response.Created(c, view.MediaOf(m))
	return nil
}

// Update changes a photograph's description, placement or visibility.
//
// The bytes are not editable. Replacing an image is an upload and a delete,
// so a URL a customer has already loaded never silently becomes a different
// picture.
func (h *mediaController) Update(c *gin.Context) error {
	var body struct {
		Role      *string `json:"role"`
		AltMN     *string `json:"alt_mn"`
		AltEN     *string `json:"alt_en"`
		Tag       *string `json:"tag"`
		Feature   *bool   `json:"feature"`
		Active    *bool   `json:"active"`
		SortOrder *int    `json:"sort_order"`
	}
	if err := apictx.Bind(c, &body); err != nil {
		return err
	}

	patch := service.MediaPatch{
		AltMN:     body.AltMN,
		AltEN:     body.AltEN,
		Tag:       body.Tag,
		Feature:   body.Feature,
		Active:    body.Active,
		SortOrder: body.SortOrder,
	}
	if body.Role != nil {
		role := models.MediaRole(*body.Role)
		patch.Role = &role
	}

	if err := h.svc.Update(c.Request.Context(), apictx.TenantID(c), c.Param("id"), patch); err != nil {
		return err
	}
	response.NoContent(c)
	return nil
}

// Reorder sets the display order of several photographs in one request.
//
// One call rather than one per tile: dragging a photograph to the front of a
// twelve-image gallery renumbers most of them, and twelve requests racing
// each other is how a grid ends up in an order nobody chose.
func (h *mediaController) Reorder(c *gin.Context) error {
	var body struct {
		// IDs in the order they should appear.
		IDs []string `json:"ids" binding:"required"`
	}
	if err := apictx.Bind(c, &body); err != nil {
		return err
	}
	if len(body.IDs) == 0 {
		return apierr.ValidationFailed("ids must not be empty")
	}

	tenantID := apictx.TenantID(c)
	for i, id := range body.IDs {
		order := i
		if err := h.svc.Update(c.Request.Context(), tenantID, id, service.MediaPatch{SortOrder: &order}); err != nil {
			// Reported with the position, because "image 7 of 12 is not
			// yours" is actionable and "not found" is not.
			return apierr.BadRequest("could not order image at position " + strconv.Itoa(i+1))
		}
	}
	response.NoContent(c)
	return nil
}

func (h *mediaController) Delete(c *gin.Context) error {
	if err := h.svc.Delete(c.Request.Context(), apictx.TenantID(c), c.Param("id")); err != nil {
		return err
	}
	response.NoContent(c)
	return nil
}
