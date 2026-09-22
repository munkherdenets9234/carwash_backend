// Package config loads and validates every setting this service reads from
// the environment.
//
// Two rules shape it:
//
//   - A missing *required* setting stops the process, and Validate reports
//     every missing key at once. Discovering them one restart at a time is
//     the slowest possible way to bring up a new environment.
//   - A missing *optional* setting disables one feature and nothing else.
//     Which features those are is not implicit: Features() names them, the
//     startup log states each one's status, and /readyz keeps reporting it
//     for as long as the process runs.
package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	// The timezone database is embedded rather than read from the host.
	//
	// Windows ships no /usr/share/zoneinfo and a scratch container ships no
	// tzdata package, so time.LoadLocation("Asia/Ulaanbaatar") fails on both
	// — on the dev machine and in the smallest production image, but not on
	// a developer's Linux laptop, which is the worst way to find out. The
	// embedded copy costs about 450 KB in the binary and removes the whole
	// class of problem.
	_ "time/tzdata"
)

type Env string

const (
	EnvDevelopment Env = "development"
	EnvProduction  Env = "production"
	EnvTest        Env = "test"
)

type Config struct {
	AppEnv  Env
	AppPort string

	// Required.
	MongoURI    string
	MongoDB     string
	TokenSecret string

	TokenExpiry int // hours

	// Optional — each blank value disables exactly one feature.
	ManagerName     string
	ManagerEmail    string // blank (with password): no startup manager bootstrap
	ManagerPassword string

	// DemoConsoleEnabled serves the single-page operator console at /demo.
	// Off in production by default: it is a demonstration surface, and a
	// deployment serving real customer bookings should not also ship an
	// unauthenticated page that enumerates its own API.
	DemoConsoleEnabled bool

	// Scheduling.
	SlotStepMin         int // granularity of offered booking slots
	MaxBookingDaysAhead int

	// Timezone is the business day this service reports in.
	//
	// It is configuration, not a constant, and not the server's local zone.
	// "Today's revenue" has to mean the same day to the manager reading it
	// and the employee who earned it, and a container defaulting to UTC
	// would cut the business day at 08:00 local — putting the morning's
	// washes on the previous report. Held as a loaded *time.Location so a
	// bad name fails at startup rather than at the first report.
	Timezone string
	Location *time.Location

	// CORSOrigins is the exact list of browser origins allowed to call this
	// API with credentials. Empty means none — see middleware.CORS.
	CORSOrigins []string

	// Rate limiting. Per client IP, per named route group.
	RateLimitEnabled     bool
	AuthRatePerMinute    int // login and registration
	BookingRatePerMinute int // authenticated writes that create rows
	RateLimitBurst       int
}

// IsDev reports whether stack traces and debug routing are appropriate.
// Anything not explicitly production is treated as development, so a blank
// or misspelled APP_ENV never accidentally hides diagnostics.
func (c Config) IsDev() bool { return c.AppEnv != EnvProduction }

// ManagerBootstrapEnabled reports whether a first manager should be created
// at startup when none exists.
func (c Config) ManagerBootstrapEnabled() bool {
	return c.ManagerEmail != "" && c.ManagerPassword != ""
}

// Feature is one optional capability and whether this deployment has it.
type Feature struct {
	Name    string
	Enabled bool
	// Detail says what is missing when Enabled is false, in terms an
	// operator can act on: the variable to set, not the symptom.
	Detail string
}

// Features is the full list of optional capabilities, in a fixed order. It is
// logged at startup and served by /readyz.
//
// This list is the contract. If a capability can be switched off by
// configuration it appears here — so "the feature was quietly unmounted and
// nobody noticed" has exactly one place to look.
func (c Config) Features() []Feature {
	return []Feature{
		{
			Name:    "manager_bootstrap",
			Enabled: c.ManagerBootstrapEnabled(),
			Detail:  "MANAGER_EMAIL/MANAGER_PASSWORD are not both set — no first manager is created, and a fresh database has nobody who can log in",
		},
		{
			Name:    "rate_limiting",
			Enabled: c.RateLimitEnabled,
			Detail:  "RATE_LIMIT_ENABLED=false — login, registration and booking accept unlimited requests",
		},
		{
			Name:    "demo_console",
			Enabled: c.DemoConsoleEnabled,
			Detail:  "DEMO_CONSOLE=false — GET /demo is not mounted",
		},
	}
}

// Validate checks every required setting and reports all failures together.
func (c Config) Validate() error {
	var problems []string

	require := func(val, name string) {
		if strings.TrimSpace(val) == "" {
			problems = append(problems, name+" is required")
		}
	}

	require(c.MongoURI, "MONGO_URI")
	require(c.MongoDB, "MONGO_DB")
	require(c.AppPort, "PORT (or APP_PORT)")

	// Checked here rather than at token.NewMaker so a short secret is
	// reported alongside every other configuration problem, in one pass.
	if strings.TrimSpace(c.TokenSecret) == "" {
		problems = append(problems, "TOKEN_SECRET is required")
	} else if len(c.TokenSecret) < 32 {
		problems = append(problems, "TOKEN_SECRET must be at least 32 characters")
	}

	if c.TokenExpiry < 1 {
		problems = append(problems, "TOKEN_EXPIRY_HOURS must be at least 1")
	}

	// A bootstrap password that is set but unusable would fail at startup
	// and leave a deployment nobody can log into, with nothing saying why.
	if c.ManagerEmail != "" && c.ManagerPassword != "" && len(c.ManagerPassword) < 8 {
		problems = append(problems, "MANAGER_PASSWORD must be at least 8 characters")
	}

	// A zero or negative step would make the availability loop produce no
	// slots at best and spin at worst.
	if c.SlotStepMin < 5 {
		problems = append(problems, "SLOT_STEP_MIN must be at least 5")
	}
	if c.MaxBookingDaysAhead < 1 {
		problems = append(problems, "MAX_BOOKING_DAYS_AHEAD must be at least 1")
	}

	// Load left Location nil when the name did not resolve. Reported here so
	// it arrives with every other configuration problem instead of panicking
	// on the first report request.
	if c.Location == nil {
		problems = append(problems, "TIMEZONE="+c.Timezone+" is not a known IANA zone name (e.g. Asia/Ulaanbaatar)")
	}

	// The demo console is unauthenticated by design. Refusing the
	// combination outright rather than warning: the whole point of the
	// startup-visibility work is that a dangerous setting should not be
	// something you find out about from a log line that scrolled away.
	if c.AppEnv == EnvProduction && c.DemoConsoleEnabled {
		problems = append(problems, "DEMO_CONSOLE must be false when APP_ENV=production")
	}

	if len(problems) > 0 {
		return errors.New("invalid configuration: " + strings.Join(problems, "; "))
	}
	return nil
}

func Load() *Config {
	env := Env(getEnv("APP_ENV", string(EnvDevelopment)))

	// A failure here leaves Location nil, which Validate reports by name.
	// Falling back to UTC instead would start the service with every report
	// silently cut at the wrong hour.
	tz := getEnv("TIMEZONE", "Asia/Ulaanbaatar")
	loc, _ := time.LoadLocation(tz)

	return &Config{
		Timezone: tz,
		Location: loc,

		AppEnv: env,
		// PORT takes precedence over APP_PORT because hosting platforms
		// inject PORT and require the app to bind to it.
		AppPort:     getEnv("PORT", getEnv("APP_PORT", "8090")),
		MongoURI:    getEnv("MONGO_URI", "mongodb://localhost:27017"),
		MongoDB:     getEnv("MONGO_DB", "carwash"),
		TokenSecret: getEnv("TOKEN_SECRET", ""),
		TokenExpiry: getEnvInt("TOKEN_EXPIRY_HOURS", 12),

		ManagerName:     getEnv("MANAGER_NAME", ""),
		ManagerEmail:    getEnv("MANAGER_EMAIL", ""),
		ManagerPassword: getEnv("MANAGER_PASSWORD", ""),

		DemoConsoleEnabled: getEnvBool("DEMO_CONSOLE", env != EnvProduction),

		SlotStepMin:         getEnvInt("SLOT_STEP_MIN", 15),
		MaxBookingDaysAhead: getEnvInt("MAX_BOOKING_DAYS_AHEAD", 30),

		CORSOrigins: getEnvList("CORS_ORIGINS"),

		RateLimitEnabled:     getEnvBool("RATE_LIMIT_ENABLED", true),
		AuthRatePerMinute:    getEnvInt("AUTH_RATE_PER_MINUTE", 10),
		BookingRatePerMinute: getEnvInt("BOOKING_RATE_PER_MINUTE", 30),
		RateLimitBurst:       getEnvInt("RATE_LIMIT_BURST", 5),
	}
}

// getEnv trims the value it reads. A .env file saved with CRLF line endings
// yields values like "mongodb://...\r", which look correct in an editor and
// fail every connection attempt with an error naming neither the file nor
// the character.
func getEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// getEnvList reads a comma-separated list, dropping blanks. Returns nil for
// an unset variable, which middleware.CORS reads as "no cross-origin access"
// rather than as "allow everything".
func getEnvList(key string) []string {
	raw := getEnv(key, "")
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getEnvInt(key string, fallback int) int {
	if v := getEnv(key, ""); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v := getEnv(key, ""); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}
