package middleware

import (
	"errors"
	"strings"

	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/pkg/apierr"
	"github.com/eandstravel/carwash/pkg/token"
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Context keys set by Auth.Require. Handlers read them through
// internal/api/apictx rather than by string literal, so a typo is a compile
// error instead of a nil lookup at request time.
const (
	CtxUserID = "user_id"
	CtxRole   = "role"
	CtxUser   = "user"
)

type Auth struct {
	maker *token.Maker
	users *repository.UserRepo
}

func NewAuth(maker *token.Maker, users *repository.UserRepo) *Auth {
	return &Auth{maker: maker, users: users}
}

// Require verifies the bearer token, loads the user behind it, and checks
// their role is one of roles. With no roles given it authenticates only.
//
// It costs one indexed lookup by _id per authenticated request, and that is
// the point. A token-only check is stateless and fast, and it also means a
// suspended employee keeps every permission they had until their token
// expires — up to TOKEN_EXPIRY_HOURS of a person who has just been let go
// still being able to clock in and complete jobs. Suspension that does not
// take effect until tomorrow is not suspension. The role is read from the
// database for the same reason: a demotion applies on the next request, not
// on the next login.
func (a *Auth) Require(roles ...models.Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" || !strings.HasPrefix(header, "Bearer ") {
			fail(c, apierr.Unauthorized("missing authorization header"))
			return
		}

		claims, err := a.maker.VerifyToken(strings.TrimPrefix(header, "Bearer "))
		if err != nil {
			// The verifier's own wording ("invalid or expired token") is
			// useful to a legitimate client and tells an attacker nothing
			// they could not learn by waiting.
			fail(c, apierr.Unauthorized(err.Error()))
			return
		}

		id, err := primitive.ObjectIDFromHex(claims.UserID)
		if err != nil {
			fail(c, apierr.Unauthorized("malformed token subject"))
			return
		}

		user, err := a.users.FindByID(c.Request.Context(), id)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				fail(c, apierr.Unauthorized("this session is no longer valid"))
				return
			}
			fail(c, apierr.Internal(err))
			return
		}

		if user.Status != models.UserActive {
			fail(c, apierr.Forbidden("this account is suspended"))
			return
		}

		if len(roles) > 0 && !hasRole(user.Role, roles) {
			// No detail. Telling a customer that a route needs the manager
			// role maps the private surface for them.
			fail(c, apierr.Forbidden(""))
			return
		}

		c.Set(CtxUserID, user.ID)
		c.Set(CtxRole, user.Role)
		c.Set(CtxUser, user)
		c.Next()
	}
}

func hasRole(role models.Role, allowed []models.Role) bool {
	for _, r := range allowed {
		if role == r {
			return true
		}
	}
	return false
}
