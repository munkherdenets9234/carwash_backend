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
	"github.com/eandstravel/carwash/pkg/token"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"
)

// minPasswordLen is checked on every path that sets a password, including
// the manager creating staff — a weak password chosen on someone's behalf is
// no better than one they chose themselves.
const minPasswordLen = 8

type AuthService struct {
	users  *repository.UserRepo
	maker  *token.Maker
	expiry time.Duration
	log    *zap.Logger
}

func NewAuthService(users *repository.UserRepo, maker *token.Maker, expiryHours int, log *zap.Logger) *AuthService {
	return &AuthService{
		users:  users,
		maker:  maker,
		expiry: time.Duration(expiryHours) * time.Hour,
		log:    log,
	}
}

// Session is what a successful login hands back.
type Session struct {
	Token     string
	ExpiresAt time.Time
	User      *models.User
}

// RegisterCustomer creates a customer account. It is the only self-service
// signup: employees and managers are created by a manager, because a public
// endpoint that mints staff accounts is a public endpoint that mints
// authority.
func (s *AuthService) RegisterCustomer(ctx context.Context, name, email, phone, plain string) (*Session, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, apierr.ValidationFailed("name is required")
	}
	if err := validateEmail(email); err != nil {
		return nil, err
	}
	if len(plain) < minPasswordLen {
		return nil, apierr.ValidationFailed("password must be at least 8 characters")
	}

	hash, err := password.Hash(plain)
	if err != nil {
		return nil, apierr.Internal(err)
	}

	u := &models.User{
		Role:         models.RoleCustomer,
		Name:         name,
		Email:        email,
		Phone:        strings.TrimSpace(phone),
		PasswordHash: hash,
		Status:       models.UserActive,
	}
	if err := s.users.Create(ctx, u); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			// This does confirm the address is registered. The alternative —
			// accepting the signup and saying nothing — leaves someone
			// unable to log in with no explanation, so the honest error is
			// the better trade. The route is rate limited, which is what
			// keeps it from being a bulk enumeration tool.
			return nil, apierr.Conflict("that email address is already registered").In(apierr.DomainAuth)
		}
		return nil, apierr.Internal(err)
	}

	return s.issue(u)
}

// Login verifies credentials and issues a token.
func (s *AuthService) Login(ctx context.Context, email, plain string) (*Session, error) {
	u, err := s.users.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// Deliberately the same error, with the same wording and the
			// same status, as a wrong password below. Distinguishing them
			// turns this endpoint into a "does this person have an account
			// here" lookup for anyone with a list of email addresses.
			return nil, apierr.Unauthorized("invalid email or password")
		}
		return nil, apierr.Internal(err)
	}

	if !password.Verify(u.PasswordHash, plain) {
		return nil, apierr.Unauthorized("invalid email or password")
	}

	// Checked after the password, not before: answering "this account is
	// suspended" to anyone who types the address would leak which addresses
	// are real.
	if u.Status != models.UserActive {
		return nil, apierr.Forbidden("this account is suspended")
	}

	return s.issue(u)
}

// Current loads the caller's own record. Used by the /me endpoint and by
// middleware-adjacent checks that need more than the token's claims.
func (s *AuthService) Current(ctx context.Context, userID string) (*models.User, error) {
	id, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return nil, apierr.Unauthorized("malformed token subject")
	}
	u, err := s.users.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// A valid signature over a user that no longer exists. Treated
			// as unauthenticated rather than as a 404: from the caller's
			// point of view their session is simply over.
			return nil, apierr.Unauthorized("this session is no longer valid")
		}
		return nil, apierr.Internal(err)
	}
	return u, nil
}

func (s *AuthService) issue(u *models.User) (*Session, error) {
	signed, claims, err := s.maker.CreateToken(u.ID.Hex(), string(u.Role), s.expiry)
	if err != nil {
		return nil, apierr.Internal(err)
	}
	return &Session{Token: signed, ExpiresAt: claims.ExpiresAt.Time, User: u}, nil
}

// validateEmail is a shape check, not a deliverability check. It exists to
// catch a transposed field or a missing "@" before the value reaches a
// unique index, not to decide whether mail would arrive.
func validateEmail(email string) error {
	e := strings.TrimSpace(email)
	if e == "" {
		return apierr.ValidationFailed("email is required")
	}
	at := strings.Index(e, "@")
	if at <= 0 || at == len(e)-1 || strings.Contains(e, " ") {
		return apierr.ValidationFailed("email is not a valid address")
	}
	if !strings.Contains(e[at+1:], ".") {
		return apierr.ValidationFailed("email is not a valid address")
	}
	return nil
}
