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
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/eandstravel/carwash/internal/api"
	"github.com/eandstravel/carwash/internal/config"
	"github.com/eandstravel/carwash/internal/entitlement"
	"github.com/eandstravel/carwash/internal/middleware"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/pkg/token"
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson/primitive"
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

	// The platform link. Optional in the same sense as every other
	// dependency here: without it the process still starts, still serves
	// /healthz and /readyz, and says on both that it cannot resolve tenants.
	// The data routes then answer FEATURE_UNAVAILABLE, which is a far more
	// useful thing for an operator to see than a process that will not boot.
	entClient := entitlement.NewClient(entitlement.Config{
		BaseURL:     cfg.TenantcoreURL,
		ServiceKey:  cfg.TenantcoreServiceKey,
		TTL:         cfg.EntitlementTTL,
		GraceWindow: cfg.EntitlementGrace,
		Log:         log,
	})

	// Bootstrapping the first manager is optional, and now belongs to ONE
	// named tenant: "create the first manager at startup" was a
	// single-business idea, and multi-tenancy leaves no single business to
	// create one for. This is the development and demo path only — see
	// config.BootstrapTenantID for what production still needs.
	if cfg.ManagerBootstrapEnabled() {
		tenantID, err := primitive.ObjectIDFromHex(cfg.BootstrapTenantID)
		if err != nil {
			log.Error("manager bootstrap skipped — BOOTSTRAP_TENANT_ID is not a valid id",
				zap.String("value", cfg.BootstrapTenantID))
		} else if err := s.staff.EnsureBootstrapManager(ctx, tenantID, cfg.ManagerName, cfg.ManagerEmail, cfg.ManagerPassword); err != nil {
			log.Error("manager bootstrap failed — no manager was created from the environment",
				zap.Error(err))
		}
	}

	limiter := middleware.NewRateLimiter()

	deps := api.Deps{
		Config: cfg,
		Log:    log,

		Auth:        middleware.NewAuth(maker, r.users),
		Tenant:      middleware.NewTenant(entClient, cfg.Module),
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
		Media:        s.media,
	}

	if err := requireWired(deps); err != nil {
		return nil, err
	}
	srv := api.NewServer(deps)

	return &App{Config: cfg, Log: log, Engine: srv.Handler(), limiter: limiter}, nil
}

// Run serves until SIGINT/SIGTERM, then shuts down cleanly.
func (a *App) Run() error {
	srv := &http.Server{
		Addr:    ":" + a.Config.AppPort,
		Handler: a.Engine,

		// ReadHeaderTimeout is the tight one, and it is the one that matters.
		// It is what stops a client from opening a connection and dribbling
		// headers forever to hold a goroutine hostage. It does not care how
		// large the body is or how long the handler runs, so it can stay
		// short without breaking anything slow and legitimate.
		ReadHeaderTimeout: 15 * time.Second,

		// ReadTimeout and WriteTimeout cover the BODY and the HANDLER, and
		// they were both 15s here, which was wrong: an image upload is the
		// one request in this service that is routinely slower than that.
		//
		// The symptom was not a clean error. WriteTimeout is measured from
		// the end of the header read, so it covers the round trip to the
		// image host. A 30s upload finished, stored the row, logged 201 —
		// and Go had already torn the connection down, so the caller saw the
		// backend go unreachable. The photograph was uploaded; the manager
		// was told it was not. On a replace, that also meant the delete of
		// the old picture never ran, leaving a duplicate nobody asked for.
		//
		// A silent success reported as a failure is worse than a slow
		// request, so these are now sized for the slowest thing the service
		// legitimately does: a 10 MiB photograph sent from a phone on mobile
		// data, then forwarded to an image host that has been observed
		// taking 25-35s from here.
		//
		// The cost is honest and worth stating: a handler wedged on a
		// dependency now holds its connection for minutes rather than
		// seconds. What bounds the damage is not this timeout — it is the
		// body size cap and the per-request context deadlines inside the
		// handlers, which are the right place for it, because only the
		// handler knows what it is waiting on.
		ReadTimeout:  a.Config.HTTPReadTimeout,
		WriteTimeout: a.Config.HTTPWriteTimeout,

		// Without this a keep-alive connection that goes quiet is held until
		// the client gives up.
		IdleTimeout: 120 * time.Second,
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

// requireWired refuses to start when a dependency was built but never handed
// to the router.
//
// This exists because of a real bug: the media service was constructed, the
// routes were registered, the tests were green, and every photograph request
// answered 500 — because one line was missing from the struct literal above.
// A forgotten field in a Go composite literal is not an error, it is the zero
// value, so the cost of the mistake is paid by the first customer to load the
// page rather than by the deploy.
//
// Reflection rather than a list of checks on purpose: a list has to be
// updated when a field is added, which is the same act of remembering that
// failed in the first place. Every pointer, interface and map field must be
// non-nil, so a new dependency is covered the moment it is declared.
//
// It fails the boot rather than logging. An unreachable feature is not the
// degraded-but-serving state the optional dependencies above are about: there
// is no configuration to fix and nothing an operator can do at runtime, it is
// simply a build that should not be deployed.
func requireWired(d api.Deps) error {
	v := reflect.ValueOf(d)
	t := v.Type()
	var missing []string
	for i := 0; i < t.NumField(); i++ {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func:
			if f.IsNil() {
				missing = append(missing, t.Field(i).Name)
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("router dependencies not wired: %s — these were left out of the "+
			"api.Deps literal in bootstrap, so every route using them would answer 500",
			strings.Join(missing, ", "))
	}
	return nil
}
