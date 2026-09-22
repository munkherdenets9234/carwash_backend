package manager

import (
	"time"

	"github.com/eandstravel/carwash/internal/api/apictx"
	"github.com/eandstravel/carwash/internal/service"
	"github.com/eandstravel/carwash/internal/view"
	"github.com/eandstravel/carwash/pkg/response"
	"github.com/gin-gonic/gin"
)

type reportController struct {
	reports *service.ReportService
	loc     *time.Location
}

// Daily is the end-of-day figure: revenue taken, bonus owed, and the split
// per employee with the hours each of them was on site.
//
// The day is rendered back as a plain YYYY-MM-DD string rather than as a
// timestamp. A report for "2026-09-21" that comes back as an instant invites
// the client to re-derive the date in the browser's timezone and display a
// different day than the one it asked for.
func (h *reportController) Daily(c *gin.Context) error {
	day, err := apictx.Day(c, "day", h.loc)
	if err != nil {
		return err
	}

	report, err := h.reports.Daily(c.Request.Context(), day)
	if err != nil {
		return err
	}

	rows := make([]view.EmployeeDaily, 0, len(report.Employees))
	for _, e := range report.Employees {
		rows = append(rows, view.EmployeeDaily{
			EmployeeID:    e.EmployeeID.Hex(),
			Name:          e.Name,
			Washes:        e.Washes,
			RevenueMNT:    e.RevenueMNT,
			BonusMNT:      e.BonusMNT,
			WorkedMinutes: e.WorkedMinutes,
			WorkedHours:   view.HoursOf(e.WorkedMinutes),
		})
	}

	response.OK(c, view.DailyReport{
		Day:        report.Day.In(h.loc).Format("2006-01-02"),
		Washes:     report.Washes,
		RevenueMNT: report.RevenueMNT,
		BonusMNT:   report.BonusMNT,
		NetMNT:     report.NetMNT,
		Employees:  rows,
	})
	return nil
}
