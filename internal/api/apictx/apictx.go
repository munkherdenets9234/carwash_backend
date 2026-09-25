// Package apictx reads what the middleware put on the request, and parses
// what the client put in it.
//
// Every accessor here depends on a specific middleware having run. That is
// why they live in one place: one file to state which middleware each value
// comes from, and one file to change if a key ever moves.
package apictx

import (
	"time"

	"github.com/eandstravel/carwash/internal/entitlement"
	"github.com/eandstravel/carwash/internal/middleware"
	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/pkg/apierr"
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// UserID is the authenticated caller, set by middleware.Auth.Require.
//
// It panics when called from a route that is not behind that middleware —
// deliberately. A controller reading a caller id on an unauthenticated route
// is a routing mistake, and a zero ObjectID would quietly scope the query to
// nobody instead of saying so.
func UserID(c *gin.Context) primitive.ObjectID {
	return c.MustGet(middleware.CtxUserID).(primitive.ObjectID)
}

// Role is the caller's role, read from the database by the auth middleware
// rather than from the token — see middleware.Auth.Require.
func Role(c *gin.Context) models.Role {
	return c.MustGet(middleware.CtxRole).(models.Role)
}

// User is the caller's full record, already loaded by the auth middleware.
// Using it saves controllers a second lookup for something the request has
// paid for once.
func User(c *gin.Context) *models.User {
	return c.MustGet(middleware.CtxUser).(*models.User)
}

// Bind decodes a JSON body, turning a parse failure into one error shape
// rather than letting gin write its own.
func Bind(c *gin.Context, dst interface{}) error {
	if err := c.ShouldBindJSON(dst); err != nil {
		return apierr.BadRequest("request body is not valid JSON for this endpoint: " + err.Error())
	}
	return nil
}

// IDParam reads an ObjectID from the path.
func IDParam(c *gin.Context, name string) (primitive.ObjectID, error) {
	id, err := primitive.ObjectIDFromHex(c.Param(name))
	if err != nil {
		return primitive.NilObjectID, apierr.BadRequest(name + " is not a valid id")
	}
	return id, nil
}

// OptionalIDQuery reads an ObjectID filter from the query string. A missing
// value is (nil, nil) — "no filter" — while a present but malformed one is
// an error rather than being silently ignored, which would answer a narrowed
// question with unnarrowed data.
func OptionalIDQuery(c *gin.Context, name string) (*primitive.ObjectID, error) {
	raw := c.Query(name)
	if raw == "" {
		return nil, nil
	}
	id, err := primitive.ObjectIDFromHex(raw)
	if err != nil {
		return nil, apierr.BadRequest(name + " is not a valid id")
	}
	return &id, nil
}

// RequiredIDQuery reads a mandatory ObjectID from the query string.
func RequiredIDQuery(c *gin.Context, name string) (primitive.ObjectID, error) {
	raw := c.Query(name)
	if raw == "" {
		return primitive.NilObjectID, apierr.ValidationFailed(name + " is required")
	}
	id, err := primitive.ObjectIDFromHex(raw)
	if err != nil {
		return primitive.NilObjectID, apierr.BadRequest(name + " is not a valid id")
	}
	return id, nil
}

const dayLayout = "2006-01-02"

// Day reads a YYYY-MM-DD query parameter as a day in loc, defaulting to
// today when absent.
//
// Parsed in the business timezone, not UTC: "?day=2026-09-21" means that
// calendar day where the business is, which is the only reading that makes
// the answer match what someone standing in the branch would call today.
func Day(c *gin.Context, name string, loc *time.Location) (time.Time, error) {
	raw := c.Query(name)
	if raw == "" {
		return time.Now().In(loc), nil
	}
	d, err := time.ParseInLocation(dayLayout, raw, loc)
	if err != nil {
		return time.Time{}, apierr.BadRequest(name + " must be a date as YYYY-MM-DD")
	}
	return d, nil
}

// maxRangeDays caps a from/to window. An unbounded range on a timesheet is
// a full collection scan that one bookmarked URL can repeat all day.
const maxRangeDays = 92

// DateRange reads from/to as YYYY-MM-DD and returns the half-open instant
// range covering those days inclusive, in loc. Both absent means the current
// day.
func DateRange(c *gin.Context, loc *time.Location) (time.Time, time.Time, error) {
	now := time.Now().In(loc)

	parse := func(name string, fallback time.Time) (time.Time, error) {
		raw := c.Query(name)
		if raw == "" {
			return fallback, nil
		}
		d, err := time.ParseInLocation(dayLayout, raw, loc)
		if err != nil {
			return time.Time{}, apierr.BadRequest(name + " must be a date as YYYY-MM-DD")
		}
		return d, nil
	}

	fromDay, err := parse("from", now)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	toDay, err := parse("to", fromDay)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	from := time.Date(fromDay.Year(), fromDay.Month(), fromDay.Day(), 0, 0, 0, 0, loc)
	// "to" is inclusive to the reader — a range of the 1st to the 7th should
	// contain the 7th — so the exclusive bound is the start of the next day.
	to := time.Date(toDay.Year(), toDay.Month(), toDay.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)

	if !from.Before(to) {
		return time.Time{}, time.Time{}, apierr.ValidationFailed("from must not be after to")
	}
	if to.Sub(from) > maxRangeDays*24*time.Hour {
		return time.Time{}, time.Time{}, apierr.ValidationFailed("the range cannot be longer than 92 days")
	}
	return from, to, nil
}

// TenantID is the business this request belongs to, resolved by
// middleware.Tenant.Require from the X-API-Key header.
//
// It panics on a route without that middleware, deliberately — the same
// reasoning as UserID. A zero ObjectID here would not scope a query to
// nobody, which is merely useless; it would scope it to whatever rows happen
// to carry a zero tenant, which is worse than useless.
func TenantID(c *gin.Context) primitive.ObjectID {
	id, ok := middleware.TenantFrom(c)
	if !ok {
		panic("apictx.TenantID called on a route without the tenant middleware")
	}
	return id
}

// Entitlement is what this tenant's plan permits, already fetched by the
// tenant middleware. Using it saves a second call to the platform for
// something the request has paid for once.
//
// Service-layer limit checks read it from here. Middleware cannot do those:
// it sees a request, not a row count.
func Entitlement(c *gin.Context) entitlement.Entitlement {
	ent, ok := middleware.EntitlementFrom(c)
	if !ok {
		panic("apictx.Entitlement called on a route without the tenant middleware")
	}
	return ent
}
