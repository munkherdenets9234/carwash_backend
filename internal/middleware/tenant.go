package middleware

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/eandstravel/carwash/internal/entitlement"
	"github.com/eandstravel/carwash/pkg/apierr"
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Context keys set by RequireTenant.
const (
	CtxTenantID    = "tenant_id"
	CtxEntitlement = "entitlement"
)

// EntitlementSource is what the tenant gate needs from the platform link.
//
// An interface rather than *entitlement.Client so the gate can be exercised
// without a running platform. The failure modes below are the most important
// behaviour in this file and the hardest to reproduce by hand — a test that
// cannot make the platform return "unknown key" or go unreachable is a test
// that only covers the happy path.
type EntitlementSource interface {
	// Available reports whether the link is configured at all.
	Available() bool
	// ForKey resolves a tenant API key to its entitlement, serving cached
	// state when the platform cannot be reached.
	ForKey(ctx context.Context, tenantKey string) (entitlement.Entitlement, error)
}

// Tenant resolves which business a request belongs to, and what its plan
// permits, from a single cached call to the platform.
//
// One call for both on purpose. The platform owns the tenants collection, so
// "which business is this" and "what have they bought" are one question
// there; asking separately would mean this service keeping a copy of the
// tenants to drift out of step with theirs.
type Tenant struct {
	client EntitlementSource
	module string
}

// NewTenant builds the gate. A nil client is the unconfigured state and is
// handled in Require, so callers need no nil check of their own.
func NewTenant(client EntitlementSource, module string) *Tenant {
	return &Tenant{client: client, module: module}
}

// available is nil-safe over the interface: a nil *entitlement.Client stored
// in a non-nil interface still answers false through its own method, and a
// nil interface is handled here.
func (t *Tenant) available() bool {
	return t != nil && t.client != nil && t.client.Available()
}

// Require resolves the tenant from X-API-Key and refuses a caller whose plan
// does not include this product.
//
// It must be the FIRST middleware on every data route, ahead of Auth: a token
// is issued against one tenant and means nothing until we know which tenant
// the request claims to be for.
//
// The failure modes, which are the whole design:
//
//   - No key, or a key the platform does not know → 401. A failed
//     authentication.
//   - Known key, plan without this module → 402 MODULE_NOT_ENTITLED. They
//     are one plan change away, so a client shows an upgrade.
//   - Platform unreachable, something cached → the cached answer, with a
//     warning logged and /readyz reporting degraded.
//   - Platform unreachable, nothing cached → 503. NEVER 402. Deciding
//     "not entitled" because we could not ask would make the platform a
//     single point of failure for every product at once, and would tell
//     paying customers they had not paid.
func (t *Tenant) Require() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !t.available() {
			// The platform link was not configured at startup. Said once at
			// startup and on /readyz; said here so a caller gets a
			// distinguishable answer rather than a bare 500.
			fail(c, apierr.FeatureUnavailable("the platform link"))
			return
		}

		key := c.GetHeader("X-API-Key")
		if key == "" {
			fail(c, apierr.Unauthorized("missing X-API-Key header").In(apierr.DomainTenant))
			return
		}

		ent, err := t.client.ForKey(c.Request.Context(), key)
		if err != nil {
			if errors.Is(err, entitlement.ErrUnknownTenant) {
				fail(c, apierr.Unauthorized("").In(apierr.DomainTenant))
				return
			}
			// Could not find out. 503, with the cause logged — never a 402.
			e := apierr.Wrap(err, http.StatusServiceUnavailable, apierr.DomainTenant,
				apierr.CodeUpstream, "cannot reach the platform to check this subscription")
			fail(c, e)
			return
		}

		if ent.TenantID.IsZero() {
			fail(c, apierr.Internal(errors.New("platform returned an entitlement with no tenant id")))
			return
		}

		if !ent.HasModule(t.module) {
			fail(c, apierr.ModuleNotEntitled(t.module))
			return
		}

		c.Set(CtxTenantID, ent.TenantID)
		c.Set(CtxEntitlement, ent)
		c.Next()
	}
}

// RequireActiveSubscription refuses MUTATING calls when the subscription is
// not in a paying state.
//
// Reads are deliberately allowed through. A tenant whose card expired can
// still see their bookings, their roster and their staff — and, more to the
// point, can still reach the screen that tells them why. Cutting off reads
// turns a billing problem into "the app is broken" and generates a support
// ticket instead of a payment.
//
// Must run after Require, which puts the entitlement on the context.
func (t *Tenant) RequireActiveSubscription() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			c.Next()
			return
		}

		ent, ok := EntitlementFrom(c)
		if !ok {
			fail(c, apierr.Internal(errors.New("subscription gate mounted without the tenant middleware ahead of it")))
			return
		}

		// An unknown status is a tenant the platform has not provisioned a
		// subscription for yet. That is a legitimate gap — provisioning is a
		// separate deliberate act — and it is treated as permitted here, the
		// same way the platform's own gate treats it, rather than locking
		// out a business that was onboarded five minutes ago.
		if ent.Status == entitlement.StatusUnknown {
			c.Next()
			return
		}

		if !ent.Active() {
			fail(c, apierr.SubscriptionRequired(""))
			return
		}
		c.Next()
	}
}

// TenantFrom reads the resolved tenant. ok is false on a route with no tenant
// middleware ahead of it.
func TenantFrom(c *gin.Context) (primitive.ObjectID, bool) {
	v, exists := c.Get(CtxTenantID)
	if !exists {
		return primitive.NilObjectID, false
	}
	id, ok := v.(primitive.ObjectID)
	return id, ok
}

// EntitlementFrom reads the entitlement the tenant middleware already
// fetched, so a service-layer limit check on the same request costs no second
// lookup.
func EntitlementFrom(c *gin.Context) (entitlement.Entitlement, bool) {
	v, exists := c.Get(CtxEntitlement)
	if !exists {
		return entitlement.Entitlement{}, false
	}
	ent, ok := v.(entitlement.Entitlement)
	return ent, ok
}

// PlatformDegraded reports whether the platform link is currently failing,
// and since when — surfaced on /readyz.
//
// Falling back to cache is the right behaviour and a liability on its own:
// without this, a platform that has been down all day looks exactly like one
// that is fine. Alert on it.
func (t *Tenant) PlatformDegraded() (bool, *time.Time) {
	if t == nil || t.client == nil {
		return false, nil
	}
	d, ok := t.client.(interface {
		Degraded() (bool, *time.Time)
	})
	if !ok {
		return false, nil
	}
	return d.Degraded()
}
