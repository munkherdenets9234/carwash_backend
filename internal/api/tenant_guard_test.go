package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/eandstravel/carwash/internal/entitlement"
	"github.com/eandstravel/carwash/pkg/apierr"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// These cover the gate that adoption into the platform added: which business
// a request belongs to, and whether that business has bought this product.
//
// Every one of them is about a failure mode, because the failure modes are
// the design. A tenant gate that works when the platform is up and refuses
// everyone when it is down has turned one service's outage into every
// product's outage, which is worse than the arrangement it replaced.

const testModule = "carwash"

const (
	keyEntitled    = "sk_live_entitled"
	keyNoModule    = "sk_live_nomodule"
	keyLapsed      = "sk_live_lapsed"
	keyUnknown     = "sk_live_unknown"
	keyUnreachable = "sk_live_unreachable"
)

var tenantA = primitive.NewObjectID()

// stubPlatform stands in for tenantcore. Each key selects one of the answers
// the real platform can give, including the two that are not answers at all.
type stubPlatform struct{}

func (stubPlatform) Available() bool { return true }

func (stubPlatform) ForKey(_ context.Context, key string) (entitlement.Entitlement, error) {
	base := entitlement.Entitlement{
		TenantID:  tenantA,
		Status:    entitlement.StatusActive,
		PeriodEnd: time.Now().Add(24 * time.Hour),
		Modules:   []string{testModule},
	}
	switch key {
	case keyEntitled:
		return base, nil
	case keyNoModule:
		base.Modules = []string{"travel"}
		return base, nil
	case keyLapsed:
		base.Status = entitlement.StatusPastDue
		base.PeriodEnd = time.Now().Add(-24 * time.Hour)
		return base, nil
	case keyUnknown:
		return entitlement.Entitlement{}, entitlement.ErrUnknownTenant
	default:
		// Everything else: the platform could not be reached and nothing was
		// cached for this tenant.
		return entitlement.Entitlement{}, errors.New("platform unreachable")
	}
}

func call(t *testing.T, method, path, apiKey string) *httptest.ResponseRecorder {
	t.Helper()
	engine := testServer(t).Handler()
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func codeOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code   string `json:"code"`
			Domain string `json:"domain"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v (body %s)", err, rec.Body.String())
	}
	return env.Error.Code
}

// A request with no tenant key cannot be served at all: there is no longer a
// single business for an unscoped request to mean.
func TestNoAPIKeyIsRefused(t *testing.T) {
	rec := call(t, http.MethodGet, "/api/v1/services", "")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401\nbody: %s", rec.Code, rec.Body.String())
	}
	if got := codeOf(t, rec); got != apierr.CodeUnauthorized {
		t.Errorf("code = %q, want %q", got, apierr.CodeUnauthorized)
	}
}

// Even the price list — the most public thing this service has — is scoped
// now. It is one business's price list, and without a key there is no way to
// know whose.
func TestFormerlyPublicRoutesStillNeedATenantKey(t *testing.T) {
	for _, path := range []string{"/api/v1/services", "/api/v1/locations"} {
		if rec := call(t, http.MethodGet, path, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: got %d without a key, want 401", path, rec.Code)
		}
	}
}

func TestUnknownAPIKeyIsRefused(t *testing.T) {
	rec := call(t, http.MethodGet, "/api/v1/services", keyUnknown)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401 for a key the platform does not know", rec.Code)
	}
}

// A paying tenant on a plan without this product is one plan change away, so
// 402 with a code a client can turn into an upgrade prompt — not 403, which
// says "never", and not 404, which says "you imagined this".
func TestPlanWithoutTheModuleIsRefusedWithAnUpgradeCode(t *testing.T) {
	rec := call(t, http.MethodGet, "/api/v1/services", keyNoModule)

	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("got %d, want 402\nbody: %s", rec.Code, rec.Body.String())
	}
	if got := codeOf(t, rec); got != apierr.CodeModuleNotEntitled {
		t.Errorf("code = %q, want %q", got, apierr.CodeModuleNotEntitled)
	}
}

// The subscription gate's deliberate asymmetry: a lapsed tenant can still
// READ. They must be able to see their data and reach the screen that
// explains the bill; cutting reads turns a billing problem into "the app is
// broken" and produces a support ticket instead of a payment.
func TestALapsedSubscriptionStillReads(t *testing.T) {
	rec := call(t, http.MethodGet, "/api/v1/services", keyLapsed)

	if rec.Code == http.StatusPaymentRequired {
		t.Fatalf("a read was refused for a lapsed subscription; reads must stay open")
	}
	// It gets past both gates and reaches a controller backed by a nil
	// service, which panics into a 500. That is this test suite's signal for
	// "the gates let it through" — see the note in testServer.
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, expected the request to reach a controller", rec.Code)
	}
}

// ...and cannot WRITE.
func TestALapsedSubscriptionCannotWrite(t *testing.T) {
	rec := call(t, http.MethodPost, "/api/v1/auth/register", keyLapsed)

	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("got %d, want 402 for a mutating call on a lapsed subscription\nbody: %s",
			rec.Code, rec.Body.String())
	}
	if got := codeOf(t, rec); got != apierr.CodeSubscriptionRequired {
		t.Errorf("code = %q, want %q", got, apierr.CodeSubscriptionRequired)
	}
}

// The most important one. When the platform cannot be reached and nothing is
// cached, the answer is "we could not find out" — a 503 — never "you have not
// paid". Getting this wrong would make one service's outage look, to every
// customer of every product, like a billing failure of their own.
func TestAnUnreachablePlatformIsNotADenial(t *testing.T) {
	rec := call(t, http.MethodGet, "/api/v1/services", keyUnreachable)

	if rec.Code == http.StatusPaymentRequired {
		t.Fatal("an unreachable platform was reported as unpaid — this is the failure mode the design exists to prevent")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}
	if got := codeOf(t, rec); got != apierr.CodeUpstream {
		t.Errorf("code = %q, want %q", got, apierr.CodeUpstream)
	}
}

// The operational routes must stay reachable without any of this. A health
// check that needed a tenant key could not be run by a monitor, and a
// readiness probe that failed when the platform was down would make an
// orchestrator restart a process that is working exactly as designed.
func TestOperationalRoutesNeedNoTenantKey(t *testing.T) {
	for _, path := range []string{"/healthz", "/readyz", "/docs/api.json"} {
		if rec := call(t, http.MethodGet, path, ""); rec.Code != http.StatusOK {
			t.Errorf("%s: got %d without a key, want 200", path, rec.Code)
		}
	}
}
