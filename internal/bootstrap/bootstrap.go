// Package bootstrap wires every dependency the service needs and hands back
// a running application.
//
// It exists so main.go states the startup sequence and nothing else, and so
// the same wiring can be built for a test without copying it.
//
// The rule it enforces: a missing REQUIRED setting stops the process before
// anything is served, and a missing OPTIONAL one disables exactly one
// feature and says so — in the startup log and, for as long as the process
// lives, on /readyz.
//
// The reason is not tidiness. Refusing to boot the whole API because one
// optional thing was misconfigured is a worse outage than the one it
// prevents. The counterweight, and the part that is easy to skip, is that a
// degraded deployment has to be loud about it: silent degradation is how a
// feature stays switched off for a week with nobody noticing.
package bootstrap

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/eandstravel/carwash/internal/api"
	"github.com/eandstravel/carwash/internal/config"
	"github.com/eandstravel/carwash/internal/middleware"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/pkg/token"
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

// App is a fully wired application.
type App struct {
	Config *config.Config
	Log    *zap.Logger
	Engine *gin.Engine

	mongo   *mongo.Client
	limiter *middleware.RateLimiter
}

// New wires everything from cfg.
//
// It returns an error rather than exiting, so a test can assert on a bad
// configuration instead of killing the test binary. main.go is the one place
// that turns the error into a non-zero exit.
func New(ctx context.Context, cfg *config.Config, log *zap.Logger) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	client, db, err := connectMongo(ctx, cfg, log)
	if err != nil {
		return nil, err
	}

	app, err := NewForDatabase(ctx, cfg, db, log)
	if err != nil {
		_ = client.Disconnect(ctx)
		return nil, err
	}
	app.mongo = client
	return app, nil
}

// NewForDatabase is the half of New that takes an already-open database.
//
// It is what an integration test needs: the test brings its own disposable
// database and exercises the same wiring the binary uses, rather than a
// second copy of it that can drift.
func NewForDatabase(ctx context.Context, cfg *config.Config, db *mongo.Database, log *zap.Logger) (*App, error) {
	logFeatures(log, cfg)

	// Indexes are NOT optional. They carry correctness constraints, not
	// just speed: a duplicate account email is a login that resolves to
	// whichever row came back first, and a second open time entry is a
	// timesheet that double-counts a shift. A failure here stops startup.
	if err := repository.EnsureIndexes(ctx, db); err != nil {
		return nil, fmt.Errorf("index setup: %w", err)
	}

	maker, err := token.NewMaker(cfg.TokenSecret)
	if err != nil {
		return nil, fmt.Errorf("token maker: %w", err)
	}

	r := newRepos(db)
	s := newServices(r, maker, cfg, log)

	// Bootstrapping the first manager is optional: a deployment that
	// already has one does not need the variables, and one with neither is
	// a deployment nobody can log into — worth a loud error, not a refusal
	// to start, since an operator can create the account another way.
	if cfg.ManagerBootstrapEnabled() {
		if err := s.staff.EnsureBootstrapManager(ctx, cfg.ManagerName, cfg.ManagerEmail, cfg.ManagerPassword); err != nil {
			log.Error("manager bootstrap failed — no manager was created from the environment",
				zap.Error(err))
		}
	}

	limiter := middleware.NewRateLimiter()

	srv := api.NewServer(api.Deps{
		Config: cfg,
		Log:    log,

		Auth:        middleware.NewAuth(maker, r.users),
		RateLimiter: limiter,

		AuthSvc:      s.auth,
		Staff:        s.staff,
		Catalog:      s.catalog,
		Cars:         s.cars,
		Schedule:     s.schedule,
		Reservations: s.reservations,
		Attendance:   s.attendance,
		Reports:      s.reports,
		Resolver:     s.resolver,
	})

	return &App{Config: cfg, Log: log, Engine: srv.Handler(), limiter: limiter}, nil
}

// Run serves until SIGINT/SIGTERM, then shuts down cleanly.
func (a *App) Run() error {
	srv := &http.Server{
		Addr:         ":" + a.Config.AppPort,
		Handler:      a.Engine,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		a.Log.Info("server starting",
			zap.String("port", a.Config.AppPort),
			zap.String("env", string(a.Config.AppEnv)),
			zap.String("timezone", a.Config.Timezone))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-quit:
		a.Log.Info("shutdown signal received", zap.String("signal", sig.String()))
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	a.Close(shutdownCtx)
	a.Log.Info("server stopped")
	return nil
}

// Close releases everything New acquired.
func (a *App) Close(ctx context.Context) {
	if a.limiter != nil {
		a.limiter.Close()
	}
	if a.mongo != nil {
		_ = a.mongo.Disconnect(ctx)
	}
}

// logFeatures states every optional capability and its status at startup.
//
// A disabled feature is logged at WARN precisely so it shows up in whatever
// filters out the routine lines. This is the loud half of "degrade rather
// than crash", and without it the quiet half is just a silent outage.
func logFeatures(log *zap.Logger, cfg *config.Config) {
	for _, f := range cfg.Features() {
		if f.Enabled {
			log.Info("feature enabled", zap.String("feature", f.Name))
			continue
		}
		log.Warn("feature DISABLED — this deployment is running degraded",
			zap.String("feature", f.Name),
			zap.String("detail", f.Detail))
	}
}

func connectMongo(ctx context.Context, cfg *config.Config, log *zap.Logger) (*mongo.Client, *mongo.Database, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.MongoURI))
	if err != nil {
		return nil, nil, fmt.Errorf("mongo connect: %w", err)
	}
	// Connect is lazy, so without this ping a wrong URI produces a service
	// that starts cleanly and fails every request instead of failing here.
	if err := client.Ping(ctx, nil); err != nil {
		return nil, nil, fmt.Errorf("mongo ping: %w", err)
	}
	log.Info("mongodb connected", zap.String("database", cfg.MongoDB))
	return client, client.Database(cfg.MongoDB), nil
}
