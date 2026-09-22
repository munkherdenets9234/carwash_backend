package service

import (
	"testing"
	"time"
)

var utc = time.UTC

// at builds a time on a fixed reference day, so the tests read as clock
// times rather than as timestamps.
func at(hour, min int) time.Time {
	return time.Date(2026, 9, 21, hour, min, 0, 0, utc)
}

func times(ts ...time.Time) []time.Time { return ts }

func sameTimes(t *testing.T, got, want []time.Time) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d slots %v, want %d %v", len(got), fmtAll(got), len(want), fmtAll(want))
	}
	for i := range got {
		if !got[i].Equal(want[i]) {
			t.Fatalf("slot %d = %s, want %s (got %v)", i, got[i].Format("15:04"), want[i].Format("15:04"), fmtAll(got))
		}
	}
}

func fmtAll(ts []time.Time) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Format("15:04")
	}
	return out
}

func TestIntervalOverlaps(t *testing.T) {
	a := Interval{at(10, 0), at(11, 0)}

	cases := []struct {
		name string
		b    Interval
		want bool
	}{
		{"back to back after is free", Interval{at(11, 0), at(12, 0)}, false},
		{"back to back before is free", Interval{at(9, 0), at(10, 0)}, false},
		{"partial overlap at the start", Interval{at(9, 30), at(10, 30)}, true},
		{"partial overlap at the end", Interval{at(10, 30), at(11, 30)}, true},
		{"fully contained", Interval{at(10, 15), at(10, 45)}, true},
		{"fully containing", Interval{at(9, 0), at(12, 0)}, true},
		{"disjoint", Interval{at(13, 0), at(14, 0)}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := a.Overlaps(c.b); got != c.want {
				t.Fatalf("Overlaps = %v, want %v", got, c.want)
			}
			// Overlap is symmetric; a rule that is not would make the
			// booking check depend on argument order.
			if got := c.b.Overlaps(a); got != c.want {
				t.Fatalf("reversed Overlaps = %v, want %v", got, c.want)
			}
		})
	}
}

func TestFreeSlots(t *testing.T) {
	shift := Interval{at(9, 0), at(12, 0)}
	const step = 30 * time.Minute
	const dur = 60 * time.Minute

	t.Run("empty shift offers every anchored start that fits", func(t *testing.T) {
		got := FreeSlots(shift, nil, dur, step, time.Time{})
		sameTimes(t, got, times(at(9, 0), at(9, 30), at(10, 0), at(10, 30), at(11, 0)))
	})

	t.Run("a job never runs past the end of the shift", func(t *testing.T) {
		got := FreeSlots(shift, nil, 90*time.Minute, step, time.Time{})
		sameTimes(t, got, times(at(9, 0), at(9, 30), at(10, 0), at(10, 30)))
	})

	t.Run("a booking removes every slot it touches", func(t *testing.T) {
		busy := []Interval{{at(10, 0), at(11, 0)}}
		got := FreeSlots(shift, busy, dur, step, time.Time{})
		// 09:30 is gone too: a 60-minute job starting then would run into
		// the 10:00 booking. Dropping only the exact start time is the
		// classic way to produce a double-booking.
		sameTimes(t, got, times(at(9, 0), at(11, 0)))
	})

	t.Run("notBefore hides slots already past", func(t *testing.T) {
		got := FreeSlots(shift, nil, dur, step, at(10, 15))
		sameTimes(t, got, times(at(10, 30), at(11, 0)))
	})

	t.Run("a fully booked shift offers nothing", func(t *testing.T) {
		busy := []Interval{{at(9, 0), at(12, 0)}}
		if got := FreeSlots(shift, busy, dur, step, time.Time{}); len(got) != 0 {
			t.Fatalf("want no slots, got %v", fmtAll(got))
		}
	})

	t.Run("a job longer than the shift offers nothing", func(t *testing.T) {
		if got := FreeSlots(shift, nil, 4*time.Hour, step, time.Time{}); len(got) != 0 {
			t.Fatalf("want no slots, got %v", fmtAll(got))
		}
	})

	t.Run("degenerate inputs return nothing rather than looping", func(t *testing.T) {
		if got := FreeSlots(shift, nil, 0, step, time.Time{}); got != nil {
			t.Fatalf("zero duration: want nil, got %v", fmtAll(got))
		}
		if got := FreeSlots(shift, nil, dur, 0, time.Time{}); got != nil {
			t.Fatalf("zero step: want nil, got %v", fmtAll(got))
		}
		if got := FreeSlots(Interval{at(12, 0), at(9, 0)}, nil, dur, step, time.Time{}); got != nil {
			t.Fatalf("inverted shift: want nil, got %v", fmtAll(got))
		}
	})
}

func TestFitsInShift(t *testing.T) {
	shifts := []Interval{
		{at(9, 0), at(12, 0)},
		{at(13, 0), at(17, 0)},
	}

	cases := []struct {
		name string
		busy []Interval
		job  Interval
		want bool
	}{
		{"inside a shift, nothing booked", nil, Interval{at(10, 0), at(11, 0)}, true},
		{"exactly filling a shift", nil, Interval{at(13, 0), at(17, 0)}, true},
		{"in the lunch gap between two shifts", nil, Interval{at(12, 0), at(13, 0)}, false},
		{"straddling the end of a shift", nil, Interval{at(11, 30), at(12, 30)}, false},
		{"before any shift", nil, Interval{at(8, 0), at(9, 0)}, false},
		{"after every shift", nil, Interval{at(17, 0), at(18, 0)}, false},
		{"clashing with a booking", []Interval{{at(10, 0), at(10, 30)}}, Interval{at(9, 30), at(10, 15)}, false},
		{"back to back with a booking", []Interval{{at(10, 0), at(11, 0)}}, Interval{at(11, 0), at(12, 0)}, true},
		{"zero length job", nil, Interval{at(10, 0), at(10, 0)}, false},
		{"inverted job", nil, Interval{at(11, 0), at(10, 0)}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FitsInShift(shifts, c.busy, c.job); got != c.want {
				t.Fatalf("FitsInShift = %v, want %v", got, c.want)
			}
		})
	}

	t.Run("a job spanning two adjoining shifts does not fit", func(t *testing.T) {
		// Two shifts that touch are still two shifts. Allowing a job to
		// span them would let a wash be booked across a handover where
		// nobody agreed to stay.
		adjoining := []Interval{{at(9, 0), at(12, 0)}, {at(12, 0), at(15, 0)}}
		if FitsInShift(adjoining, nil, Interval{at(11, 30), at(12, 30)}) {
			t.Fatal("want false for a job spanning two shifts")
		}
	})
}

func TestDayRange(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Ulaanbaatar")
	if err != nil {
		t.Fatalf("tzdata not embedded: %v", err)
	}

	// 2026-09-21 00:30 UTC is already 08:30 on the 21st in Ulaanbaatar, so
	// the business day containing it starts at 2026-09-20T16:00Z.
	start, end := DayRange(time.Date(2026, 9, 21, 0, 30, 0, 0, time.UTC), loc)

	if got := start.In(loc).Format("2006-01-02 15:04"); got != "2026-09-21 00:00" {
		t.Fatalf("start = %s, want 2026-09-21 00:00 local", got)
	}
	if got := end.In(loc).Format("2006-01-02 15:04"); got != "2026-09-22 00:00" {
		t.Fatalf("end = %s, want 2026-09-22 00:00 local", got)
	}
	if d := end.Sub(start); d != 24*time.Hour {
		t.Fatalf("day length = %v, want 24h", d)
	}
	// The boundary must be an instant, not a wall-clock string: a wash at
	// 23:59 local belongs to this day and one at 00:01 does not.
	if !start.Before(end) {
		t.Fatal("start must precede end")
	}
}
