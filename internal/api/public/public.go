// Package public holds the routes that need no credentials at all.
//
// It is deliberately small, and what is missing from it matters as much as
// what is in it. Availability lives on the customer surface, not here,
// because it names members of staff and states when each of them is working
// — a roster is not something an anonymous caller should be able to page
// through. The catalogue below is the price list on the wall: no personal
// data, nothing an ordinary visitor could not read anyway.
package public

import (
	"github.com/eandstravel/carwash/internal/service"
	"github.com/eandstravel/carwash/pkg/httpx"
	"github.com/gin-gonic/gin"
)

type Deps struct {
	Auth    *service.AuthService
	Catalog *service.CatalogService

	// AuthRateLimit guards the two endpoints that take a password.
	AuthRateLimit gin.HandlerFunc
}

// Register mounts the unauthenticated routes on base.
func Register(base *gin.RouterGroup, d Deps) {
	auth := &authController{svc: d.Auth}
	catalog := &catalogController{svc: d.Catalog}

	// Both of these tell an anonymous caller something about whether an
	// account exists — login by refusing, registration by conflicting — and
	// both hash a password on every call, which is CPU somebody else can
	// spend for free. Rate limited, not as a formality but because these
	// two are the reason the limiter exists.
	a := httpx.Wrap(base.Group("/auth", d.AuthRateLimit))
	a.POST("/register", auth.Register)
	a.POST("/login", auth.Login)

	g := httpx.Wrap(base.Group(""))
	g.GET("/services", catalog.ListServices)
	g.GET("/locations", catalog.ListLocations)
}
