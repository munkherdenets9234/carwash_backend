package service

import (
	"context"
	"errors"
	"strings"

	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/pkg/apierr"
	"github.com/eandstravel/carwash/pkg/geo"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// CatalogService owns the reference data the booking flow depends on:
// where the washes happen and what they cost.
type CatalogService struct {
	locations *repository.LocationRepo
	services  *repository.WashServiceRepo
}

func NewCatalogService(locations *repository.LocationRepo, services *repository.WashServiceRepo) *CatalogService {
	return &CatalogService{locations: locations, services: services}
}

// ── Locations ─────────────────────────────────────────────────────────────

// minGeofenceRadiusM is a floor on how tight a geofence may be set.
//
// Consumer GPS is routinely 10–20 m out and worse beside a building. A 5 m
// radius would not be a strict policy, it would be a clock-in that fails at
// random for people standing in the right place — and the support answer
// ("try again") teaches everyone to distrust the system.
const minGeofenceRadiusM = 25

// maxGeofenceRadiusM keeps the check meaningful. Past a kilometre the fence
// no longer says "at work", it says "in the district".
const maxGeofenceRadiusM = 1000

type LocationInput struct {
	Name            string
	Address         string
	Lat             float64
	Lng             float64
	GeofenceRadiusM float64
	Active          *bool
}

func (s *CatalogService) CreateLocation(ctx context.Context, tenantID primitive.ObjectID, in LocationInput) (*models.Location, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, apierr.ValidationFailed("name is required")
	}
	if !geo.ValidCoordinate(in.Lat, in.Lng) {
		return nil, apierr.ValidationFailed("lat/lng must be a real coordinate")
	}
	if in.GeofenceRadiusM < minGeofenceRadiusM || in.GeofenceRadiusM > maxGeofenceRadiusM {
		return nil, apierr.ValidationFailed("geofence_radius_m must be between 25 and 1000")
	}

	active := true
	if in.Active != nil {
		active = *in.Active
	}
	l := &models.Location{
		TenantID:        tenantID,
		Name:            strings.TrimSpace(in.Name),
		Address:         strings.TrimSpace(in.Address),
		Point:           models.GeoPoint{Lat: in.Lat, Lng: in.Lng},
		GeofenceRadiusM: in.GeofenceRadiusM,
		Active:          active,
	}
	if err := s.locations.Create(ctx, l); err != nil {
		return nil, apierr.Internal(err)
	}
	return l, nil
}

func (s *CatalogService) ListLocations(ctx context.Context, tenantID primitive.ObjectID, activeOnly bool) ([]*models.Location, error) {
	out, err := s.locations.List(ctx, tenantID, activeOnly)
	if err != nil {
		return nil, apierr.Internal(err)
	}
	return out, nil
}

func (s *CatalogService) GetLocation(ctx context.Context, tenantID primitive.ObjectID, id primitive.ObjectID) (*models.Location, error) {
	l, err := s.locations.FindByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apierr.NotFound("location").In(apierr.DomainCatalog)
		}
		return nil, apierr.Internal(err)
	}
	return l, nil
}

func (s *CatalogService) UpdateLocation(ctx context.Context, tenantID primitive.ObjectID, id primitive.ObjectID, in LocationInput) error {
	set := bson.M{}
	if strings.TrimSpace(in.Name) != "" {
		set["name"] = strings.TrimSpace(in.Name)
	}
	if strings.TrimSpace(in.Address) != "" {
		set["address"] = strings.TrimSpace(in.Address)
	}
	if in.Lat != 0 || in.Lng != 0 {
		if !geo.ValidCoordinate(in.Lat, in.Lng) {
			return apierr.ValidationFailed("lat/lng must be a real coordinate")
		}
		set["point"] = models.GeoPoint{Lat: in.Lat, Lng: in.Lng}
	}
	if in.GeofenceRadiusM != 0 {
		if in.GeofenceRadiusM < minGeofenceRadiusM || in.GeofenceRadiusM > maxGeofenceRadiusM {
			return apierr.ValidationFailed("geofence_radius_m must be between 25 and 1000")
		}
		set["geofence_radius_m"] = in.GeofenceRadiusM
	}
	if in.Active != nil {
		set["active"] = *in.Active
	}
	if len(set) == 0 {
		return apierr.ValidationFailed("no fields to update")
	}

	if err := s.locations.Update(ctx, tenantID, id, set); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return apierr.NotFound("location").In(apierr.DomainCatalog)
		}
		return apierr.Internal(err)
	}
	return nil
}

// ── Wash services ─────────────────────────────────────────────────────────

type WashServiceInput struct {
	Name        string
	Description string
	DurationMin int
	PriceMNT    models.MNT
	BonusMNT    models.MNT
	Active      *bool
}

func (s *CatalogService) CreateWashService(ctx context.Context, tenantID primitive.ObjectID, in WashServiceInput) (*models.WashService, error) {
	if err := validateWashService(in, true); err != nil {
		return nil, err
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	ws := &models.WashService{
		TenantID:    tenantID,
		Name:        strings.TrimSpace(in.Name),
		Description: strings.TrimSpace(in.Description),
		DurationMin: in.DurationMin,
		PriceMNT:    in.PriceMNT,
		BonusMNT:    in.BonusMNT,
		Active:      active,
	}
	if err := s.services.Create(ctx, ws); err != nil {
		return nil, apierr.Internal(err)
	}
	return ws, nil
}

func (s *CatalogService) ListWashServices(ctx context.Context, tenantID primitive.ObjectID, activeOnly bool) ([]*models.WashService, error) {
	out, err := s.services.List(ctx, tenantID, activeOnly)
	if err != nil {
		return nil, apierr.Internal(err)
	}
	return out, nil
}

func (s *CatalogService) GetWashService(ctx context.Context, tenantID primitive.ObjectID, id primitive.ObjectID) (*models.WashService, error) {
	ws, err := s.services.FindByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apierr.NotFound("service").In(apierr.DomainCatalog)
		}
		return nil, apierr.Internal(err)
	}
	return ws, nil
}

func (s *CatalogService) UpdateWashService(ctx context.Context, tenantID primitive.ObjectID, id primitive.ObjectID, in WashServiceInput) error {
	if err := validateWashService(in, false); err != nil {
		return err
	}
	set := bson.M{}
	if strings.TrimSpace(in.Name) != "" {
		set["name"] = strings.TrimSpace(in.Name)
	}
	if strings.TrimSpace(in.Description) != "" {
		set["description"] = strings.TrimSpace(in.Description)
	}
	if in.DurationMin != 0 {
		set["duration_min"] = in.DurationMin
	}
	if in.PriceMNT != 0 {
		set["price_mnt"] = in.PriceMNT
	}
	if in.BonusMNT != 0 {
		set["bonus_mnt"] = in.BonusMNT
	}
	if in.Active != nil {
		set["active"] = *in.Active
	}
	if len(set) == 0 {
		return apierr.ValidationFailed("no fields to update")
	}

	// Worth being explicit about what this does NOT do: changing a price
	// here leaves every existing reservation untouched, because each one
	// copied the price it was booked at. Yesterday's report will not move.
	if err := s.services.Update(ctx, tenantID, id, set); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return apierr.NotFound("service").In(apierr.DomainCatalog)
		}
		return apierr.Internal(err)
	}
	return nil
}

func validateWashService(in WashServiceInput, creating bool) error {
	if creating && strings.TrimSpace(in.Name) == "" {
		return apierr.ValidationFailed("name is required")
	}
	if creating && in.DurationMin <= 0 {
		return apierr.ValidationFailed("duration_min must be greater than zero")
	}
	if in.DurationMin < 0 || in.DurationMin > 8*60 {
		return apierr.ValidationFailed("duration_min must be between 1 and 480")
	}
	if creating && in.PriceMNT <= 0 {
		return apierr.ValidationFailed("price_mnt must be greater than zero")
	}
	if in.PriceMNT < 0 || in.BonusMNT < 0 {
		return apierr.ValidationFailed("amounts cannot be negative")
	}
	// A bonus above the price means every completed wash loses money. Almost
	// certainly a typo — a missing or extra zero — and catching it here is
	// cheaper than finding it in a month of payroll.
	if in.PriceMNT > 0 && in.BonusMNT > in.PriceMNT {
		return apierr.ValidationFailed("bonus_mnt cannot exceed price_mnt")
	}
	return nil
}
