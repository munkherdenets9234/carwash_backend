package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/pkg/apierr"
	"github.com/eandstravel/carwash/pkg/password"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"
)

// StaffService is the manager's view of people: creating employees and other
// managers, listing them, and suspending them.
type StaffService struct {
	users     *repository.UserRepo
	locations *repository.LocationRepo
	log       *zap.Logger
}

func NewStaffService(users *repository.UserRepo, locations *repository.LocationRepo, log *zap.Logger) *StaffService {
	return &StaffService{users: users, locations: locations, log: log}
}

// NewStaffInput is what a manager supplies to create a colleague.
type NewStaffInput struct {
	Role           models.Role
	Name           string
	Email          string
	Phone          string
	Password       string
	HomeLocationID string // employees only
}

func (s *StaffService) Create(ctx context.Context, tenantID primitive.ObjectID, in NewStaffInput) (*models.User, error) {
	if in.Role != models.RoleManager && in.Role != models.RoleEmployee {
		// Customers sign themselves up. Allowing a manager to mint one here
		// would create an account whose owner never agreed to a password
		// somebody else knows.
		return nil, apierr.ValidationFailed("role must be manager or employee")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, apierr.ValidationFailed("name is required")
	}
	if err := validateEmail(in.Email); err != nil {
		return nil, err
	}
	if len(in.Password) < minPasswordLen {
		return nil, apierr.ValidationFailed("password must be at least 8 characters")
	}

	u := &models.User{
		TenantID: tenantID,
		Role:     in.Role,
		Name:     name,
		Email:    in.Email,
		Phone:    strings.TrimSpace(in.Phone),
		Status:   models.UserActive,
	}

	if in.Role == models.RoleEmployee {
		if strings.TrimSpace(in.HomeLocationID) == "" {
			return nil, apierr.ValidationFailed("home_location_id is required for an employee")
		}
		locID, err := primitive.ObjectIDFromHex(in.HomeLocationID)
		if err != nil {
			return nil, apierr.BadRequest("home_location_id is not a valid id")
		}
		// Checked against the collection rather than trusted: an employee
		// rostered to a location that does not exist would pass every later
		// check and then fail at clock-in, far from the cause.
		if _, err := s.locations.FindByID(ctx, tenantID, locID); err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return nil, apierr.NotFound("location").In(apierr.DomainCatalog)
			}
			return nil, apierr.Internal(err)
		}
		u.Employee = &models.EmployeeProfile{HomeLocationID: locID, HiredAt: time.Now().UTC()}
	}

	hash, err := password.Hash(in.Password)
	if err != nil {
		return nil, apierr.Internal(err)
	}
	u.PasswordHash = hash

	if err := s.users.Create(ctx, u); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			return nil, apierr.Conflict("that email address is already registered").In(apierr.DomainAuth)
		}
		return nil, apierr.Internal(err)
	}
	return u, nil
}

// ListEmployees returns every employee, active or not.
func (s *StaffService) ListEmployees(ctx context.Context, tenantID primitive.ObjectID) ([]*models.User, error) {
	out, err := s.users.ListByRole(ctx, tenantID, models.RoleEmployee)
	if err != nil {
		return nil, apierr.Internal(err)
	}
	return out, nil
}

// ListManagers returns every manager.
func (s *StaffService) ListManagers(ctx context.Context, tenantID primitive.ObjectID) ([]*models.User, error) {
	out, err := s.users.ListByRole(ctx, tenantID, models.RoleManager)
	if err != nil {
		return nil, apierr.Internal(err)
	}
	return out, nil
}

// ListCustomers returns every customer. Manager-only: it is the one read in
// this service that returns other people's contact details.
func (s *StaffService) ListCustomers(ctx context.Context, tenantID primitive.ObjectID) ([]*models.User, error) {
	out, err := s.users.ListByRole(ctx, tenantID, models.RoleCustomer)
	if err != nil {
		return nil, apierr.Internal(err)
	}
	return out, nil
}

// SetStatus suspends or reactivates an account.
func (s *StaffService) SetStatus(ctx context.Context, tenantID primitive.ObjectID, actorID, targetID string, status models.UserStatus) error {
	if status != models.UserActive && status != models.UserSuspended {
		return apierr.ValidationFailed("status must be active or suspended")
	}
	id, err := primitive.ObjectIDFromHex(targetID)
	if err != nil {
		return apierr.BadRequest("id is not a valid id")
	}
	// A manager suspending themselves locks the business out of its own
	// back office, and the fix requires database access. Cheap to prevent.
	if actorID == targetID && status == models.UserSuspended {
		return apierr.ValidationFailed("you cannot suspend your own account")
	}
	if err := s.users.UpdateStatus(ctx, tenantID, id, status); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return apierr.NotFound("user")
		}
		return apierr.Internal(err)
	}
	return nil
}

// EnsureBootstrapManager creates the first manager when the collection has
// none. Called at startup, and a no-op on every restart after the first.
func (s *StaffService) EnsureBootstrapManager(ctx context.Context, tenantID primitive.ObjectID, name, email, plain string) error {
	n, err := s.users.CountByRole(ctx, tenantID, models.RoleManager)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if name == "" {
		name = "Manager"
	}
	hash, err := password.Hash(plain)
	if err != nil {
		return err
	}
	u := &models.User{
		TenantID:     tenantID,
		Role:         models.RoleManager,
		Name:         name,
		Email:        email,
		PasswordHash: hash,
		Status:       models.UserActive,
	}
	if err := s.users.Create(ctx, u); err != nil {
		// A duplicate here means someone else holds the address with a
		// different role. Not fatal, but it does mean the bootstrap did not
		// happen, so it must not be reported as success.
		return err
	}
	s.log.Info("bootstrap manager created", zap.String("email", u.Email))
	return nil
}
