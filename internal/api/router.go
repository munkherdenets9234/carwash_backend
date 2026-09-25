package api

import (
	"net/http"

	"github.com/eandstravel/carwash/docs"
	"github.com/eandstravel/carwash/internal/api/apictx"
	"github.com/eandstravel/carwash/internal/api/customer"
	"github.com/eandstravel/carwash/internal/api/demo"
	"github.com/eandstravel/carwash/internal/api/employee"
	"github.com/eandstravel/carwash/internal/api/manager"
	"github.com/eandstravel/carwash/internal/api/public"
	"github.com/eandstravel/carwash/internal/middleware"
	"github.com/eandstravel/carwash/internal/view"
	"github.com/eandstravel/carwash/pkg/httpx"
	"github.com/eandstravel/carwash/pkg/response"
	"github.com/gin-gonic/gin"
)

func (s *Server) buildEngine() *gin.Engine {
	d := s.deps
	devMode := d.Config.IsDev()

	gin.SetMode(gin.ReleaseMode)
	if devMode {
		gin.SetMode(gin.DebugMode)
	}

	e := gin.New()

	// Order matters. Recovery is outermost so a panic in any later
	// middleware is still rendered as a proper envelope; ErrorHandler wraps
	// everything below it so it sees errors raised by middleware as well as
	// by controllers.
	e.Use(middleware.Recovery(d.Log, devMode))
	e.Use(middleware.Logger(d.Log))
	e.Use(middleware.CORS(d.Config.CORSOrigins))
	e.Use(middleware.ErrorHandler(d.Log, devMode))

	s.registerOperational(e)

	// Every data route below is tenant-scoped, and the gate is applied once,
	// here, on the group they all hang off. Nothing underneath can opt out.
	//
	// The order is the argument: the tenant is resolved from X-API-Key
	// first, because a bearer token is issued by one business and means
	// nothing until we know which business the request claims to be for;
	// then the subscription gate, which refuses only mutating calls; then
	// each audience applies its own role check.
	//
	// The operational routes registered above stay outside it. A health
	// check that needed a tenant API key could not be run by a monitor.
	api := e.Group("/api/v1", d.Tenant.Require(), d.Tenant.RequireActiveSubscription())

	public.Register(api, public.Deps{
		Auth:         d.AuthSvc,
		Catalog:      d.Catalog,
		Media:        d.Media,
		Schedule:     d.Schedule,
		Reservations: d.Reservations,
		Staff:        d.Staff,
		Resolver:     d.Resolver,
		Loc:          d.Config.Location,

		AuthRateLimit:    s.limit("auth", d.Config.AuthRatePerMinute),
		BookingRateLimit: s.limit("booking", d.Config.BookingRatePerMinute),
	})

	// /me is the one authenticated route that is not audience-specific:
	// every role needs to be able to ask who it is signed in as. No role
	// argument, so it admits any authenticated caller.
	httpx.Wrap(api.Group("", d.Auth.Require())).GET("/me", func(c *gin.Context) error {
		response.OK(c, view.MeOf(apictx.User(c)))
		return nil
	})

	customer.Register(api.Group("/customer"), customer.Deps{
		Auth:             d.Auth.Require,
		BookingRateLimit: s.limit("booking", d.Config.BookingRatePerMinute),
		Loc:              d.Config.Location,
		Cars:             d.Cars,
		Reservations:     d.Reservations,
		Resolver:         d.Resolver,
	})

	employee.Register(api.Group("/employee"), employee.Deps{
		Auth:                d.Auth.Require,
		AttendanceRateLimit: s.limit("attendance", d.Config.BookingRatePerMinute),
		Loc:                 d.Config.Location,
		Attendance:          d.Attendance,
		Schedule:            d.Schedule,
		Reservations:        d.Reservations,
		Resolver:            d.Resolver,
	})

	manager.Register(api.Group("/manager"), manager.Deps{
		Auth:          d.Auth.Require,
		AuthRateLimit: s.limit("auth", d.Config.AuthRatePerMinute),
		Loc:           d.Config.Location,
		Staff:         d.Staff,
		Catalog:       d.Catalog,
		Media:         d.Media,
		Schedule:      d.Schedule,
		Reservations:  d.Reservations,
		Attendance:    d.Attendance,
		Reports:       d.Reports,
		Resolver:      d.Resolver,
	})

	if d.Config.DemoConsoleEnabled {
		demo.Register(e)
	}

	return e
}

// limit returns the named rate-limit middleware, or a pass-through when
// rate limiting is switched off.
//
// Returning a no-op rather than skipping the middleware keeps the route tree
// identical in both cases, so a limit that is off cannot also change which
// middleware a route runs — the sort of difference that makes a bug appear
// only in the environment where limiting is disabled.
func (s *Server) limit(name string, perMinute int) gin.HandlerFunc {
	if !s.deps.Config.RateLimitEnabled || s.deps.RateLimiter == nil {
		return func(c *gin.Context) { c.Next() }
	}
	return s.deps.RateLimiter.Limit(name, perMinute, s.deps.Config.RateLimitBurst)
}

// registerOperational mounts the endpoints that describe the service rather
// than serve its data. None are versioned.
func (s *Server) registerOperational(e *gin.Engine) {
	// healthz answers "is the process up". Kept cheap and dependency-free:
	// a health check that talks to the database turns a slow query into a
	// restart loop.
	e.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// readyz answers "what is this deployment actually able to do".
	//
	// This is the counterweight to letting optional dependencies degrade
	// rather than crash. Degrading quietly is the right behaviour and a
	// liability on its own: a feature can sit switched off for a week with
	// nothing to say so. The same list is written to the log at startup,
	// but a log line scrolls away — this endpoint keeps answering for as
	// long as the process runs, so a monitor can alert on it and a human
	// can check it without shell access.
	//
	// 200 with degraded:true, not 503: a deployment missing an optional
	// feature is degraded, not unhealthy, and a failure status here would
	// make an orchestrator restart a process working exactly as configured.
	// The API contract, served so an integrating service can fetch it
	// instead of being sent a copy that then goes stale in their repo.
	// Unauthenticated on purpose: it describes the shape of the API, not
	// any tenant's data, and a contract you need a token to read is one
	// nobody reads before writing their client.
	e.GET("/docs/api.json", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/json; charset=utf-8", docs.OpenAPI)
	})

	e.GET("/readyz", func(c *gin.Context) {
		features := s.deps.Config.Features()
		degraded := false
		list := make([]gin.H, 0, len(features)+1)

		// The platform link reports its RUNTIME state here, not just whether
		// it was configured. A link that is configured and currently failing
		// looks identical to a healthy one from the outside — the service
		// keeps answering, from cache — right up until an entry ages out and
		// a tenant is refused for no visible reason. This is the one place
		// that difference is visible, which is the entire reason the client
		// tracks it.
		if s.deps.Tenant != nil {
			if stale, since := s.deps.Tenant.PlatformDegraded(); stale {
				degraded = true
				entry := gin.H{"name": "platform_link_live", "enabled": false,
					"detail": "the platform is unreachable; entitlements are being served from cache and will start failing once they age out"}
				if since != nil {
					entry["since"] = since.UTC()
				}
				list = append(list, entry)
			}
		}
		for _, f := range features {
			if !f.Enabled {
				degraded = true
			}
			entry := gin.H{"name": f.Name, "enabled": f.Enabled}
			if !f.Enabled {
				entry["detail"] = f.Detail
			}
			list = append(list, entry)
		}
		c.JSON(http.StatusOK, gin.H{
			"status":   "ok",
			"env":      string(s.deps.Config.AppEnv),
			"timezone": s.deps.Config.Timezone,
			"degraded": degraded,
			"features": list,
		})
	})
}
