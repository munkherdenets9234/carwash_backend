package bootstrap

import (
	"github.com/eandstravel/carwash/internal/config"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/internal/service"
	"github.com/eandstravel/carwash/pkg/token"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"
)

// repos and services are unexported bundles that exist to keep New
// readable. They are not a service locator: nothing outside this package can
// reach into them, and each audience package still receives only the
// services its own Register signature names.

type repos struct {
	users     *repository.UserRepo
	cars      *repository.CarRepo
	locations *repository.LocationRepo
	services  *repository.WashServiceRepo
	shifts    *repository.ShiftRepo
	bookings  *repository.ReservationRepo
	entries   *repository.TimeEntryRepo
}

func newRepos(db *mongo.Database) repos {
	return repos{
		users:     repository.NewUserRepo(db),
		cars:      repository.NewCarRepo(db),
		locations: repository.NewLocationRepo(db),
		services:  repository.NewWashServiceRepo(db),
		shifts:    repository.NewShiftRepo(db),
		bookings:  repository.NewReservationRepo(db),
		entries:   repository.NewTimeEntryRepo(db),
	}
}

type services struct {
	auth         *service.AuthService
	staff        *service.StaffService
	catalog      *service.CatalogService
	cars         *service.CarService
	schedule     *service.ScheduleService
	reservations *service.ReservationService
	attendance   *service.AttendanceService
	reports      *service.ReportService
	resolver     *service.Resolver
}

func newServices(r repos, maker *token.Maker, cfg *config.Config, log *zap.Logger) services {
	return services{
		auth:    service.NewAuthService(r.users, maker, cfg.TokenExpiry, log),
		staff:   service.NewStaffService(r.users, r.locations, log),
		catalog: service.NewCatalogService(r.locations, r.services),
		cars:    service.NewCarService(r.cars),
		schedule: service.NewScheduleService(
			r.shifts, r.users, r.bookings, r.locations, r.services,
			cfg.Location, cfg.SlotStepMin),
		reservations: service.NewReservationService(
			r.bookings, r.shifts, r.users, r.cars, r.services, r.locations,
			cfg.MaxBookingDaysAhead),
		attendance: service.NewAttendanceService(r.entries, r.locations, r.users),
		reports:    service.NewReportService(r.bookings, r.entries, r.users, cfg.Location),
		resolver:   service.NewResolver(r.users, r.cars, r.services, r.locations),
	}
}
