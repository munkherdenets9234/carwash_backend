# Adopting the car wash into the core platform

How to take this service from a standalone demo to a product inside
`digitalservice`, and where subscription information comes from once it is.

The seam it plugs into already exists. `digitalservice/internal/entitlement`
was built for exactly this, and its package docs name the car wash as the
worked example — so this is not a design exercise, it is a checklist against
a boundary somebody already drew.

---

## The short version

```go
// digitalservice/internal/bootstrap/bootstrap.go
srv := api.NewServer(api.Deps{
    // ...
    Entitlement: svcs.entitlement,
    Modules: []tenant.Module{{
        Name:     "carwash",
        Register: carwash.Register,   // takes a *gin.RouterGroup
    }},
})
```

That mounts every car wash route at `/api/v1/carwash/*`, behind — in this
order, and with no way to opt out:

1. `TenantMiddleware.Require()` — resolves the tenant from `X-API-Key`
2. `SubscriptionMiddleware.Require()` — refuses mutating calls on a lapsed
   subscription
3. `RequireModule(entitlement, "carwash")` — refuses a tenant whose plan
   does not include this product

`Module.Name` is both the URL segment and the entitlement key, deliberately:
one string means a plan that grants `"carwash"` and a route tree mounted at
`/carwash` cannot drift apart.

Registering the module is the last step, not the first. Everything below is
what has to be true before that line is safe.

---

## Where subscription information comes from

### For code: the entitlement provider

Never read the `subscriptions` collection from product code. Ask the
provider:

```go
// digitalservice/internal/entitlement
type Provider interface {
    For(ctx context.Context, tenantID primitive.ObjectID) (Entitlement, error)
}

type Entitlement struct {
    TenantID  primitive.ObjectID
    Status    Status            // active | trialing | past_due | canceled | ""
    PeriodEnd time.Time         // zero means no period is tracked
    Modules   []string          // which products
    Limits    map[string]int    // numeric ceilings, product-defined keys
    Features  map[string]bool   // on/off grants, product-defined keys
    Stale     bool              // this answer came from a cache
}
```

Helpers, all on `Entitlement`: `Active()`, `HasModule(name)`,
`Limit(key) (int, bool)`, `Within(key, current)`, `Feature(key)`.

Today the only implementation is `service.EntitlementService`, which reads
the tenant's `Subscription` and its `Package` from this service's own
database, in process. When the manage-tenant service exists, that one
implementation is replaced by an HTTP client with a cache — **and nothing
that calls the provider changes.** That is the whole reason the interface
exists before it is strictly needed.

Inside a handler, the entitlement the gate already fetched is on the request
context — do not fetch it twice.

### For humans and other services: the HTTP endpoints

| What | Endpoint |
|---|---|
| One tenant's subscription, with its resolved package | `GET /api/v1/platform/tenants/{id}/subscription` |
| Packages assigned to a tenant | `GET /api/v1/platform/tenants/{id}/packages` |
| The plan catalogue | `GET /api/v1/platform/packages`, `GET /api/v1/platform/packages/{id}` |
| Provision a subscription | `POST /api/v1/platform/tenants/{id}/subscription` |
| Move a tenant to another plan | `PUT /api/v1/platform/tenants/{id}/subscription/package` |
| Cancel | `POST /api/v1/platform/tenants/{id}/subscription/cancel` |

The reads are currently on the **public** platform surface and the writes
require a platform superadmin. Two things to know before relying on that:

- `GET /platform/tenants` and `GET /platform/admins` are unauthenticated and
  return contact details — a known, recorded exposure in digitalservice, not
  something to build on. If the car wash needs subscription data over HTTP,
  treat those read endpoints as *about to become authenticated*.
- `Package.Modules`, `Package.Limits` and `Package.Capabilities` are all
  `json:"-"`. They are **not** in the public package response, on purpose:
  the plan's enforcement fields are not marketing copy. Read them through
  the provider, or add an authenticated platform route if a console needs to
  edit them.

### Three rules that are load-bearing

These are encoded in the package docs. Breaking any of them converts a
working boundary into an outage.

1. **A lookup failure is "we could not find out", never "no."** The gate
   returns 500 when the provider errors, never 402. Deciding "no" would make
   the entitlement source a single point of failure for every product at
   once — strictly worse than the monolith the split replaces.
2. **The platform stores the number, the product decides what it means.**
   `Limits` is an opaque `map[string]int`. Billing must not learn that
   `carwash.locations` is a branch, or every new car wash feature needs a
   coordinated two-repository deploy.
3. **`HasModule` returns `true` for an empty module list.** A migration
   affordance, because every plan today predates modules and a strict reading
   would 402 every live tenant the moment the first gate mounts. It is safe
   only while no plan lists modules. **The moment you add `["carwash"]` to
   one plan, tighten it to `false`** — otherwise every plan that has not been
   updated silently grants the car wash to everyone.

That last one is the trap in this whole exercise. Step 6 below is where it
gets closed, and it is not optional.

---

## The work, in the order it has to happen

### 1. Make every collection tenant-scoped

This is the large change, and it is the reason adoption is not a one-line
job. **The car wash service has no concept of a tenant.** Its seven
collections are single-business:

```
users  cars  locations  wash_services  shifts  reservations  time_entries
```

Each needs `tenant_id`, and — more importantly — every index that is unique
today must become unique *per tenant*:

| Today | Must become |
|---|---|
| `users.email` unique | `(tenant_id, email)` unique |
| `cars.(owner_id, plate)` unique | unchanged — `owner_id` already implies the tenant |
| `reservations.slot_key` unique sparse | prefix `slot_key` with the tenant id |
| `time_entries.open_key` unique sparse | prefix `open_key` with the tenant id |

The two derived keys matter more than they look. `slot_key` is
`employeeID|startInstant` and `open_key` is the employee id; both are already
scoped by an employee who belongs to one tenant, so they are *correct*
without a prefix — but the index is global, and a collision across tenants
would be a refusal one tenant cannot explain. Prefix them and the reasoning
stays local.

Every repository method then takes the tenant id **in the filter**, not as a
post-read comparison. The existing `FindByIDForOwner` / `FindByIDForCustomer`
pattern is already the right shape; extend it rather than adding a
`tenantID` parameter that a call site can forget to use.

### 2. Decide whose users these are

The sharpest decision, and it has to be made before any code moves.

`digitalservice` already has `tenant_users` (staff who log into the admin
panel) and `customers` (a tenant's end customers). The car wash has its own
`users` collection with three roles.

**Recommended: keep the car wash's own `users`, scoped by tenant.** Its role
model (manager / employee / customer) is the product's, not the platform's,
and mapping `employee` onto a `tenant_user` with some role string loses the
`EmployeeProfile` (home location, hire date) that the roster depends on. The
platform's `tenant_users` continue to authenticate the *admin console*; the
car wash authenticates *its own* three audiences.

The cost, stated plainly: a tenant's staff member who is both a brochure
admin and a car wash manager has two accounts. That is worth accepting for
now and worth revisiting when the manage-tenant service owns identity.

**What does change:** the car wash's `/auth/login` moves behind the tenant
resolution too, so a token is issued against one tenant and cannot be
replayed at another. Its middleware must compare the token's tenant to the
resolved tenant, exactly as `digitalservice/internal/middleware/auth.go`
already does.

### 3. Rename what collides

Route prefixing is not cosmetic here. `/cars` means "a rental fleet vehicle"
in digitalservice and "the customer's own car" in the car wash. The module
prefix keeps them apart at the URL, but:

- The Go package name `models.Car` collides. Adopt the car wash models as
  `models.WashCar`, or keep them in their own `internal/carwash/models`
  package — the second is cleaner and makes the later extraction trivial.
- `models.Customer` exists in both, meaning different things.
- `internal/service.CarService` collides.

Put the whole product under `internal/carwash/` with its own `models`,
`repository`, `service` and `api` sub-packages. It reads as a module rather
than as code sprayed through someone else's layers, and when the split comes
it lifts out whole.

### 4. Reconcile the error taxonomies

Both services have `pkg/apierr` with the same shape and *different* code
sets. The union is what clients see, so it has to be deliberate:

| From the car wash | Keep |
|---|---|
| `OUTSIDE_GEOFENCE` (422) | Yes — no platform equivalent |
| `SLOT_UNAVAILABLE` (409) | Yes |
| Domains `CATALOG`, `SCHEDULE`, `RESERVATION`, `ATTENDANCE` | Yes |

| From the platform | The car wash gains |
|---|---|
| `SUBSCRIPTION_REQUIRED` (402) | On every mutating call |
| `MODULE_NOT_ENTITLED` (402) | On every call |
| `LIMIT_EXCEEDED` (402) | On creates, once step 5 is done |

Clients must handle 402 after adoption and do not need to before. That is a
breaking change for any existing car wash client, which is why the frontend
work in step 8 is not optional.

### 5. Define the plan keys, and enforce the limits

Pick the keys the product will define, and write them down before they are
used — an undocumented limit key is a number in a database nobody can
explain:

```json
{
  "modules": ["carwash"],
  "limits": {
    "carwash.locations": 3,
    "carwash.employees": 12,
    "carwash.services": 20
  },
  "capabilities": {
    "carwash.multi_site": true,
    "carwash.bonus_reporting": true
  }
}
```

Limits are checked **in the service layer at the write**, not in middleware.
Middleware sees a request; it does not see a row count. The shape is:

```go
// internal/carwash/service/catalog_service.go
func (s *CatalogService) CreateLocation(ctx context.Context, in LocationInput) (*models.Location, error) {
    ent := apictx.Entitlement(ctx)          // put there by RequireModule
    current, err := s.locations.CountForTenant(ctx, tenantID)
    if err != nil {
        return nil, apierr.Internal(err)
    }
    if !ent.Within("carwash.locations", current) {
        return nil, apierr.LimitExceeded("carwash.locations")
    }
    // ...
}
```

Note `Within` returns **true** when the plan sets no ceiling: absent means
unlimited, not zero. A plan that forgot to mention `locations` and a plan
that grants zero are different situations, and collapsing them ships the one
where a missing key forbids everything.

### 6. Close the `HasModule` default

Once any plan lists `"carwash"` in `modules`:

1. Add `modules` to **every** plan in the catalogue. A plan with an empty
   list is now an unfinished plan, not an unlimited one.
2. Change `entitlement.HasModule` to `return false` for an empty list, and
   delete the paragraph in its doc comment that explains the affordance.
3. Re-run the platform's `guard_test.go`, which already asserts a module
   route is reachable only with tenant + subscription + module.

Doing 1 without 2 leaves the car wash granted to every tenant who has not
been migrated. Doing 2 without 1 402s all of them. They go together, in one
change, behind one deploy.

### 7. Move the data

The demo's data is one business with no tenant. If any of it is worth
keeping:

1. Create the tenant, then a subscription on a plan whose `modules` include
   `carwash`.
2. Backfill `tenant_id` on all seven collections to that id.
3. Rebuild the derived keys — `slot_key` and `open_key` — with the tenant
   prefix, in the same migration. A stale key is a unique index that refuses
   a correct booking.
4. Create the new compound indexes **before** dropping the old single-field
   ones, so uniqueness is never briefly unenforced.

Write it as a numbered migration and record in its comment what it actually
moved — counts, not intentions. The migration comment is the only place a
future reader learns whether the backfill covered everything.

### 8. Update the clients

`carwash-web` needs, in one change:

- Every BFF path gains the `/carwash` prefix: the proxy at
  `src/app/api/bff/[...path]/route.ts` changes its target from
  `${API_BASE}/${path}` to `${API_BASE}/carwash/${path}`. One line, because
  the prefix was never in the screens.
- The `X-API-Key` header on every forwarded request, read from server-side
  config — never `NEXT_PUBLIC_`, or the tenant key ends up in the page
  source. (digitalservice's storefronts already publish theirs; do not copy
  that.)
- A 402 handler. `MODULE_NOT_ENTITLED` and `SUBSCRIPTION_REQUIRED` need a
  real screen — "this feature is not on your plan" — not a toast. Add it
  alongside `ForbiddenState` in `src/components/app/states.tsx`.
- `hasCode(err, "LIMIT_EXCEEDED")` on the create paths, so "you have used
  all 3 branches on your plan" reads as a plan limit rather than a bug.

### 9. Verify

In this order, because each one can pass while the next fails:

```bash
cd digitalservice && make test          # guard_test covers the module gate
cd carwash        && go test ./...      # includes the OpenAPI coverage tests
cd carwash-web    && npm run build && npm run flows
```

Then, against a real database, assert by hand what no unit test can:

- A tenant on a plan **without** `carwash` gets 402 `MODULE_NOT_ENTITLED` on
  every car wash route.
- A tenant on a plan **with** it, whose subscription has lapsed, can still
  **read** and cannot **write** — that is the subscription gate's deliberate
  asymmetry, so a lapsed tenant can still see their data and reach the page
  that fixes their billing.
- Tenant A's manager cannot see tenant B's bookings, staff, or day report.
  Add this to `carwash/scripts/flows.py` as a two-tenant case; it is the
  check that matters most after adoption and the one nothing currently
  covers.
- Stopping the entitlement source produces **500s, not 402s**. If it 402s,
  rule 1 has been broken somewhere and a platform outage now reads to every
  tenant as "you have not paid".

---

## The other path: stay standalone

If the car wash ships as its own deployment first, the boundary is the same
and only the transport changes:

1. Vendor `entitlement.Entitlement` as a wire type — it imports nothing from
   the rest of digitalservice precisely so it can travel.
2. Implement `Provider` as an HTTP client against the platform, with a cache
   that sets `Stale: true` when it answers from last-known state.
3. Surface `Stale` on `/readyz` and alert on it. `Entitlement.Stale` exists
   already so the cached client needs no struct change — and a degraded
   entitlement lookup that nobody is told about is the failure mode the field
   was added to prevent.
4. Everything else in this document still applies: tenant scoping, the plan
   keys, the limit checks, the 402 handling.

This is the cheaper first move and the one that keeps the products
independently deployable. Mounting in-process is the cheaper *second* move if
the platform and the product turn out to deploy together anyway.

---

## The contract

`docs/api.json` is the OpenAPI 3.1 description of this service, served live
at `GET /docs/api.json` so an integrating team fetches it rather than being
sent a copy that goes stale in their repository.

`internal/api/openapi_test.go` asserts the spec and the router agree **in
both directions** — a new endpoint fails the build until it is documented,
and a documented path that no longer exists fails until it is removed. A
route genuinely outside the contract goes in the `undocumented` map with its
reason. That test is why the spec can be trusted; a hand-written spec without
one is a wish.

After adoption every path in it gains the `/carwash` prefix and the
`X-API-Key` header, and the 402 codes above join the error enum. Nothing else
about the contract changes — which is the point of having mounted it behind a
module boundary rather than merging it into the platform's own routes.
