package service

import (
	"context"
	"errors"
	"strings"

	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/pkg/apierr"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// CarService is the customer's garage. Every method takes the owner's id and
// passes it into the query, so there is no path through this service that
// reads or writes a vehicle belonging to someone else.
type CarService struct {
	cars *repository.CarRepo
}

func NewCarService(cars *repository.CarRepo) *CarService {
	return &CarService{cars: cars}
}

type CarInput struct {
	Plate string
	Make  string
	Model string
	Color string
	Notes string
}

func (s *CarService) Register(ctx context.Context, ownerID primitive.ObjectID, in CarInput) (*models.Car, error) {
	plate := repository.NormalizePlate(in.Plate)
	if plate == "" {
		return nil, apierr.ValidationFailed("plate is required")
	}
	if len(plate) > 16 {
		return nil, apierr.ValidationFailed("plate is too long")
	}

	c := &models.Car{
		OwnerID: ownerID,
		Plate:   plate,
		Make:    strings.TrimSpace(in.Make),
		Model:   strings.TrimSpace(in.Model),
		Color:   strings.TrimSpace(in.Color),
		Notes:   strings.TrimSpace(in.Notes),
	}
	if err := s.cars.Create(ctx, c); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			return nil, apierr.Conflict("you have already registered that plate")
		}
		return nil, apierr.Internal(err)
	}
	return c, nil
}

func (s *CarService) ListMine(ctx context.Context, ownerID primitive.ObjectID) ([]*models.Car, error) {
	out, err := s.cars.ListByOwner(ctx, ownerID)
	if err != nil {
		return nil, apierr.Internal(err)
	}
	return out, nil
}

func (s *CarService) GetMine(ctx context.Context, id, ownerID primitive.ObjectID) (*models.Car, error) {
	c, err := s.cars.FindByIDForOwner(ctx, id, ownerID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// 404 rather than 403, for a car that exists but belongs to
			// someone else. A 403 would confirm the id is real, which turns
			// id enumeration into a census of registered vehicles.
			return nil, apierr.NotFound("car")
		}
		return nil, apierr.Internal(err)
	}
	return c, nil
}

func (s *CarService) Update(ctx context.Context, id, ownerID primitive.ObjectID, in CarInput) error {
	set := bson.M{}
	if strings.TrimSpace(in.Plate) != "" {
		set["plate"] = in.Plate
	}
	if strings.TrimSpace(in.Make) != "" {
		set["make"] = strings.TrimSpace(in.Make)
	}
	if strings.TrimSpace(in.Model) != "" {
		set["model"] = strings.TrimSpace(in.Model)
	}
	if strings.TrimSpace(in.Color) != "" {
		set["color"] = strings.TrimSpace(in.Color)
	}
	if strings.TrimSpace(in.Notes) != "" {
		set["notes"] = strings.TrimSpace(in.Notes)
	}
	if len(set) == 0 {
		return apierr.ValidationFailed("no fields to update")
	}

	if err := s.cars.Update(ctx, id, ownerID, set); err != nil {
		switch {
		case errors.Is(err, repository.ErrNotFound):
			return apierr.NotFound("car")
		case errors.Is(err, repository.ErrDuplicate):
			return apierr.Conflict("you have already registered that plate")
		default:
			return apierr.Internal(err)
		}
	}
	return nil
}

func (s *CarService) Delete(ctx context.Context, id, ownerID primitive.ObjectID) error {
	if err := s.cars.Delete(ctx, id, ownerID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return apierr.NotFound("car")
		}
		return apierr.Internal(err)
	}
	return nil
}
