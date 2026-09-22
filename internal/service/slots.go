// Package service holds the business rules. Handlers parse and render;
// repositories read and write; everything that decides something lives here.
//
// The scheduling rules in this file are deliberately pure functions over
// plain values — no context, no repository, no clock of their own. They are
// the part most likely to be wrong and the part hardest to exercise through
// HTTP, so they are the part that must be testable without a database. See
// slots_test.go.
package service

import "time"

// Interval is a half-open time range [Start, End). Half-open is what makes a
// 10:00–11:00 booking and an 11:00–12:00 booking not overlap; with closed
// ranges every back-to-back pair of washes would collide on the boundary
// minute and half the day would look busy.
type Interval struct {
	Start time.Time
	End   time.Time
}

// Overlaps reports whether two half-open intervals share any time.
func (i Interval) Overlaps(o Interval) bool {
	return i.Start.Before(o.End) && o.Start.Before(i.End)
}

// maxSlotsPerShift caps the loop below. A misconfigured step of one minute
// against a 24-hour shift is 1440 entries, which is a useless response and a
// lot of allocation; anything beyond this is a configuration error, not a
// roster.
const maxSlotsPerShift = 256

// FreeSlots returns the times at which a job of length duration can start
// within a shift, given the intervals already taken.
//
// Slots are anchored to shift.Start and advance by step, so a shift starting
// at 09:00 with a 15-minute step offers 09:00, 09:15, 09:30 — round times a
// customer recognises, rather than whatever offsets the previous bookings
// happen to have left behind.
//
// notBefore drops slots already in the past. It is a parameter rather than a
// call to time.Now inside, so a test can state "it is 10:20" instead of
// arranging for it to be true.
func FreeSlots(shift Interval, busy []Interval, duration, step time.Duration, notBefore time.Time) []time.Time {
	if duration <= 0 || step <= 0 || !shift.Start.Before(shift.End) {
		return nil
	}

	var out []time.Time
	for t := shift.Start; !t.Add(duration).After(shift.End); t = t.Add(step) {
		if len(out) >= maxSlotsPerShift {
			break
		}
		if t.Before(notBefore) {
			continue
		}
		candidate := Interval{Start: t, End: t.Add(duration)}
		if overlapsAny(candidate, busy) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// FitsInShift reports whether a job fits entirely inside one of the shifts
// and clashes with nothing already booked.
//
// This is the check that runs at booking time, and it is deliberately NOT
// "was this slot in the list we offered". The list was computed for some
// earlier request and may be seconds or minutes stale; re-deriving the answer
// from the roster and the bookings is what stops two customers who loaded the
// same page from both taking 14:00.
func FitsInShift(shifts []Interval, busy []Interval, job Interval) bool {
	if !job.Start.Before(job.End) {
		return false
	}
	within := false
	for _, s := range shifts {
		if !job.Start.Before(s.Start) && !job.End.After(s.End) {
			within = true
			break
		}
	}
	if !within {
		return false
	}
	return !overlapsAny(job, busy)
}

func overlapsAny(candidate Interval, busy []Interval) bool {
	for _, b := range busy {
		if candidate.Overlaps(b) {
			return true
		}
	}
	return false
}

// DayRange returns the half-open [start, end) of the calendar day containing
// day, in loc. Reports and roster reads are built from this so "today" is one
// definition in one place.
func DayRange(day time.Time, loc *time.Location) (time.Time, time.Time) {
	l := day.In(loc)
	start := time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
	// AddDate rather than Add(24h): on a daylight-saving boundary a day is
	// 23 or 25 hours long, and the fixed offset would put an hour of it on
	// the wrong report. Mongolia does not currently observe DST, which is
	// exactly the kind of fact that changes by decree and then quietly
	// breaks arithmetic that assumed it.
	return start, start.AddDate(0, 0, 1)
}
