package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/cloudinary/cloudinary-go/v2"
	"github.com/cloudinary/cloudinary-go/v2/api/uploader"
	"github.com/eandstravel/carwash/internal/entitlement"
	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/pkg/apierr"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// allowedImageTypes is the closed set this service accepts.
//
// The type is decided by sniffing the bytes, never by the Content-Type the
// client sent. A client-declared type is a claim about a file, not a fact
// about it, and the two differ exactly when it matters.
//
// SVG is deliberately absent: it is a script-carrying document, and
// http.DetectContentType reports it as text/xml rather than an image type
// anyway, so accepting it would mean trusting the declared type for the one
// format where that is least safe.
var allowedImageTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

// MediaService stores photographs at an image host and records them.
type MediaService struct {
	repo     *repository.MediaRepo
	cld      *cloudinary.Cloudinary
	maxBytes int64
}

// NewMediaService returns a service with no image host when cloudinaryURL is
// blank.
//
// A nil host is the configured-off state, not an error. Uploads are one
// feature among many, and refusing to start the whole service because an
// image host was not configured would be a worse outage than the one it
// prevents — bookings, the roster and attendance have nothing to do with
// photographs. The upload route stays mounted and answers 503
// FEATURE_UNAVAILABLE, so a client can tell a missing deployment setting from
// a bad request.
func NewMediaService(repo *repository.MediaRepo, cloudinaryURL string, maxBytes int64) (*MediaService, error) {
	s := &MediaService{repo: repo, maxBytes: maxBytes}
	if s.maxBytes < 1 {
		s.maxBytes = 10 << 20
	}
	if cloudinaryURL == "" {
		return s, nil
	}
	cld, err := cloudinary.NewFromURL(cloudinaryURL)
	if err != nil {
		return nil, fmt.Errorf("cloudinary init: %w", err)
	}
	s.cld = cld
	return s, nil
}

// UploadsEnabled reports whether an image host is configured.
func (s *MediaService) UploadsEnabled() bool { return s != nil && s.cld != nil }

// MediaInput is everything about a photograph except the bytes.
type MediaInput struct {
	Role    models.MediaRole
	AltMN   string
	AltEN   string
	Tag     string
	Feature bool
}

// Upload stores a photograph and records it against the tenant.
//
// SECURITY: the destination folder is derived entirely from the resolved
// tenant, never from the request. A caller choosing its own storage path is
// the same class of mistake as a caller choosing its own filename — one
// business could write into another's folder by typing a different value —
// and the fix is the same, which is to stop asking. The object name is a
// random UUID for the same reason.
func (s *MediaService) Upload(
	ctx context.Context,
	tenantID primitive.ObjectID,
	ent entitlement.Entitlement,
	file io.Reader,
	in MediaInput,
) (*models.Media, error) {
	if !s.UploadsEnabled() {
		return nil, apierr.FeatureUnavailable("image uploads")
	}
	if !in.Role.Valid() {
		return nil, apierr.ValidationFailed("role must be hero, about or gallery").In(apierr.DomainCatalog)
	}
	if in.Role == models.MediaRoleGallery && in.Tag != "" && !models.ValidGalleryTag(in.Tag) {
		return nil, apierr.ValidationFailed("unknown gallery tag: " + in.Tag).In(apierr.DomainCatalog)
	}
	// Alt text is required, not optional. These are photographs of work, and
	// the caption is the only description of what the picture shows for
	// somebody who cannot see it. A field that may be left blank is a field
	// that is always left blank.
	if in.AltMN == "" && in.AltEN == "" {
		return nil, apierr.ValidationFailed("a description is required in at least one language").In(apierr.DomainCatalog)
	}

	if err := s.checkLimit(ctx, tenantID, ent, in.Role); err != nil {
		return nil, err
	}

	// Sniff before streaming. http.DetectContentType reads at most 512
	// bytes, which are then put back in front of the stream so the upload
	// still sees the whole file.
	head := make([]byte, 512)
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, apierr.BadRequest("could not read the file")
	}
	head = head[:n]
	if n == 0 {
		return nil, apierr.BadRequest("the file is empty")
	}
	if !allowedImageTypes[stripCharset(http.DetectContentType(head))] {
		return nil, apierr.ValidationFailed("only jpeg, png, gif and webp images are accepted").In(apierr.DomainCatalog)
	}

	// Cap the stream rather than trusting Content-Length, which is a header
	// and therefore also a claim. One byte over the limit is read on purpose
	// so "exactly at the limit" and "over it" stay distinguishable.
	body := io.MultiReader(bytes.NewReader(head), io.LimitReader(file, s.maxBytes+1-int64(n)))
	counted := &countingReader{r: body}

	res, err := s.cld.Upload.Upload(ctx, counted, uploader.UploadParams{
		Folder:   "carwash/" + tenantID.Hex(),
		PublicID: uuid.NewString(),
	})
	if err != nil {
		return nil, apierr.Upstream(apierr.DomainCatalog, err)
	}
	if counted.n > s.maxBytes {
		// The object is already at the host; remove it rather than leaving a
		// file no row points at.
		_, _ = s.cld.Upload.Destroy(ctx, uploader.DestroyParams{PublicID: res.PublicID})
		return nil, apierr.ValidationFailed(fmt.Sprintf("the image exceeds the %d byte limit", s.maxBytes)).In(apierr.DomainCatalog)
	}

	m := &models.Media{
		TenantID: tenantID,
		Role:     in.Role,
		URL:      res.SecureURL,
		PublicID: res.PublicID,
		// Taken from the host's response, not asked of the uploader: these
		// are what stop the page shifting as each photograph loads, and a
		// number a person types is the one nobody checks.
		Width:   res.Width,
		Height:  res.Height,
		Alt:     map[string]string{"mn": in.AltMN, "en": in.AltEN},
		Tag:     in.Tag,
		Feature: in.Feature,
		Active:  true,
	}

	if err := s.repo.Create(ctx, m); err != nil {
		// The bytes are already stored. Leaving them there with no row is an
		// orphan nobody will find, so undo the upload before reporting.
		_, _ = s.cld.Upload.Destroy(ctx, uploader.DestroyParams{PublicID: res.PublicID})
		return nil, apierr.Internal(err)
	}

	if in.Role.Singular() {
		if err := s.repo.DemoteRole(ctx, tenantID, in.Role, m.ID); err != nil {
			return nil, apierr.Internal(err)
		}
	}
	return m, nil
}

// checkLimit enforces the plan's ceiling on how many photographs a tenant may
// hold.
//
// In the service, at the write, because middleware sees a request and not a
// row count. The key is this product's own name for what is being counted;
// the platform stores the number and does not know what it counts.
func (s *MediaService) checkLimit(ctx context.Context, tenantID primitive.ObjectID, ent entitlement.Entitlement, role models.MediaRole) error {
	// Only the gallery is capped. Capping the two single-slot images would
	// mean a plan could forbid a business from having a home page cover,
	// which is not a tier — it is a broken site.
	if role != models.MediaRoleGallery {
		return nil
	}
	const key = "carwash.gallery_images"
	limit, ok := ent.Limit(key)
	if !ok {
		// Absent means unlimited, not zero. A plan that never mentioned
		// gallery images and one that grants none are different situations,
		// and collapsing them ships the one where a missing key forbids
		// everything.
		return nil
	}
	current, err := s.repo.CountForRole(ctx, tenantID, role)
	if err != nil {
		return apierr.Internal(err)
	}
	if !ent.Within(key, int(current)) {
		return apierr.LimitExceeded("gallery images", limit)
	}
	return nil
}

// List returns a tenant's photographs in display order.
func (s *MediaService) List(ctx context.Context, tenantID primitive.ObjectID, q repository.MediaQuery) ([]*models.Media, error) {
	out, err := s.repo.List(ctx, tenantID, q)
	if err != nil {
		return nil, apierr.Internal(err)
	}
	return out, nil
}

// MediaPatch is the set of fields a manager may change after upload. The
// bytes are not among them: replacing an image is a new upload and a delete,
// so a URL somebody has already linked to never silently becomes a different
// picture.
type MediaPatch struct {
	Role      *models.MediaRole
	AltMN     *string
	AltEN     *string
	Tag       *string
	Feature   *bool
	Active    *bool
	SortOrder *int
}

func (s *MediaService) Update(ctx context.Context, tenantID primitive.ObjectID, idStr string, p MediaPatch) error {
	id, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		return apierr.BadRequest("invalid image id").In(apierr.DomainCatalog)
	}

	existing, err := s.repo.FindByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return apierr.NotFound("image").In(apierr.DomainCatalog)
		}
		return apierr.Internal(err)
	}

	set := bson.M{}
	if p.Role != nil {
		if !p.Role.Valid() {
			return apierr.ValidationFailed("role must be hero, about or gallery").In(apierr.DomainCatalog)
		}
		set["role"] = *p.Role
	}
	if p.Tag != nil {
		if *p.Tag != "" && !models.ValidGalleryTag(*p.Tag) {
			return apierr.ValidationFailed("unknown gallery tag: " + *p.Tag).In(apierr.DomainCatalog)
		}
		set["tag"] = *p.Tag
	}
	if p.AltMN != nil || p.AltEN != nil {
		alt := existing.Alt
		if alt == nil {
			alt = map[string]string{}
		}
		if p.AltMN != nil {
			alt["mn"] = *p.AltMN
		}
		if p.AltEN != nil {
			alt["en"] = *p.AltEN
		}
		if alt["mn"] == "" && alt["en"] == "" {
			return apierr.ValidationFailed("a description is required in at least one language").In(apierr.DomainCatalog)
		}
		set["alt"] = alt
	}
	if p.Feature != nil {
		set["feature"] = *p.Feature
	}
	if p.Active != nil {
		set["active"] = *p.Active
	}
	if p.SortOrder != nil {
		set["sort_order"] = *p.SortOrder
	}
	if len(set) == 0 {
		return apierr.BadRequest("nothing to change")
	}

	if err := s.repo.Update(ctx, tenantID, id, set); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return apierr.NotFound("image").In(apierr.DomainCatalog)
		}
		return apierr.Internal(err)
	}

	// Promoting into a single-slot role demotes whoever held it.
	if p.Role != nil && p.Role.Singular() {
		if err := s.repo.DemoteRole(ctx, tenantID, *p.Role, id); err != nil {
			return apierr.Internal(err)
		}
	}
	return nil
}

// Delete removes the record and then the file.
//
// The row goes first. If the host call fails afterwards the result is one
// orphaned object, which costs a little storage and is invisible; doing it
// the other way round risks a row pointing at a file that is already gone,
// which is a broken image on a customer's screen.
func (s *MediaService) Delete(ctx context.Context, tenantID primitive.ObjectID, idStr string) error {
	id, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		return apierr.BadRequest("invalid image id").In(apierr.DomainCatalog)
	}

	m, err := s.repo.FindByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return apierr.NotFound("image").In(apierr.DomainCatalog)
		}
		return apierr.Internal(err)
	}

	if err := s.repo.Delete(ctx, tenantID, id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return apierr.NotFound("image").In(apierr.DomainCatalog)
		}
		return apierr.Internal(err)
	}

	if s.UploadsEnabled() && m.PublicID != "" {
		_, _ = s.cld.Upload.Destroy(ctx, uploader.DestroyParams{PublicID: m.PublicID})
	}
	return nil
}

// stripCharset reduces "text/plain; charset=utf-8" to "text/plain".
func stripCharset(ct string) string {
	for i := 0; i < len(ct); i++ {
		if ct[i] == ';' {
			return ct[:i]
		}
	}
	return ct
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
