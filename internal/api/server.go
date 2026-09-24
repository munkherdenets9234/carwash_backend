// Package api builds the HTTP surface.
//
// The audience split lives one level down, in public/, customer/, employee/
// and manager/. This package only wires: engine-wide middleware, the
// operational endpoints, and mounting each audience behind the role that
// may enter it.
package api

import (
	"github.com/eandstravel/carwash/internal/config"
	"github.com/eandstravel/carwash/internal/middleware"
	"github.com/eandstravel/carwash/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Deps is everything the HTTP layer needs. Deliberately a flat struct of
// services rather than a container handlers can reach into: a controller
// gets what its own Register passes it, and nothing else.
type Deps struct {
	Config *config.Config
	Log    *zap.Logger

	Auth        *middleware.Auth
	Tenant      *middleware.Tenant
	RateLimiter *middleware.RateLimiter

	AuthSvc      *service.AuthService
	Staff        *service.StaffService
	Catalog      *service.CatalogService
	Cars         *service.CarService
	Schedule     *service.ScheduleService
	Reservations *service.ReservationService
	Attendance   *service.AttendanceService
	Reports      *service.ReportService
	Resolver     *service.Resolver
	Media        *service.MediaService
}

// Server is the built HTTP engine.
type Server struct {
	engine *gin.Engine
	deps   Deps
}

func NewServer(d Deps) *Server {
	s := &Server{deps: d}
	s.engine = s.buildEngine()
	return s
}

// Handler exposes the engine, for http.Server and for tests.
func (s *Server) Handler() *gin.Engine { return s.engine }
