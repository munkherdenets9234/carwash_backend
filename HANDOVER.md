# Handover

You are picking this up mid-migration: the car wash demo went multi-tenant
against a sibling platform service, `tenantcore`, and this document is the
state of that migration as of this commit — not the plan for it. For
architecture, the seven features, and what the service deliberately doesn't
do, read [`README.md`](README.md) first. For the tenant-adoption design (why
the boundary is where it is, what the entitlement provider guarantees, the
order the platform's own gates run in) read
[`docs/ADOPTING-INTO-CORE.md`](docs/ADOPTING-INTO-CORE.md) — most of it
describes the in-process future, but "Where subscription information comes
from" and the three load-bearing rules apply exactly as written to the
standalone deployment this repo actually is today.

This document only states what was verified against the code at the time of
writing. Where something couldn't be verified without infrastructure this
session didn't have (a live `tenantcore`, a seeded Atlas database), it says
so instead of guessing.

## Running it locally

Env vars: [`.env.example`](.env.example) is the source of truth, and
`internal/config/config.go` validates all of it at startup — a missing
required var stops the process with every problem listed at once, not one
restart at a time. The two blocks worth reading before you copy it:

- **Required**: `MONGO_URI`, `MONGO_DB`, `TOKEN_SECRET` (32+ chars). Missing
  any of these and the process refuses to start.
- **The platform link**: `TENANTCORE_URL` and `TENANTCORE_SERVICE_KEY`.
  Leave them blank and the process still boots and still serves `/healthz`
  and `/readyz` — every `/api/v1` route then answers `503
  FEATURE_UNAVAILABLE` instead. **This means you cannot exercise anything
  under `/api/v1` without a running `tenantcore`.** There is no local
  fallback and no flag to fake one; see "The tenant integration" below.

**Port collision to watch for**: this service's default port
(`PORT`, falls back to `APP_PORT`, falls back to `8090` — see
`config.go:288`) and tenantcore's default port (`APP_PORT`, also `8090`) are
the same value. `.env.example` here even ships `TENANTCORE_URL=http://localhost:8090`
as its example. If you run both services locally with their respective
`.env.example` defaults untouched, one of them has to move — set this
service's `PORT` to something else (`8091` is fine) and leave
`TENANTCORE_URL` pointing at tenantcore's `8090`, or vice versa. Confirmed by
reading both services' config code, not by trusting old notes — check again
if either config.go changes.

Once tenantcore is reachable, you also need, from it:

- A service key for this product (`POST /api/v1/admin/service-clients` on
  tenantcore, per its own README) → `TENANTCORE_SERVICE_KEY` here.
- A tenant, and an API key for it → this is the `X-API-Key` every request
  needs (see below). tenantcore owns provisioning both; this repo has no
  UI or script for it.

With that in place:

```bash
cp .env.example .env
# fill in MONGO_URI, TOKEN_SECRET, TENANTCORE_URL, TENANTCORE_SERVICE_KEY,
# BOOTSTRAP_TENANT_ID (the tenant id from tenantcore, hex ObjectId)
make seed      # every seeded row is tagged with BOOTSTRAP_TENANT_ID
make run
```

Seeded demo accounts are listed in `README.md` — that table hasn't moved,
don't duplicate it here. What's worth adding: reaching any of them now also
requires the `X-API-Key` for `BOOTSTRAP_TENANT_ID`'s tenant on every request,
including the ones the demo console at `/demo` makes — if `/demo` was built
or last touched before adoption, check whether it actually sends that header
before assuming it still works out of the box.

## The tenant integration, as it stands today

Two headers, two different questions, both required on every `/api/v1`
route:

- **`X-API-Key`** (the spec calls the security scheme `tenantKey`) — *which
  business* the request is for. Issued by tenantcore, resolved by this
  service's `internal/middleware/tenant.go` (`Tenant.Require()`), which runs
  **before** the bearer-auth middleware for exactly the reason
  `ADOPTING-INTO-CORE.md` gives: a bearer token is issued against one
  tenant and means nothing until the tenant is known.
- **`Authorization: Bearer <token>`** — *which person* within that business.
  Unrelated credential; neither substitutes for the other.

**There is no local `tenants` collection.** Confirmed by grepping
`internal/repository` — nothing there models a tenant. Resolving the API key
*is* tenant resolution: `entitlement.Client.ForKey` (see below) is the only
place this service learns a tenant exists, and the same call returns the
tenant's name, subscription status and module/limit entitlement together,
because tenantcore answers all of that from one lookup. This service keeps
no copy of anything tenantcore owns.

The client-side caching and outage behavior lives in
**`internal/entitlement/client.go`** (`entitlement.Client`), consumed
through the `EntitlementSource` interface in
**`internal/middleware/tenant.go`**. The behavior that matters:

- An answer is cached for `ENTITLEMENT_TTL` (default 60s), then refetched.
- If tenantcore is unreachable past the TTL, a cached answer is still served,
  stale, for up to `ENTITLEMENT_GRACE` (default 15m) — with `Stale: true`
  and a warning logged once (not once per request; see `logOnce`).
- Past the grace window with nothing fresh, or with *nothing at all* cached
  for that key, the request gets **503**, `error.code = UPSTREAM_ERROR` —
  **never 402.** An unreachable platform is "we could not find out," not
  "not entitled," and the test that pins this down is
  `TestAnUnreachablePlatformIsNotADenial` in
  `internal/api/tenant_guard_test.go`. If you're ever tempted to make an
  entitlement-lookup failure a 402 "just for this one case," read that test
  and its comment first.
- An *unknown* key (tenantcore recognizes the request as bad, not merely
  unreachable) is the one case that **is** a real denial: 401, cache
  untouched, nothing served from stale state — see `ErrUnknownTenant`
  handling in `client.go`.

`carwash-web` (the Next.js frontend, sibling directory) already has its side
of this done, not just planned: its BFF proxy
(`src/app/api/bff/[...path]/route.ts`) attaches `X-API-Key` server-side from
`TENANT_API_KEY` (never `NEXT_PUBLIC_`), and `src/lib/api/client.ts` /
`src/components/app/states.tsx` handle `MODULE_NOT_ENTITLED`,
`SUBSCRIPTION_REQUIRED` and `LIMIT_EXCEEDED` with a real screen, not a toast.
If you're relying on `ADOPTING-INTO-CORE.md`'s step 8 checklist to judge
what's left on the frontend, it's stale in the optimistic direction — this
part is already done. Verified by reading `client.ts`, `states.tsx` and
`gallery-screen.tsx` directly.

## Verification

- **`make test`** — zero setup. No Docker, no database, no network. Runs
  `go vet` plus everything under `go test ./internal/... ./pkg/...`:
  route auth gates (`guard_test.go`, `tenant_guard_test.go`), config
  validation, slot arithmetic, report arithmetic, geofence math, and the
  three OpenAPI-router-parity tests (see below). Run this constantly; it's
  the one an engineer with nothing but a Go toolchain can run today.
- **`make flows`** — needs a live server (`make run`) and a seeded, reachable
  MongoDB (Atlas or otherwise) *and* a reachable tenantcore, because the
  server it drives now 401s/503s every route without one. **Check this
  before relying on it**: `scripts/flows.py`'s `call()` helper sends only an
  `Authorization` header — it does not send `X-API-Key` anywhere in the
  file (confirmed by grep; zero matches for `X-API-Key` or `tenant` in
  `scripts/flows.py`). Against today's router, where the tenant gate runs
  first on every `/api/v1` route, every one of that script's ~60 checks
  should fail closed with 401 before it reaches the check it's meant to be
  testing. This wasn't run live this session (no seeded Atlas database was
  available), so treat it as a strong prediction from reading the code, not
  a confirmed repro — but budget for `flows.py` needing an `X-API-Key`
  parameter and a header on every `call()` before it's useful again. This is
  exactly the gap `ADOPTING-INTO-CORE.md`'s step 9 anticipated ("add a
  two-tenant case to `flows.py`") — it hasn't been started; the script needs
  to speak tenant at all before it can speak *two* tenants.
- No testcontainers-based integration suite exists (`grep -ri testcontainer`
  across the repo and `go.mod`/`go.sum` returns nothing, and there is no
  `test-integration` Makefile target). README already says this plainly;
  still true.

## Known gaps, verified against current code

- **No automated integration suite.** Still just `make flows`, still a
  script a human runs, and see above — it needs updating for the tenant
  header before it's even that.
- **Per-tenant staff provisioning is still `BOOTSTRAP_TENANT_ID`-only.**
  `internal/bootstrap/bootstrap.go` calls
  `staff.EnsureBootstrapManager(ctx, tenantID, ...)` for exactly one tenant
  id read from config at startup, and `cmd/seed` refuses to run without the
  same var. There's no route or job that provisions a first manager when a
  new tenant buys the module in production — `ADOPTING-INTO-CORE.md` names
  this as a real gap needing a machine-to-machine route from the platform,
  and nothing in this codebase closes it yet.
- **`carwash.locations` (and any other non-gallery limit) is unwired.**
  The only limit actually enforced today is `carwash.gallery_images`, in
  `internal/service/media_service.go`'s `checkLimit` — grepped
  `internal/service` for every `.Limit(`/`.Within(` call site and that's the
  only one. `internal/service/catalog_service.go`'s `CreateLocation` has no
  entitlement check at all. If a plan is ever given a `carwash.locations`
  ceiling in tenantcore, it will not be enforced until this is added — the
  shape to copy is `checkLimit` in `media_service.go`, called from the
  write path per `ADOPTING-INTO-CORE.md`'s step 5.
- **The `HasModule` empty-list default is still open.** `entitlement.go`'s
  `HasModule` still returns `true` for an empty `Modules` list — the
  documented migration affordance. Whether it's safe to leave that way
  depends on whether any tenantcore plan has started listing `"carwash"` in
  its modules yet, which is a fact about tenantcore's data, not this repo;
  this session had no way to check it. Ask before assuming either answer.
- **`carwash-web`'s 402/limit handling is done, not a gap** — see above;
  called out here only because `ADOPTING-INTO-CORE.md` still frames it as
  outstanding work and a reader going off that doc alone would think
  otherwise.

## The API contract

[`docs/api.json`](docs/api.json) (OpenAPI 3.1) is served live at
`GET /docs/api.json`, precisely so an integrating team fetches it instead of
a copy that goes stale in their own repo. Three tests keep it honest —
`TestEveryRouteIsDocumented`, `TestSpecDocumentsNoRouteThatIsGone`,
`TestUndocumentedListIsNotStale` in `internal/api/openapi_test.go` — by
diffing the live router against the spec's paths in both directions; they
run under `make test`. They only check that a path+method exists on both
sides, not that the documented schema matches the Go types — that part was
audited by hand this session (every tenant/entitlement-related operation,
the full error-code enum against `pkg/apierr`, and a sample of request/
response schemas against their Go structs) and found accurate; no changes
were needed. That audit doesn't repeat itself automatically, so the next
person to add a field to a request struct without touching `api.json` is
the next drift — nothing catches that but a human re-reading both sides.
