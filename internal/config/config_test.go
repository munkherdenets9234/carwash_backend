package config

import (
	"strings"
	"testing"
	"time"
)

func valid() Config {
	loc, _ := time.LoadLocation("Asia/Ulaanbaatar")
	return Config{
		AppEnv:              EnvDevelopment,
		AppPort:             "8090",
		MongoURI:            "mongodb://localhost:27017",
		MongoDB:             "carwash",
		TokenSecret:         strings.Repeat("k", 32),
		TokenExpiry:         12,
		SlotStepMin:         15,
		MaxBookingDaysAhead: 30,
		Timezone:            "Asia/Ulaanbaatar",
		Location:            loc,
	}
}

func TestValidateAcceptsAWorkingConfig(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	// The whole point of collecting problems: bringing up a new environment
	// should take one run, not one restart per missing variable.
	c := valid()
	c.MongoURI = ""
	c.MongoDB = ""
	c.TokenSecret = ""
	c.TokenExpiry = 0

	err := c.Validate()
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"MONGO_URI", "MONGO_DB", "TOKEN_SECRET", "TOKEN_EXPIRY_HOURS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}

func TestValidateRejectsWeakSettings(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"short token secret", func(c *Config) { c.TokenSecret = "tooshort" }, "at least 32 characters"},
		{"short bootstrap password", func(c *Config) {
			c.ManagerEmail = "m@example.mn"
			c.ManagerPassword = "short"
		}, "MANAGER_PASSWORD"},
		{"tiny slot step", func(c *Config) { c.SlotStepMin = 1 }, "SLOT_STEP_MIN"},
		{"no booking window", func(c *Config) { c.MaxBookingDaysAhead = 0 }, "MAX_BOOKING_DAYS_AHEAD"},
		{"unknown timezone", func(c *Config) { c.Location = nil; c.Timezone = "Mars/Olympus" }, "Mars/Olympus"},
		{
			// The demo console is unauthenticated. Refused outright rather
			// than warned about, because a warning in a log nobody reads is
			// not a control.
			"demo console in production",
			func(c *Config) { c.AppEnv = EnvProduction; c.DemoConsoleEnabled = true },
			"DEMO_CONSOLE",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := valid()
			tc.mutate(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("want an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestFeaturesNamesEveryOptionalCapability(t *testing.T) {
	c := valid()
	features := c.Features()
	if len(features) == 0 {
		t.Fatal("Features must not be empty — it is what /readyz reports")
	}

	for _, f := range features {
		if f.Name == "" {
			t.Error("a feature has no name")
		}
		// A disabled feature with no detail tells an operator that
		// something is off but not what to set, which is the failure this
		// list exists to prevent.
		if !f.Enabled && f.Detail == "" {
			t.Errorf("feature %q is disabled with no detail", f.Name)
		}
	}
}

func TestManagerBootstrapNeedsBothHalves(t *testing.T) {
	c := valid()
	if c.ManagerBootstrapEnabled() {
		t.Error("blank credentials must not enable the bootstrap")
	}
	c.ManagerEmail = "m@example.mn"
	if c.ManagerBootstrapEnabled() {
		t.Error("an email without a password must not enable the bootstrap")
	}
	c.ManagerPassword = "manager123"
	if !c.ManagerBootstrapEnabled() {
		t.Error("both halves set must enable the bootstrap")
	}
}

func TestIsDevTreatsAnythingButProductionAsDevelopment(t *testing.T) {
	// A blank or misspelled APP_ENV must not silently hide diagnostics.
	for _, env := range []Env{EnvDevelopment, EnvTest, "", "prod", "Production"} {
		c := valid()
		c.AppEnv = env
		if !c.IsDev() {
			t.Errorf("APP_ENV=%q was treated as production", env)
		}
	}
	c := valid()
	c.AppEnv = EnvProduction
	if c.IsDev() {
		t.Error("APP_ENV=production must not be development")
	}
}
