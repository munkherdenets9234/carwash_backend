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

// RegisterInput is a self-service signup.
type RegisterInput struct {
	Name     string
	Email    string
	Phone    string
	Password string

	// ClaimCustomerID is a guest record this signup has PROVED it owns, or
	// nil. The proof is not done here: the caller verifies a booking
	// reference against the phone number through the same path the public
	// lookup uses, and passes the id it resolved. Keeping one implementation
	// of "does this code and number check out" is the point — a second one
	// written for signup is the one that would drift.
	ClaimCustomerID *primitive.ObjectID
}

// RegisterCustomer creates a customer account. It is the only self-service
// signup: employees and managers are created by a manager, because a public
// endpoint that mints staff accounts is a public endpoint that mints
// authority.
//
// Booking without an account added a second job. A guest who has booked
// already HAS a customer record, keyed by their phone number and holding
// their bookings, and the naive signup collided with it: the phone derives a
// unique contact_key, so registering with a number that had booked before
// failed on the index and was reported as "that email address is already
// registered" — about an address that was not registered at all, and which
// the person could not fix by changing.
//
// So there are three cases now, and each has to be answered differently.
func (s *AuthService) RegisterCustomer(ctx context.Context, tenantID primitive.ObjectID, in RegisterInput) (*Session, error) {
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

	hash, err := password.Hash(in.Password)
	if err != nil {
		return nil, apierr.Internal(err)
	}

	// Case one: a proved claim. The guest record becomes the account, in
	// place, so every booking made under that phone number is simply there
	// when they sign in.
	if in.ClaimCustomerID != nil {
		if err := s.users.UpgradeGuest(ctx, tenantID, *in.ClaimCustomerID, name, in.Email, hash); err != nil {
			if errors.Is(err, repository.ErrDuplicate) {
				return nil, apierr.Conflict("that email address is already registered").In(apierr.DomainAuth)
			}
			if errors.Is(err, repository.ErrNotFound) {
				// The row exists — the caller resolved it a moment ago — so
				// this means it already had a password: an account, not a
				// guest. Signing in is the answer, not a second account.
				return nil, apierr.Conflict("there is already an account for that phone number — sign in instead").In(apierr.DomainAuth)
			}
			return nil, apierr.Internal(err)
		}
		u, err := s.users.FindByID(ctx, tenantID, *in.ClaimCustomerID)
		if err != nil {
			return nil, apierr.Internal(err)
		}
		return s.issue(u)
	}

	phone := models.NormalizePhone(in.Phone)

	// Case two: no claim, but the number is already known. Refused with the
	// thing that actually works — rather than the unique index refusing it and
	// the message blaming the email address.
	//
	// Which message depends on whether that row can be signed in to, and the
	// distinction is not cosmetic: telling the holder of an existing ACCOUNT
	// to go and find a booking code sends them after something that cannot
	// work, because the claim above refuses a row that already has
	// credentials. One of these two answers is always wrong for the other
	// case, so the branch has to exist.
	if phone != "" {
		existing, err := s.users.FindByContactKey(ctx, tenantID, phone)
		switch {
		case err == nil && existing.PasswordHash != "":
			return nil, apierr.Conflict(
				"there is already an account for that phone number — sign in instead.").In(apierr.DomainAuth)
		case err == nil:
			// A guest record: bookings, no login. That one CAN be claimed,
			// and the booking code is how.
			return nil, apierr.Conflict(
				"there are already bookings under that phone number. Add your booking code to bring them " +
					"into this account, or register without a phone number.").In(apierr.DomainAuth)
		case !errors.Is(err, repository.ErrNotFound):
			return nil, apierr.Internal(err)
		}
	}

	// Case three: an ordinary new customer.
	u := &models.User{
		TenantID:     tenantID,
		Role:         models.RoleCustomer,
		Name:         name,
		Email:        in.Email,
		Phone:        phone,
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
func (s *AuthService) Login(ctx context.Context, tenantID primitive.ObjectID, email, plain string) (*Session, error) {
	u, err := s.users.FindByEmail(ctx, tenantID, email)
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
func (s *AuthService) Current(ctx context.Context, tenantID primitive.ObjectID, userID string) (*models.User, error) {
	id, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return nil, apierr.Unauthorized("malformed token subject")
	}
	u, err := s.users.FindByID(ctx, tenantID, id)
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
