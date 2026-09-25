package service

import (
	"context"
	"sort"
	"time"

	"github.com/eandstravel/carwash/internal/models"
	"github.com/eandstravel/carwash/internal/repository"
	"github.com/eandstravel/carwash/pkg/apierr"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// EmployeeDaily is one employee's line in the daily report.
type EmployeeDaily struct {
	EmployeeID primitive.ObjectID
	Name       string
	Washes     int
	RevenueMNT models.MNT
	BonusMNT   models.MNT
	// WorkedMinutes comes from the timesheet, not from the washes. The two
	// are independent on purpose: hours on site and jobs completed are
	// different questions, and a report that derived one from the other
	// would hide the case a manager most wants to see — a full shift with
	// no washes on it.
	WorkedMinutes int
}

// DailyReport is what a manager reads at the end of the day.
type DailyReport struct {
	Day        time.Time
	Washes     int
	RevenueMNT models.MNT
	BonusMNT   models.MNT
	// NetMNT is revenue less bonus. Carried rather than left to the client
	// to subtract, so two clients cannot disagree about it.
	NetMNT    models.MNT
	Employees []EmployeeDaily
}

type ReportService struct {
	bookings *repository.ReservationRepo
	entries  *repository.TimeEntryRepo
	users    *repository.UserRepo
	loc      *time.Location
}

func NewReportService(
	bookings *repository.ReservationRepo,
	entries *repository.TimeEntryRepo,
	users *repository.UserRepo,
	loc *time.Location,
) *ReportService {
	return &ReportService{bookings: bookings, entries: entries, users: users, loc: loc}
}

// Daily builds the report for the calendar day containing day, in the
// business timezone.
func (s *ReportService) Daily(ctx context.Context, tenantID primitive.ObjectID, day time.Time) (*DailyReport, error) {
	from, to := DayRange(day, s.loc)

	completed, err := s.bookings.CompletedBetween(ctx, tenantID, from, to)
	if err != nil {
		return nil, apierr.Internal(err)
	}
	entries, err := s.entries.List(ctx, tenantID, repository.TimeEntryQuery{From: from, To: to})
	if err != nil {
		return nil, apierr.Internal(err)
	}

	report := AggregateDaily(completed, entries)
	report.Day = from

	ids := make([]primitive.ObjectID, 0, len(report.Employees))
	for _, e := range report.Employees {
		ids = append(ids, e.EmployeeID)
	}
	names, err := s.users.FindManyByIDs(ctx, tenantID, ids)
	if err != nil {
		return nil, apierr.Internal(err)
	}
	for i := range report.Employees {
		if u, ok := names[report.Employees[i].EmployeeID]; ok {
			report.Employees[i].Name = u.Name
		} else {
			// A deleted employee still owns the work they did. Naming the
			// row explicitly beats a blank cell that reads like a bug.
			report.Employees[i].Name = "(removed employee)"
		}
	}
	return report, nil
}

// AggregateDaily turns one day's completed washes and time entries into the
// report.
//
// Pure, and exported, because this is the arithmetic that decides what people
// are paid. It takes the rows rather than the database so the rules — which
// bookings count, how a bonus is attributed, what happens to an employee who
// worked but sold nothing — can be stated as tests. See report_test.go.
func AggregateDaily(completed []*models.Reservation, entries []*models.TimeEntry) *DailyReport {
	byEmployee := map[primitive.ObjectID]*EmployeeDaily{}

	get := func(id primitive.ObjectID) *EmployeeDaily {
		row, ok := byEmployee[id]
		if !ok {
			row = &EmployeeDaily{EmployeeID: id}
			byEmployee[id] = row
		}
		return row
	}

	report := &DailyReport{}
	for _, r := range completed {
		// Defensive: CompletedBetween filters on status already, but this
		// function is exported and the rule "only completed work counts"
		// belongs with the arithmetic, not only with the query.
		if r.Status != models.ReservationCompleted {
			continue
		}
		row := get(r.EmployeeID)
		row.Washes++
		row.RevenueMNT += r.PriceMNT
		row.BonusMNT += r.BonusMNT

		report.Washes++
		report.RevenueMNT += r.PriceMNT
		report.BonusMNT += r.BonusMNT
	}

	for _, e := range entries {
		// An employee who was on site but completed nothing still appears,
		// with zero washes. Dropping them would make the report a list of
		// today's earners rather than today's staff.
		row := get(e.EmployeeID)
		if !e.Running() {
			row.WorkedMinutes += e.WorkedMinutes
		}
	}

	report.NetMNT = report.RevenueMNT - report.BonusMNT
	report.Employees = make([]EmployeeDaily, 0, len(byEmployee))
	for _, row := range byEmployee {
		report.Employees = append(report.Employees, *row)
	}

	// Highest revenue first, then by id so the order is stable across calls
	// — a report whose rows shuffle between refreshes looks untrustworthy
	// even when the numbers are right. Map iteration order alone would do
	// exactly that.
	sort.Slice(report.Employees, func(i, j int) bool {
		a, b := report.Employees[i], report.Employees[j]
		if a.RevenueMNT != b.RevenueMNT {
			return a.RevenueMNT > b.RevenueMNT
		}
		return a.EmployeeID.Hex() < b.EmployeeID.Hex()
	})
	return report
}
