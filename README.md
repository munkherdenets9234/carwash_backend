# Car Wash — demo service

A working backend for a car wash business: role-based accounts, a roster you
can book against, geofenced time registration, per-wash bonuses tied to the
employee who did the job, and an end-of-day report for the manager.

Go + MongoDB. It is a **demo**: complete and runnable, not hardened for real
customer data. The open questions are listed at the bottom rather than left
implied.

It is built on the same core as `digitalservice` — `pkg/httpx`, `pkg/apierr`,
`pkg/response`, `internal/bootstrap`, and the audience-segmented router —
ported rather than shared, since the two are separate modules.

---

## Running it

```bash
cp .env.example .env
# set TOKEN_SECRET to at least 32 characters:
#   openssl rand -base64 48
make seed      # fills an empty database with a day's worth of data
make run
```

Then open <http://localhost:8090/demo> — a single page that drives every
flow below, including a "use my GPS" button for the clock-in.

Seeded accounts:

| Email | Password | Role |
|---|---|---|
| `manager@carwash.mn` | `manager123` | manager |
| `bat@carwash.mn` | `employee123` | employee |
| `saran@carwash.mn` | `employee123` | employee |
| `temuulen@carwash.mn` | `employee123` | employee |
| `customer@example.mn` | `customer123` | customer |
| `oyuna@example.mn` | `customer123` | customer |

`make test` runs everything that needs no Docker and no database: the auth
gate on every registered route, config validation, the slot arithmetic, the
report arithmetic and the geofence maths.

`make flows` is the other half — it drives every flow below against a running
server and a seeded database and prints a pass/fail line per rule (60-odd of
them), including the ones that are easiest to get quietly wrong: that a
customer's booking carries no `bonus_mnt`, that one customer cannot read
another's car, that a suspended employee's existing token stops working on
the next request, and that clock-in is refused at 3.5 km but allowed at
80 m.

---

## The contract, and adopting this into the platform

- **[`docs/api.json`](docs/api.json)** — OpenAPI 3.1 for every route, served
  live at `GET /docs/api.json` so an integrating team fetches it instead of
  being sent a copy that goes stale in their repo.
  `internal/api/openapi_test.go` asserts the spec and the router agree in
  both directions, so a new endpoint fails the build until it is documented.
- **[`docs/ADOPTING-INTO-CORE.md`](docs/ADOPTING-INTO-CORE.md)** — how this
  becomes a `tenant.Module` inside `digitalservice`, and where subscription
  and entitlement information comes from once it is. The seam it plugs into
  (`digitalservice/internal/entitlement`) already exists and names the car
  wash as its worked example.

---

## The seven features, and where each one lives

**1 · Role-based authentication.** Three roles — manager, employee, customer
— in one `users` collection, since they share a login flow. What differs is
which route group the token may enter. Each group carries its own
`Auth.Require(role)` in `internal/api/{customer,employee,manager}`, applied
to the group rather than checked inside handlers, so a route added to a
package is scoped by construction. `guard_test.go` enumerates the router and
asserts that every route not on a short allowlist refuses an anonymous
caller; it currently checks 37.

The middleware loads the user on each request instead of trusting the
token's claims. That costs one indexed lookup, and it buys suspension that
takes effect now rather than whenever the token happens to expire.

**2 · Availability — who is free.** `GET /api/v1/customer/availability?service_id=&day=`.
Derived from the roster minus the bookings that overlap it, computed on
read. Slots are anchored to the start of each shift and stepped by
`SLOT_STEP_MIN`, and a slot is only offered when the whole service duration
fits. Employees who are working but fully booked come back with an empty
slot list rather than being dropped — "booked solid" and "not in today" are
different answers.

**3 · Timesheets.** `GET /api/v1/employee/timesheet` for an employee's own,
`GET /api/v1/manager/timesheets?employee_id=&from=&to=` for anyone's. Same
response type for both, deliberately: a manager reviewing a disputed shift
should be looking at the record the employee is looking at.

**4 · Time registration, geofenced.** `POST /api/v1/employee/attendance/clock-in`
with `{location_id, lat, lng}`. The radius is per-site
(`geofence_radius_m`), because a forecourt and a bay inside a shopping
centre do not need the same tolerance. Outside it, the call is refused with
`422 OUTSIDE_GEOFENCE` and the measured distance in the message, so the app
can say "you are 240 m away" rather than "invalid request".

Clock-**out** records the distance but never refuses: blocking it would
leave someone who has gone home clocked in indefinitely. At most one open
entry per employee, enforced by a sparse unique index rather than only by a
service check, because a read-then-write races with itself (see the last
bullet at the bottom for why it is sparse and not partial).

**5 · Cars, and the bonus that follows them.** A customer registers vehicles
(`/api/v1/customer/cars`); a booking names one, along with the employee. The
service's `bonus_mnt` is **copied onto the reservation at booking time**, not
joined at report time — so editing the price list never rewrites a bonus
already earned. A manager can reassign an open job
(`PUT /api/v1/manager/reservations/:id/assign`), which is how the bonus
changes hands; a completed job is frozen.

**6 · Daily report.** `GET /api/v1/manager/reports/daily?day=YYYY-MM-DD`:
total washes, revenue, bonus, net, and a row per employee with their washes,
revenue, bonus and hours on site. Grouped on `completed_at`, not booking
time — a wash that ran past midnight belongs to the day it finished.
Employees who worked but sold nothing still appear, with zero washes; hours
and washes are independent figures and neither is derived from the other.

**7 · Booking with a chosen employee.** `POST /api/v1/customer/reservations`
takes `employee_id`. Availability is re-derived at booking time rather than
trusting the slot list the client was shown, and a unique sparse index on
(employee, start instant) makes the database refuse the second of two
simultaneous bookings for the same slot.

---

## Shape

```
cmd/api          entry point: load env, start logger, wire, run
cmd/seed         demo data; refuses a non-empty database without -reset

internal/
  config         every setting, validated as one list; Features() drives /readyz
  bootstrap      wiring; required settings stop the process, optional ones degrade loudly
  models         the documents, with the decisions behind them in comments
  repository     the only place that talks to Mongo; ownership lives in the query
  service        the rules — scheduling and reporting are pure and tested
  view           response types, deliberately different per audience
  api/
    public       login, registration, price list. Nothing else.
    customer     own cars, own bookings, availability
    employee     own jobs, own timesheet, clock in/out
    manager      everyone's everything, and the money
    demo         the single-page console, mounted only when DEMO_CONSOLE=true
pkg/
  apierr         the closed error taxonomy: domain + stable code
  httpx          handlers return errors; one middleware renders them
  response       the one place a response body is written
  geo            haversine, and what counts as a usable coordinate
```

Two conventions carry most of the weight:

- **Handlers return errors.** No handler writes an error response, so the
  envelope cannot drift between endpoints. Clients branch on
  `error.code`, never on the prose.
- **Ownership is part of the query.** `FindByIDForOwner`, `FindByIDForCustomer`
  and the employee-scoped job lookups take the caller's id and put it in the
  filter. The version that fetches by id and compares afterwards works until
  one call site forgets, and that call site is an IDOR.

---

## What this demo does not do

Stated rather than discovered:

- **No payments.** A reservation carries a price; nothing collects it.
  `revenue_mnt` is what was sold, not what was banked.
- **Slot races are only partly closed.** The unique index catches two
  bookings at the same start instant, which is the case that happens because
  slots are anchored. A partial overlap from a manual reschedule can still
  slip through; closing it needs a transaction, which needs a replica set.
- **Rate limits are per process.** Behind more than one replica the
  effective limit multiplies by the replica count. Weaker, not broken —
  moving the buckets to Redis is the fix when it matters.
- **No audit trail.** A manager reassigning a job or suspending an account
  leaves no record of who did it. For real use this is the first thing to
  add; `RecordAdminAction`-style middleware on the manager group is the
  shape.
- **Registration confirms whether an email is already in use.** The honest
  error beats a silent no-op that leaves someone unable to log in, and the
  route is rate limited, but it is an enumeration oracle for a patient
  attacker.
- **No automated integration tests.** `make flows` exercises a real database
  but is a script someone runs, not something CI would catch a regression
  with. A testcontainers suite behind its own `make test-integration` is the
  next step.
- **One constraint had to be expressed indirectly.** "At most one open time
  entry per employee" cannot be a partial unique index on `employee_id`
  filtered to `{clock_out_at: {$exists: false}}` — MongoDB rejects that with
  *"Expression not supported in partial index: $not"*. It is a sparse unique
  index on a key that is present exactly while the entry is open, which is
  the same rule by a different route. Same trick as the booking slot key.
