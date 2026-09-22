package service

import (
	"testing"
	"time"

	"github.com/eandstravel/carwash/internal/models"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

var (
	bat   = primitive.NewObjectID()
	saran = primitive.NewObjectID()
)

func wash(employee primitive.ObjectID, price, bonus models.MNT, status models.ReservationStatus) *models.Reservation {
	done := at(12, 0)
	return &models.Reservation{
		ID:          primitive.NewObjectID(),
		EmployeeID:  employee,
		Status:      status,
		PriceMNT:    price,
		BonusMNT:    bonus,
		CompletedAt: &done,
	}
}

func entry(employee primitive.ObjectID, minutes int, open bool) *models.TimeEntry {
	e := &models.TimeEntry{
		ID:            primitive.NewObjectID(),
		EmployeeID:    employee,
		ClockInAt:     at(9, 0),
		WorkedMinutes: minutes,
	}
	if !open {
		out := at(9, 0).Add(time.Duration(minutes) * time.Minute)
		e.ClockOutAt = &out
	}
	return e
}

func rowFor(t *testing.T, r *DailyReport, id primitive.ObjectID) EmployeeDaily {
	t.Helper()
	for _, row := range r.Employees {
		if row.EmployeeID == id {
			return row
		}
	}
	t.Fatalf("no row for employee %s", id.Hex())
	return EmployeeDaily{}
}

func TestAggregateDaily(t *testing.T) {
	t.Run("totals revenue and bonus across employees", func(t *testing.T) {
		r := AggregateDaily([]*models.Reservation{
			wash(bat, 25000, 3000, models.ReservationCompleted),
			wash(bat, 40000, 5000, models.ReservationCompleted),
			wash(saran, 25000, 3000, models.ReservationCompleted),
		}, nil)

		if r.Washes != 3 {
			t.Fatalf("washes = %d, want 3", r.Washes)
		}
		if r.RevenueMNT != 90000 {
			t.Fatalf("revenue = %d, want 90000", r.RevenueMNT)
		}
		if r.BonusMNT != 11000 {
			t.Fatalf("bonus = %d, want 11000", r.BonusMNT)
		}
		if r.NetMNT != 79000 {
			t.Fatalf("net = %d, want 79000", r.NetMNT)
		}

		batRow := rowFor(t, r, bat)
		if batRow.Washes != 2 || batRow.RevenueMNT != 65000 || batRow.BonusMNT != 8000 {
			t.Fatalf("bat row = %+v", batRow)
		}
	})

	t.Run("only completed work counts", func(t *testing.T) {
		r := AggregateDaily([]*models.Reservation{
			wash(bat, 25000, 3000, models.ReservationCompleted),
			wash(bat, 99000, 9000, models.ReservationCancelled),
			wash(bat, 99000, 9000, models.ReservationNoShow),
			wash(bat, 99000, 9000, models.ReservationBooked),
		}, nil)

		if r.Washes != 1 || r.RevenueMNT != 25000 || r.BonusMNT != 3000 {
			t.Fatalf("want one 25000/3000 wash, got %d washes rev=%d bonus=%d",
				r.Washes, r.RevenueMNT, r.BonusMNT)
		}
	})

	t.Run("an employee who worked but sold nothing still appears", func(t *testing.T) {
		r := AggregateDaily(
			[]*models.Reservation{wash(bat, 25000, 3000, models.ReservationCompleted)},
			[]*models.TimeEntry{entry(saran, 480, false)},
		)

		if len(r.Employees) != 2 {
			t.Fatalf("want 2 employee rows, got %d", len(r.Employees))
		}
		row := rowFor(t, r, saran)
		if row.Washes != 0 || row.RevenueMNT != 0 {
			t.Fatalf("saran should have no sales: %+v", row)
		}
		if row.WorkedMinutes != 480 {
			t.Fatalf("saran worked = %d, want 480", row.WorkedMinutes)
		}
	})

	t.Run("an open entry contributes no minutes", func(t *testing.T) {
		r := AggregateDaily(nil, []*models.TimeEntry{
			entry(bat, 240, false),
			entry(bat, 999, true), // still clocked in
		})
		if got := rowFor(t, r, bat).WorkedMinutes; got != 240 {
			t.Fatalf("worked = %d, want 240 (the open entry must not count)", got)
		}
	})

	t.Run("hours and washes are independent", func(t *testing.T) {
		// Bat sold more; Saran was on site longer. Neither figure may be
		// derived from the other.
		r := AggregateDaily(
			[]*models.Reservation{wash(bat, 50000, 5000, models.ReservationCompleted)},
			[]*models.TimeEntry{entry(bat, 120, false), entry(saran, 600, false)},
		)
		if got := rowFor(t, r, bat).WorkedMinutes; got != 120 {
			t.Fatalf("bat worked = %d, want 120", got)
		}
		if got := rowFor(t, r, saran).Washes; got != 0 {
			t.Fatalf("saran washes = %d, want 0", got)
		}
	})

	t.Run("rows are ordered by revenue and are stable", func(t *testing.T) {
		build := func() *DailyReport {
			return AggregateDaily([]*models.Reservation{
				wash(saran, 10000, 1000, models.ReservationCompleted),
				wash(bat, 90000, 9000, models.ReservationCompleted),
			}, nil)
		}
		first := build()
		if first.Employees[0].EmployeeID != bat {
			t.Fatal("highest revenue must come first")
		}
		// Map iteration order differs per run; the sort must absorb that.
		for i := 0; i < 20; i++ {
			again := build()
			for j := range again.Employees {
				if again.Employees[j].EmployeeID != first.Employees[j].EmployeeID {
					t.Fatal("row order is not stable across calls")
				}
			}
		}
	})

	t.Run("an empty day is a zero report, not a nil one", func(t *testing.T) {
		r := AggregateDaily(nil, nil)
		if r == nil {
			t.Fatal("want a report")
		}
		if r.Washes != 0 || r.RevenueMNT != 0 || r.NetMNT != 0 {
			t.Fatalf("want zeroes, got %+v", r)
		}
		if r.Employees == nil {
			t.Fatal("Employees must be an empty slice, not nil — it is rendered as a JSON array")
		}
	})
}

func TestSummarizeTimesheet(t *testing.T) {
	sum := SummarizeTimesheet([]*models.TimeEntry{
		entry(bat, 240, false),
		entry(bat, 180, false),
		entry(bat, 60, true),
	})

	if sum.Entries != 3 {
		t.Fatalf("entries = %d, want 3", sum.Entries)
	}
	if sum.WorkedMinutes != 420 {
		t.Fatalf("worked = %d, want 420", sum.WorkedMinutes)
	}
	if sum.OpenEntries != 1 {
		t.Fatalf("open = %d, want 1", sum.OpenEntries)
	}
}
