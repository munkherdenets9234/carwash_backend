// Command seed fills an empty database with a believable day at a car wash,
// so the demo has something to show before anyone has typed anything.
//
// It is a separate binary rather than a flag on the API, and it refuses to
// run against a database that already has users unless -reset is passed.
// Seeding is destructive by nature; making it a thing you can do by accident
// while starting the server is how someone loses real data.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/eandstravel/carwash/internal/config"
	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/pkg/password"
	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const demoPasswordNote = `
Seeded accounts (all on the demo password shown):

  manager@carwash.mn     manager123    manager
  bat@carwash.mn         employee123   employee
  saran@carwash.mn       employee123   employee
  temuulen@carwash.mn    employee123   employee
  customer@example.mn    customer123   customer
  oyuna@example.mn       customer123   customer
`

func main() {
	reset := flag.Bool("reset", false, "drop the existing demo collections first")
	flag.Parse()

	_ = godotenv.Load()
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		exit(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.MongoURI))
	if err != nil {
		exit(fmt.Errorf("mongo connect: %w", err))
	}
	defer func() { _ = client.Disconnect(context.Background()) }()

	if err := client.Ping(ctx, nil); err != nil {
		exit(fmt.Errorf("mongo ping: %w", err))
	}
	db := client.Database(cfg.MongoDB)

	if err := run(ctx, db, cfg, *reset); err != nil {
		exit(err)
	}

	fmt.Print(demoPasswordNote)
	fmt.Printf("Seeded %q in %s. Start the API and open http://localhost:%s/demo\n",
		cfg.MongoDB, cfg.Timezone, cfg.AppPort)
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, "seed failed: "+err.Error())
	os.Exit(1)
}

var collections = []string{"users", "cars", "locations", "wash_services", "shifts", "reservations", "time_entries"}

func run(ctx context.Context, db *mongo.Database, cfg *config.Config, reset bool) error {
	users := repository.NewUserRepo(db)

	existing, err := db.Collection("users").CountDocuments(ctx, map[string]any{})
	if err != nil {
		return err
	}
	if existing > 0 && !reset {
		return fmt.Errorf("database %q already has %d users; pass -reset to wipe and reseed", cfg.MongoDB, existing)
	}
	if reset {
		for _, name := range collections {
			if err := db.Collection(name).Drop(ctx); err != nil {
				return fmt.Errorf("drop %s: %w", name, err)
			}
		}
	}

	// Indexes first: the seed writes rows the unique constraints apply to,
	// and creating them afterwards would fail on data the seed itself let
	// through.
	if err := repository.EnsureIndexes(ctx, db); err != nil {
		return fmt.Errorf("indexes: %w", err)
	}

	locations := repository.NewLocationRepo(db)
	services := repository.NewWashServiceRepo(db)
	cars := repository.NewCarRepo(db)
	shifts := repository.NewShiftRepo(db)
	bookings := repository.NewReservationRepo(db)
	entries := repository.NewTimeEntryRepo(db)

	// ── Sites ────────────────────────────────────────────────────────────
	// Real coordinates in Ulaanbaatar, so the geofence demo works with a
	// phone's actual GPS if you happen to be there, and with the console's
	// typed coordinates if you are not.
	central := &models.Location{
		Name: "Central — Sukhbaatar Square", Address: "Sukhbaatar District, Ulaanbaatar",
		Point: models.GeoPoint{Lat: 47.918730, Lng: 106.917700}, GeofenceRadiusM: 120, Active: true,
	}
	zaisan := &models.Location{
		Name: "Zaisan", Address: "Khan-Uul District, Ulaanbaatar",
		Point: models.GeoPoint{Lat: 47.887000, Lng: 106.918000}, GeofenceRadiusM: 150, Active: true,
	}
	for _, l := range []*models.Location{central, zaisan} {
		if err := locations.Create(ctx, l); err != nil {
			return fmt.Errorf("location %s: %w", l.Name, err)
		}
	}

	// ── Price list ───────────────────────────────────────────────────────
	express := &models.WashService{Name: "Express exterior", Description: "Outside only, machine wash and dry",
		DurationMin: 30, PriceMNT: 18000, BonusMNT: 2000, Active: true}
	standard := &models.WashService{Name: "Standard wash", Description: "Exterior plus interior vacuum",
		DurationMin: 60, PriceMNT: 30000, BonusMNT: 4000, Active: true}
	full := &models.WashService{Name: "Full valet", Description: "Exterior, interior, wax and trim",
		DurationMin: 120, PriceMNT: 75000, BonusMNT: 12000, Active: true}
	engine := &models.WashService{Name: "Engine bay clean", Description: "Seasonal — withdrawn for winter",
		DurationMin: 45, PriceMNT: 25000, BonusMNT: 3500, Active: false}
	for _, s := range []*models.WashService{express, standard, full, engine} {
		if err := services.Create(ctx, s); err != nil {
			return fmt.Errorf("service %s: %w", s.Name, err)
		}
	}

	// ── People ───────────────────────────────────────────────────────────
	mk := func(role models.Role, name, email, phone, plain string, home *models.Location) (*models.User, error) {
		hash, err := password.Hash(plain)
		if err != nil {
			return nil, err
		}
		u := &models.User{
			Role: role, Name: name, Email: email, Phone: phone,
			PasswordHash: hash, Status: models.UserActive,
		}
		if home != nil {
			u.Employee = &models.EmployeeProfile{
				HomeLocationID: home.ID,
				HiredAt:        time.Now().UTC().AddDate(0, -8, 0),
			}
		}
		return u, users.Create(ctx, u)
	}

	if _, err := mk(models.RoleManager, "Enkhbayar B.", "manager@carwash.mn", "+976 9911 0000", "manager123", nil); err != nil {
		return fmt.Errorf("manager: %w", err)
	}
	bat, err := mk(models.RoleEmployee, "Bat-Erdene T.", "bat@carwash.mn", "+976 9911 1111", "employee123", central)
	if err != nil {
		return fmt.Errorf("employee bat: %w", err)
	}
	saran, err := mk(models.RoleEmployee, "Saranchimeg D.", "saran@carwash.mn", "+976 9911 2222", "employee123", central)
	if err != nil {
		return fmt.Errorf("employee saran: %w", err)
	}
	temuulen, err := mk(models.RoleEmployee, "Temuulen G.", "temuulen@carwash.mn", "+976 9911 3333", "employee123", zaisan)
	if err != nil {
		return fmt.Errorf("employee temuulen: %w", err)
	}
	cust1, err := mk(models.RoleCustomer, "Nomin-Erdene S.", "customer@example.mn", "+976 8800 1111", "customer123", nil)
	if err != nil {
		return fmt.Errorf("customer 1: %w", err)
	}
	cust2, err := mk(models.RoleCustomer, "Oyuna Ch.", "oyuna@example.mn", "+976 8800 2222", "customer123", nil)
	if err != nil {
		return fmt.Errorf("customer 2: %w", err)
	}

	// ── Cars ─────────────────────────────────────────────────────────────
	prius := &models.Car{OwnerID: cust1.ID, Plate: "1234UBA", Make: "Toyota", Model: "Prius 30", Color: "white"}
	lexus := &models.Car{OwnerID: cust1.ID, Plate: "5678UBB", Make: "Lexus", Model: "RX 350", Color: "black",
		Notes: "Alloy wheels — no harsh brushes"}
	crv := &models.Car{OwnerID: cust2.ID, Plate: "9012UBE", Make: "Honda", Model: "CR-V", Color: "silver"}
	for _, c := range []*models.Car{prius, lexus, crv} {
		if err := cars.Create(ctx, c); err != nil {
			return fmt.Errorf("car %s: %w", c.Plate, err)
		}
	}

	// ── Roster: today plus the next six days ─────────────────────────────
	// Built in the business timezone so the shift really does run 09:00 to
	// 18:00 where the branch is, not wherever the seeding machine happens
	// to be.
	loc := cfg.Location
	now := time.Now().In(loc)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	roster := []struct {
		emp            *models.User
		site           *models.Location
		startHr, endHr int
	}{
		{bat, central, 9, 18},
		{saran, central, 10, 19},
		{temuulen, zaisan, 9, 17},
	}

	for day := 0; day < 7; day++ {
		d := dayStart.AddDate(0, 0, day)
		for _, r := range roster {
			sh := &models.Shift{
				EmployeeID: r.emp.ID,
				LocationID: r.site.ID,
				StartAt:    d.Add(time.Duration(r.startHr) * time.Hour).UTC(),
				EndAt:      d.Add(time.Duration(r.endHr) * time.Hour).UTC(),
			}
			if err := shifts.Create(ctx, sh); err != nil {
				return fmt.Errorf("shift: %w", err)
			}
		}
	}

	// ── Today's finished work, so the daily report is not empty ──────────
	done := []struct {
		emp  *models.User
		car  *models.Car
		svc  *models.WashService
		site *models.Location
		hour int
	}{
		{bat, prius, standard, central, 9},
		{bat, lexus, full, central, 11},
		{saran, crv, express, central, 10},
		{saran, prius, standard, central, 13},
		{temuulen, crv, full, zaisan, 9},
	}

	for _, d := range done {
		start := dayStart.Add(time.Duration(d.hour) * time.Hour).UTC()
		end := start.Add(time.Duration(d.svc.DurationMin) * time.Minute)
		completed := end
		customerID := cust1.ID
		if d.car.OwnerID == cust2.ID {
			customerID = cust2.ID
		}

		res := &models.Reservation{
			CustomerID: customerID,
			EmployeeID: d.emp.ID,
			CarID:      d.car.ID,
			ServiceID:  d.svc.ID,
			LocationID: d.site.ID,
			StartAt:    start,
			EndAt:      end,
			Status:     models.ReservationCompleted,
			// Copied from the service exactly as a real booking would,
			// so the report reads the stored amounts and not a join.
			PriceMNT:    d.svc.PriceMNT,
			BonusMNT:    d.svc.BonusMNT,
			CompletedAt: &completed,
		}
		if err := bookings.Create(ctx, res); err != nil {
			return fmt.Errorf("completed reservation: %w", err)
		}
	}

	// One booking still ahead of us, so the employee's job list and the
	// customer's "my bookings" both have a live row to act on. Placed two
	// hours out and clamped inside the roster.
	upcoming := now.Add(2 * time.Hour).Truncate(time.Hour).UTC()
	if upcoming.In(loc).Hour() >= 17 {
		// Too late in the day to fit inside a shift; move it to tomorrow.
		upcoming = dayStart.AddDate(0, 0, 1).Add(11 * time.Hour).UTC()
	}
	live := &models.Reservation{
		CustomerID: cust2.ID,
		EmployeeID: bat.ID,
		CarID:      crv.ID,
		ServiceID:  standard.ID,
		LocationID: central.ID,
		StartAt:    upcoming,
		EndAt:      upcoming.Add(time.Duration(standard.DurationMin) * time.Minute),
		Status:     models.ReservationBooked,
		PriceMNT:   standard.PriceMNT,
		BonusMNT:   standard.BonusMNT,
		SlotKey:    bat.ID.Hex() + "|" + upcoming.Format(time.RFC3339),
		Notes:      "Customer will wait on site",
	}
	if err := bookings.Create(ctx, live); err != nil {
		return fmt.Errorf("upcoming reservation: %w", err)
	}

	// ── Attendance: two closed shifts and one still running ──────────────
	closed := func(emp *models.User, site *models.Location, inHr, outHr int) error {
		in := dayStart.Add(time.Duration(inHr) * time.Hour).UTC()
		out := dayStart.Add(time.Duration(outHr) * time.Hour).UTC()
		e := &models.TimeEntry{
			EmployeeID: emp.ID, LocationID: site.ID,
			ClockInAt: in, ClockInPoint: site.Point, ClockInDistanceM: 14,
		}
		if err := entries.Create(ctx, e); err != nil {
			return err
		}
		return entries.Close(ctx, e.ID, out, site.Point, 22, int(out.Sub(in).Minutes()))
	}
	if err := closed(saran, central, 10, 19); err != nil {
		return fmt.Errorf("time entry saran: %w", err)
	}
	if err := closed(temuulen, zaisan, 9, 17); err != nil {
		return fmt.Errorf("time entry temuulen: %w", err)
	}

	// Bat is still on site. The report shows him with washes but no hours
	// yet, which is exactly the case the open-entry handling is for.
	openEntry := &models.TimeEntry{
		EmployeeID: bat.ID, LocationID: central.ID,
		ClockInAt: dayStart.Add(9 * time.Hour).UTC(), ClockInPoint: central.Point, ClockInDistanceM: 31,
	}
	if err := entries.Create(ctx, openEntry); err != nil {
		return fmt.Errorf("open time entry: %w", err)
	}

	fmt.Printf("locations=2 services=4 staff=4 customers=2 cars=3 shifts=%d reservations=6 time_entries=3\n", 7*len(roster))
	return nil
}
