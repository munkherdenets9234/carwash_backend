"""End-to-end check of every flow, against a running API and a seeded database.

    make reseed && make run     # in one shell
    make flows                  # in another

It is a script rather than a Go test because it needs both a live server and
a real MongoDB, and `make test` is the target that must stay runnable with
neither. The two pauses are not padding: the rate limiter is on, and the
checks around them would otherwise measure the limiter instead of the
endpoint.
"""
import json
import urllib.request
import urllib.error
import time
from datetime import datetime, timezone, timedelta

BASE = "http://localhost:8090"
UB = timezone(timedelta(hours=8))
TODAY = datetime.now(UB).strftime("%Y-%m-%d")

fails = []


def call(method, path, token=None, body=None):
    req = urllib.request.Request(BASE + path, method=method)
    if token:
        req.add_header("Authorization", "Bearer " + token)
    data = None
    if body is not None:
        data = json.dumps(body).encode()
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, data, timeout=20) as r:
            raw = r.read().decode()
            return r.status, (json.loads(raw) if raw else None), dict(r.headers)
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        try:
            parsed = json.loads(raw)
        except Exception:
            parsed = raw
        return e.code, parsed, dict(e.headers)


def check(label, got, want, extra=""):
    ok = got == want
    print(f"  {'PASS' if ok else 'FAIL'}  {label}: {got} (want {want}) {extra}")
    if not ok:
        fails.append(label)
    return ok


def code_of(body):
    if isinstance(body, dict) and isinstance(body.get("error"), dict):
        return body["error"].get("code")
    return None


def data_of(body):
    return body.get("data") if isinstance(body, dict) else None


def login(email, pw):
    s, b, _ = call("POST", "/api/v1/auth/login", body={"email": email, "password": pw})
    assert s == 200, (s, b)
    return data_of(b)["token"], data_of(b)["user"]


def section(name):
    print("\n" + "=" * 70)
    print(name)
    print("=" * 70)


# ── 1. Public surface ────────────────────────────────────────────────────
section("1. Public catalogue (no token)")
s, b, _ = call("GET", "/api/v1/services")
check("GET /services", s, 200)
services = data_of(b)
print(f"        {len(services)} active services:", ", ".join(x["name"] for x in services))
standard = next(x for x in services if x["name"] == "Standard wash")
check("withdrawn service hidden from public list", any(x["name"] == "Engine bay clean" for x in services), False)
check("public price list omits bonus_mnt", "bonus_mnt" in standard, False)

s, b, _ = call("GET", "/api/v1/locations")
check("GET /locations", s, 200)
locations = data_of(b)
central = next(x for x in locations if x["name"].startswith("Central"))
print(f"        {central['name']} radius={central['geofence_radius_m']}m")

# ── 2. Role-based auth ───────────────────────────────────────────────────
section("2. Role-based authentication")
mgr_token, mgr = login("manager@carwash.mn", "manager123")
cust_token, cust = login("customer@example.mn", "customer123")
bat_token, bat = login("bat@carwash.mn", "employee123")
saran_token, saran = login("saran@carwash.mn", "employee123")
print(f"        manager={mgr['name']} customer={cust['name']} employees={bat['name']}, {saran['name']}")

s, b, _ = call("GET", "/api/v1/me", mgr_token)
check("GET /me as manager", s, 200, f"role={data_of(b)['role']}")

s, _, _ = call("GET", "/api/v1/manager/staff")
check("manager route with no token", s, 401)
s, b, _ = call("GET", "/api/v1/manager/staff", cust_token)
check("manager route with a customer token", s, 403, code_of(b))
s, b, _ = call("GET", "/api/v1/manager/staff", bat_token)
check("manager route with an employee token", s, 403, code_of(b))
s, b, _ = call("GET", "/api/v1/employee/jobs", cust_token)
check("employee route with a customer token", s, 403, code_of(b))
s, b, _ = call("GET", "/api/v1/customer/cars", bat_token)
check("customer route with an employee token", s, 403, code_of(b))

s, b, _ = call("GET", "/api/v1/manager/staff", mgr_token)
check("manager route with a manager token", s, 200, f"{len(data_of(b))} staff")

# ── 3. Customer: cars ────────────────────────────────────────────────────
section("3. Car registration (customer)")
s, b, _ = call("GET", "/api/v1/customer/cars", cust_token)
check("GET /customer/cars", s, 200)
cars = data_of(b)
print("        owns:", ", ".join(f"{c['plate']} ({c['make']})" for c in cars))
car = cars[0]

s, b, _ = call("POST", "/api/v1/customer/cars", cust_token, {"plate": "7777 UBZ", "make": "Nissan", "model": "X-Trail"})
check("register a new car", s, 201, data_of(b)["plate"] if s == 201 else b)
new_car = data_of(b)
check("plate normalised to uppercase, no spaces", new_car["plate"], "7777UBZ")

s, b, _ = call("POST", "/api/v1/customer/cars", cust_token, {"plate": "7777ubz"})
check("duplicate plate for same owner", s, 409, code_of(b))

# Another customer's car must not be reachable.
oyuna_token, _ = login("oyuna@example.mn", "customer123")
s, b, _ = call("GET", "/api/v1/customer/cars", oyuna_token)
oyuna_car = data_of(b)[0]
s, b, _ = call("GET", "/api/v1/customer/cars/" + oyuna_car["id"], cust_token)
check("reading another customer's car", s, 404, code_of(b))

# ── 4. Availability ──────────────────────────────────────────────────────
section("4. Time reservation — who is available")
s, b, _ = call("GET", "/api/v1/customer/employees", cust_token)
check("GET /customer/employees", s, 200)
employees = data_of(b)
print("        choices:", ", ".join(e["name"] for e in employees))
check("employee card carries no email", "email" in employees[0], False)
check("employee card carries no phone", "phone" in employees[0], False)

s, _, _ = call("GET", f"/api/v1/customer/availability?service_id={standard['id']}&day={TODAY}")
check("availability without a token", s, 401)

s, b, _ = call("GET", f"/api/v1/customer/availability?service_id={standard['id']}&day={TODAY}", cust_token)
check("GET /customer/availability", s, 200)
avail = data_of(b)
for a in avail:
    print(f"        {a['employee']['name']:<18} {len(a['slots']):>2} free slots"
          + (f"  first {a['slots'][0]['start_at']}" if a["slots"] else "  (none left today)"))

bat_avail = next((a for a in avail if a["employee"]["id"] == bat["id"]), None)
if not bat_avail or not bat_avail["slots"]:
    # Late in the day there may be nothing left; fall back to tomorrow.
    tomorrow = (datetime.now(UB) + timedelta(days=1)).strftime("%Y-%m-%d")
    s, b, _ = call("GET", f"/api/v1/customer/availability?service_id={standard['id']}&day={tomorrow}", cust_token)
    avail = data_of(b)
    bat_avail = next(a for a in avail if a["employee"]["id"] == bat["id"])
    print(f"        (nothing left today — using {tomorrow})")

slot = bat_avail["slots"][0]
print(f"        booking {bat['name']} at {slot['start_at']}")

# ── 5. Booking with a chosen employee ────────────────────────────────────
section("5. Customer books, choosing the employee")
book = {
    "employee_id": bat["id"],
    "car_id": car["id"],
    "service_id": standard["id"],
    "location_id": slot["location_id"],
    "start_at": slot["start_at"],
    "notes": "Booked by the flow script",
}
s, b, _ = call("POST", "/api/v1/customer/reservations", cust_token, book)
check("book a wash", s, 201, code_of(b) or "")
res = data_of(b)
print(f"        id={res['id']} {res['service']['name']} with {res['employee']['name']} at {res['location']['name']}")
check("customer's view hides bonus_mnt", "bonus_mnt" in res, False)
check("customer's view hides the customer block", "customer" in res, False)
check("price copied onto the booking", res["price_mnt"], standard["price_mnt"])

s, b, _ = call("POST", "/api/v1/customer/reservations", cust_token, book)
check("same slot booked twice", s, 409, code_of(b))

bad = dict(book, start_at="2020-01-01T09:00:00Z")
s, b, _ = call("POST", "/api/v1/customer/reservations", cust_token, bad)
check("booking in the past", s, 422, code_of(b))

bad = dict(book, car_id=oyuna_car["id"])
s, b, _ = call("POST", "/api/v1/customer/reservations", cust_token, bad)
check("booking someone else's car", s, 404, code_of(b))

# Off-shift: 04:00 on the slot's day.
off = dict(book, start_at=slot["start_at"][:11] + "20:00:00Z")
s, b, _ = call("POST", "/api/v1/customer/reservations", cust_token, off)
check("booking outside the roster", s, 409, code_of(b))

# ── 6. Geofenced time registration ───────────────────────────────────────
section("6. Time registration — geofenced (employee)")
s, b, _ = call("GET", "/api/v1/employee/attendance/current", saran_token)
check("Saran starts clocked out", data_of(b), None)

far = {"location_id": central["id"], "lat": 47.8870, "lng": 106.9180}  # Zaisan, ~3.5km away
s, b, _ = call("POST", "/api/v1/employee/attendance/clock-in", saran_token, far)
check("clock in from 3.5 km away", s, 422, code_of(b))
print("        message:", b["error"]["message"] if code_of(b) else b)

nofix = {"location_id": central["id"], "lat": 0, "lng": 0}
s, b, _ = call("POST", "/api/v1/employee/attendance/clock-in", saran_token, nofix)
check("clock in with no GPS fix (0,0)", s, 422, code_of(b))
print("        message:", b["error"]["message"] if code_of(b) else b)

near = {"location_id": central["id"], "lat": 47.91940, "lng": 106.91810}  # ~80 m from the site
s, b, _ = call("POST", "/api/v1/employee/attendance/clock-in", saran_token, near)
check("clock in from 80 m away (radius 120 m)", s, 201, code_of(b) or "")
entry = data_of(b)
print(f"        recorded distance {entry['clock_in_distance_m']} m, running={entry['running']}")

s, b, _ = call("POST", "/api/v1/employee/attendance/clock-in", saran_token, near)
check("clock in twice", s, 409, code_of(b))

s, b, _ = call("GET", "/api/v1/employee/attendance/current", saran_token)
check("current shows the open entry", data_of(b) is not None, True)

s, b, _ = call("POST", "/api/v1/employee/attendance/clock-out", saran_token, {"lat": 47.8870, "lng": 106.9180})
check("clock out from far away is allowed", s, 200, code_of(b) or "")
out = data_of(b)
print(f"        worked {out['worked_minutes']} min, clock-out distance {out['clock_out_distance_m']} m (recorded, not refused)")

s, b, _ = call("POST", "/api/v1/employee/attendance/clock-out", saran_token, {"lat": 47.9194, "lng": 106.9181})
check("clock out when not clocked in", s, 409, code_of(b))

print("        (pausing 14 s — the previous checks spent the attendance limiter's burst)")
time.sleep(14)
s, b, _ = call("POST", "/api/v1/employee/attendance/clock-in", saran_token, near)
check("clock in again after clocking out", s, 201, code_of(b) or "")

# ── 7. Timesheets ────────────────────────────────────────────────────────
section("7. Timesheets")
s, b, _ = call("GET", f"/api/v1/employee/timesheet?from={TODAY}&to={TODAY}", saran_token)
check("employee's own timesheet", s, 200)
ts = data_of(b)
print(f"        {ts['total_entries']} entries, {ts['worked_hours']:.1f} h, {ts['open_entries']} still open")

s, b, _ = call("GET", f"/api/v1/manager/timesheets?from={TODAY}&to={TODAY}", mgr_token)
check("manager sees everyone's timesheets", s, 200)
allts = data_of(b)
print(f"        {allts['total_entries']} entries across staff, {allts['worked_hours']:.1f} h, {allts['open_entries']} open")
check("manager's timesheet covers more entries than one employee's", allts["total_entries"] > ts["total_entries"], True)

s, b, _ = call("GET", f"/api/v1/manager/timesheets?employee_id={bat['id']}&from={TODAY}&to={TODAY}", mgr_token)
check("manager filters by employee", s, 200, f"{data_of(b)['total_entries']} entries for {bat['name']}")

# ── 8. Employee job card and completion ──────────────────────────────────
section("8. Employee works the job — bonus follows")
day_of_slot = slot["start_at"][:10]
s, b, _ = call("GET", f"/api/v1/employee/jobs?from={day_of_slot}&to={day_of_slot}", bat_token)
check("employee sees their own jobs", s, 200)
jobs = data_of(b)
mine = next(j for j in jobs if j["id"] == res["id"])
check("employee's job card shows bonus_mnt", "bonus_mnt" in mine, True, f"{mine.get('bonus_mnt')}")
check("employee's job card shows the customer's phone", bool(mine["customer"].get("phone")), True)

s, b, _ = call("GET", f"/api/v1/employee/jobs?from={day_of_slot}&to={day_of_slot}", saran_token)
check("another employee's list excludes this job", any(j["id"] == res["id"] for j in data_of(b)), False)

s, b, _ = call("PUT", f"/api/v1/employee/jobs/{res['id']}/status", saran_token, {"status": "completed"})
check("employee completing someone else's job", s, 404, code_of(b))

before = call("GET", f"/api/v1/manager/reports/daily?day={TODAY}", mgr_token)[1]
before = data_of(before)

s, b, _ = call("PUT", f"/api/v1/employee/jobs/{res['id']}/status", bat_token, {"status": "in_progress"})
check("start the job", s, 200, data_of(b)["status"] if s == 200 else code_of(b))
s, b, _ = call("PUT", f"/api/v1/employee/jobs/{res['id']}/status", bat_token, {"status": "completed"})
check("complete the job", s, 200, data_of(b)["status"] if s == 200 else code_of(b))
completed = data_of(b)

s, b, _ = call("PUT", f"/api/v1/employee/jobs/{res['id']}/status", bat_token, {"status": "in_progress"})
check("reopening a completed job", s, 409, code_of(b))

# ── 9. Daily report ──────────────────────────────────────────────────────
section("9. Manager's daily report")
s, b, _ = call("GET", f"/api/v1/manager/reports/daily?day={TODAY}", mgr_token)
check("GET /manager/reports/daily", s, 200)
rep = data_of(b)
print(f"        {rep['day']}: {rep['washes']} washes, revenue {rep['revenue_mnt']:,}, "
      f"bonus {rep['bonus_mnt']:,}, net {rep['net_mnt']:,}")
for e in rep["employees"]:
    print(f"          {e['name']:<18} {e['washes']} washes  revenue {e['revenue_mnt']:>7,}  "
          f"bonus {e['bonus_mnt']:>6,}  {e['worked_hours']:.1f} h")
check("net == revenue - bonus", rep["net_mnt"], rep["revenue_mnt"] - rep["bonus_mnt"])

if day_of_slot == TODAY:
    check("completing a wash added it to today's report", rep["washes"], before["washes"] + 1)
    check("revenue rose by the booking's price", rep["revenue_mnt"] - before["revenue_mnt"], completed["price_mnt"])
    check("bonus rose by the booking's bonus", rep["bonus_mnt"] - before["bonus_mnt"], completed["bonus_mnt"])
else:
    print("        (booking landed on tomorrow, so today's totals are unchanged — checked below)")

worked_but_idle = [e for e in rep["employees"] if e["washes"] == 0]
print(f"        employees with hours but no washes still listed: {len(worked_but_idle)}")

s, b, _ = call("GET", f"/api/v1/manager/reports/daily?day={TODAY}", cust_token)
check("customer reading the report", s, 403, code_of(b))

# ── 10. Reassignment moves the bonus ─────────────────────────────────────
section("10. Manager reassigns an open job")
s, b, _ = call("GET", f"/api/v1/customer/availability?service_id={standard['id']}&day={day_of_slot}", cust_token)
bat_left = next(a for a in data_of(b) if a["employee"]["id"] == bat["id"])
if bat_left["slots"]:
    slot2 = bat_left["slots"][0]
    s, b, _ = call("POST", "/api/v1/customer/reservations", cust_token,
                   dict(book, start_at=slot2["start_at"], location_id=slot2["location_id"]))
    check("book a second wash with Bat", s, 201, code_of(b) or "")
    res2 = data_of(b)

    s, b, _ = call("PUT", f"/api/v1/manager/reservations/{res2['id']}/assign", mgr_token,
                   {"employee_id": saran["id"]})
    check("reassign it to Saran", s, 200, code_of(b) or "")
    if s == 200:
        print(f"        now assigned to {data_of(b)['employee']['name']}, bonus {data_of(b)['bonus_mnt']:,}")
        check("employee changed", data_of(b)["employee"]["id"], saran["id"])

    s, b, _ = call("PUT", f"/api/v1/manager/reservations/{res2['id']}/assign", cust_token,
                   {"employee_id": bat["id"]})
    check("customer trying to reassign", s, 403, code_of(b))
else:
    print("        (Bat has no free slots left to book a second job — skipped)")
    res2 = None

s, b, _ = call("PUT", f"/api/v1/manager/reservations/{res['id']}/assign", mgr_token, {"employee_id": saran["id"]})
check("reassigning a COMPLETED job", s, 409, code_of(b))

# ── 11. Customer cancels ─────────────────────────────────────────────────
section("11. Customer cancels, and the slot is released")
if res2:
    s, b, _ = call("POST", f"/api/v1/customer/reservations/{res2['id']}/cancel", cust_token, {})
    check("cancel an upcoming booking", s, 200, data_of(b)["status"] if s == 200 else code_of(b))

    s, b, _ = call("POST", "/api/v1/customer/reservations", cust_token,
                   dict(book, employee_id=saran["id"], start_at=res2["start_at"], location_id=res2["location"]["id"]))
    check("the freed slot can be booked again", s, 201, code_of(b) or "")
    if s == 201:
        call("POST", f"/api/v1/customer/reservations/{data_of(b)['id']}/cancel", cust_token, {})

s, b, _ = call("POST", f"/api/v1/customer/reservations/{res['id']}/cancel", cust_token, {})
check("cancelling a completed wash", s, 409, code_of(b))

# ── 12. Manager catalogue and staff ──────────────────────────────────────
section("12. Manager catalogue and staff management")
s, b, _ = call("GET", "/api/v1/manager/services", mgr_token)
check("manager's price list includes withdrawn items", any(x["name"] == "Engine bay clean" for x in data_of(b)), True)
check("manager's price list includes bonus_mnt", "bonus_mnt" in data_of(b)[0], True)

s, b, _ = call("POST", "/api/v1/manager/services", mgr_token,
               {"name": "Winter underbody rinse", "duration_min": 20, "price_mnt": 12000, "bonus_mnt": 1500, "active": True})
check("create a service", s, 201, code_of(b) or "")

s, b, _ = call("POST", "/api/v1/manager/services", mgr_token,
               {"name": "Nonsense", "duration_min": 30, "price_mnt": 10000, "bonus_mnt": 50000})
check("bonus larger than the price", s, 422, code_of(b))

s, b, _ = call("POST", "/api/v1/manager/locations", mgr_token,
               {"name": "Too tight", "lat": 47.9, "lng": 106.9, "geofence_radius_m": 5})
check("geofence radius below the floor", s, 422, code_of(b))

s, b, _ = call("POST", "/api/v1/manager/staff", mgr_token,
               {"role": "customer", "name": "Sneaky", "email": "sneaky@x.mn", "password": "password1"})
check("manager creating a CUSTOMER account", s, 422, code_of(b))

s, b, _ = call("POST", "/api/v1/manager/staff", mgr_token,
               {"role": "employee", "name": "Ganbold P.", "email": "ganbold@carwash.mn",
                "phone": "+976 9911 4444", "password": "employee123", "home_location_id": central["id"]})
check("create an employee", s, 201, code_of(b) or "")
ganbold = data_of(b) if s == 201 else None

s, b, _ = call("PUT", f"/api/v1/manager/staff/{mgr['id']}/status", mgr_token, {"status": "suspended"})
check("manager suspending themselves", s, 422, code_of(b))

if ganbold:
    gtoken, _ = login("ganbold@carwash.mn", "employee123")
    s, _, _ = call("GET", "/api/v1/employee/jobs", gtoken)
    check("new employee can use the employee surface", s, 200)

    s, b, _ = call("PUT", f"/api/v1/manager/staff/{ganbold['id']}/status", mgr_token, {"status": "suspended"})
    check("suspend the new employee", s, 204)

    s, b, _ = call("GET", "/api/v1/employee/jobs", gtoken)
    check("their EXISTING token stops working immediately", s, 403, code_of(b))

    s, b, _ = call("POST", "/api/v1/auth/login", body={"email": "ganbold@carwash.mn", "password": "employee123"})
    check("and they cannot log back in", s, 403, code_of(b))

# ── 13. Login and rate limiting ──────────────────────────────────────────
section("13. Login hardening")
# /auth/login and /auth/register share one limiter group at 10/min with a
# burst of 5, and the logins above have spent it. Wait for a refill so the
# checks below measure the endpoints and not the limiter.
print("        (pausing 40 s for the auth limiter to refill)")
time.sleep(40)
s, b, _ = call("POST", "/api/v1/auth/login", body={"email": "nobody@nowhere.mn", "password": "whatever"})
unknown = (s, (b.get("error") or {}).get("message"))
s2, b2, _ = call("POST", "/api/v1/auth/login", body={"email": "manager@carwash.mn", "password": "wrongpassword"})
wrong = (s2, (b2.get("error") or {}).get("message"))
check("unknown email and wrong password are indistinguishable", unknown, wrong, str(unknown))

s, b, _ = call("POST", "/api/v1/auth/register", body={"email": "manager@carwash.mn", "name": "x", "password": "password1"})
check("registering an email already in use", s, 409, code_of(b))

s, b, _ = call("POST", "/api/v1/auth/register", body={"email": "not-an-email", "name": "x", "password": "password1"})
check("malformed email", s, 422, code_of(b))

s, b, _ = call("POST", "/api/v1/auth/register", body={"email": "shortpw@example.mn", "name": "x", "password": "abc"})
check("short password", s, 422, code_of(b))

hit429 = None
for i in range(25):
    s, b, hdrs = call("POST", "/api/v1/auth/login", body={"email": "nobody@nowhere.mn", "password": "x"})
    if s == 429:
        hit429 = (i + 1, code_of(b), hdrs.get("Retry-After"))
        break
check("login is rate limited", hit429 is not None, True,
      f"429 after {hit429[0]} attempts, code={hit429[1]}, Retry-After={hit429[2]}s" if hit429 else "never limited")

# ── Summary ──────────────────────────────────────────────────────────────
print("\n" + "=" * 70)
if fails:
    print(f"{len(fails)} CHECK(S) FAILED:")
    for f in fails:
        print("  -", f)
else:
    print("ALL CHECKS PASSED")
print("=" * 70)
