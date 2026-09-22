package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/eandstravel/carwash/internal/config"
	"github.com/eandstravel/carwash/internal/middleware"
	"github.com/eandstravel/carwash/pkg/token"
	"go.uber.org/zap"
)

// publicRoutes is the complete list of endpoints that may answer without a
// bearer token. Everything else the router mounts must refuse one.
//
// This is an allowlist rather than a denylist on purpose. A new private
// route is covered the moment it is registered, with nothing to remember;
// making a route public, by contrast, requires editing this list, which is
// a change a reviewer sees. The previous arrangement — a test naming the
// routes it checks — silently covers nothing for anything added later.
var publicRoutes = map[string]bool{
	"GET /healthz": true,
	"GET /readyz":  true,
	"GET /demo":    true,
	// The API contract. It describes the shape of the service, not any
	// tenant's data, and a contract you need a token to read is one nobody
	// reads before writing their client.
	"GET /docs/api.json":         true,
	"POST /api/v1/auth/register": true,
	"POST /api/v1/auth/login":    true,
	"GET /api/v1/services":       true,
	"GET /api/v1/locations":      true,
}

func testServer(t *testing.T) *Server {
	t.Helper()

	loc, err := time.LoadLocation("Asia/Ulaanbaatar")
	if err != nil {
		t.Fatalf("tzdata: %v", err)
	}

	cfg := &config.Config{
		AppEnv:              config.EnvTest,
		AppPort:             "0",
		TokenSecret:         strings.Repeat("k", 32),
		TokenExpiry:         1,
		Timezone:            "Asia/Ulaanbaatar",
		Location:            loc,
		SlotStepMin:         15,
		MaxBookingDaysAhead: 30,
		DemoConsoleEnabled:  true,
		// Off so a burst of probes in this test cannot be throttled into a
		// 429 and mistaken for a passing 401.
		RateLimitEnabled: false,
	}

	maker, err := token.NewMaker(cfg.TokenSecret)
	if err != nil {
		t.Fatalf("token maker: %v", err)
	}

	// Every service is nil. Nothing dereferences them, which is the point:
	// if a request reaches a controller this test would panic rather than
	// quietly pass, so a missing guard cannot look like a present one.
	return NewServer(Deps{
		Config: cfg,
		Log:    zap.NewNop(),
		Auth:   middleware.NewAuth(maker, nil),
	})
}

func TestEveryPrivateRouteRefusesAnonymousCallers(t *testing.T) {
	srv := testServer(t)
	engine := srv.Handler()

	routes := engine.Routes()
	if len(routes) < 20 {
		t.Fatalf("only %d routes registered — the router is not fully wired", len(routes))
	}

	checked := 0
	for _, r := range routes {
		key := r.Method + " " + r.Path
		if publicRoutes[key] {
			continue
		}
		checked++

		t.Run(key, func(t *testing.T) {
			req := httptest.NewRequest(r.Method, concretePath(r.Path), strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("answered %d without a token, want 401. Either it is missing an auth guard, "+
					"or it is genuinely public and belongs in publicRoutes.", rec.Code)
			}
		})
	}

	if checked == 0 {
		t.Fatal("no private routes were checked — publicRoutes is probably too broad")
	}
	t.Logf("%d private routes checked, %d public routes allowlisted", checked, len(publicRoutes))
}

func TestPublicRoutesAreAllRegistered(t *testing.T) {
	// The mirror of the test above: an entry in publicRoutes that no longer
	// matches a real route is a hole waiting for a path to be reused, and
	// it would silently exempt that path from the check.
	registered := map[string]bool{}
	for _, r := range testServer(t).Handler().Routes() {
		registered[r.Method+" "+r.Path] = true
	}
	for key := range publicRoutes {
		if !registered[key] {
			t.Errorf("publicRoutes names %q, which the router does not register", key)
		}
	}
}

func TestBadTokenIsRefused(t *testing.T) {
	engine := testServer(t).Handler()

	for _, header := range []string{
		"",
		"garbage",
		"Bearer",
		"Bearer not.a.token",
		// A well-formed HS256 token signed with a different key.
		"Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoiYSIsInJvbGUiOiJtYW5hZ2VyIn0.xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/manager/staff", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Authorization %q gave %d, want 401", header, rec.Code)
		}
	}
}

func TestOperationalRoutesAnswerWithoutAuth(t *testing.T) {
	engine := testServer(t).Handler()

	for _, path := range []string{"/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s gave %d, want 200", path, rec.Code)
		}
	}
}

func TestDemoConsoleIsNotMountedWhenDisabled(t *testing.T) {
	srv := testServer(t)
	srv.deps.Config.DemoConsoleEnabled = false
	engine := NewServer(srv.deps).Handler()

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/demo", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("/demo gave %d with the console disabled, want 404", rec.Code)
	}
}

// concretePath fills in gin's :params with a syntactically valid ObjectID,
// so the request reaches the route rather than 404ing on the pattern. The
// id never resolves to a row, which does not matter: the guard under test
// runs before any lookup.
func concretePath(pattern string) string {
	parts := strings.Split(pattern, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, ":") || strings.HasPrefix(p, "*") {
			parts[i] = "64b7f1c2a4d3e2f1a0b9c8d7"
		}
	}
	return strings.Join(parts, "/")
}
