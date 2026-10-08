// Package schedule reads the five-field cron expressions a Schedule is written
// in, and says when one fires next.
//
// Written here rather than imported: the grammar is small, and what matters is
// that it is the one people already know — minute, hour, day of month, month,
// day of week, with lists, ranges and steps — evaluated in the time zone the
// person wrote it in, not in the one the server happens to run in.
package schedule

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	// The zone a person wrote a Schedule in must resolve wherever Core runs,
	// including an image that ships no zone database of its own.
	_ "time/tzdata"
)

// Spec is a parsed cron expression.
type Spec struct {
	minute, hour, dom, month, dow uint64
	// The day fields combine the way cron always has: when both are
	// restricted, either one matching is enough.
	domAny, dowAny bool
}

type field struct {
	name     string
	min, max int
	names    map[string]int
}

var (
	minuteField = field{name: "minute", min: 0, max: 59}
	hourField   = field{name: "hour", min: 0, max: 23}
	domField    = field{name: "day of month", min: 1, max: 31}
	monthField  = field{name: "month", min: 1, max: 12, names: map[string]int{
		"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
		"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
	}}
	// 7 is Sunday too, as in every cron.
	dowField = field{name: "day of week", min: 0, max: 7, names: map[string]int{
		"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
	}}
)

// Parse reads "minute hour day-of-month month day-of-week".
func Parse(expression string) (Spec, error) {
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return Spec{}, fmt.Errorf("a schedule has five fields (minute hour day month weekday), this one has %d", len(fields))
	}
	var spec Spec
	var err error
	if spec.minute, _, err = parseField(fields[0], minuteField); err != nil {
		return Spec{}, err
	}
	if spec.hour, _, err = parseField(fields[1], hourField); err != nil {
		return Spec{}, err
	}
	if spec.dom, spec.domAny, err = parseField(fields[2], domField); err != nil {
		return Spec{}, err
	}
	if spec.month, _, err = parseField(fields[3], monthField); err != nil {
		return Spec{}, err
	}
	if spec.dow, spec.dowAny, err = parseField(fields[4], dowField); err != nil {
		return Spec{}, err
	}
	if spec.dow&(1<<7) != 0 {
		spec.dow |= 1 << 0
	}
	return spec, nil
}

// parseField reads one field into a bit set, and reports whether it was "*".
func parseField(text string, f field) (uint64, bool, error) {
	var bits uint64
	for _, part := range strings.Split(text, ",") {
		step := 1
		if base, raw, found := strings.Cut(part, "/"); found {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				return 0, false, fmt.Errorf("the %s step %q is not a positive number", f.name, raw)
			}
			step = n
			part = base
		}

		low, high := f.min, f.max
		switch {
		case part == "*":
		case strings.Contains(part, "-"):
			from, to, _ := strings.Cut(part, "-")
			var err error
			if low, err = value(from, f); err != nil {
				return 0, false, err
			}
			if high, err = value(to, f); err != nil {
				return 0, false, err
			}
			if low > high {
				return 0, false, fmt.Errorf("the %s range %q runs backwards", f.name, part)
			}
		default:
			v, err := value(part, f)
			if err != nil {
				return 0, false, err
			}
			low = v
			// "5/15" means from 5 to the end, every 15.
			if step == 1 {
				high = v
			}
		}
		for v := low; v <= high; v += step {
			bits |= 1 << uint(v)
		}
	}
	return bits, text == "*", nil
}

func value(text string, f field) (int, error) {
	if v, ok := f.names[strings.ToLower(text)]; ok {
		return v, nil
	}
	v, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("the %s %q is not a number", f.name, text)
	}
	if v < f.min || v > f.max {
		return 0, fmt.Errorf("the %s %d is outside %d-%d", f.name, v, f.min, f.max)
	}
	return v, nil
}

// ErrNever is returned for an expression that names no real date, such as
// the 30th of February.
var ErrNever = errors.New("this schedule never fires")

// Next is the first time strictly after the given instant at which the
// expression fires, in the given location.
//
// It walks forward a field at a time — the month, then the day, then the
// hour, then the minute — so a yearly schedule costs a handful of steps, not
// half a million minutes. A local time a daylight-saving change skips does not
// fire that day, and one it repeats fires once, which is what a person who
// wrote "every day at 2:30" in that zone would expect of a clock.
func (s Spec) Next(after time.Time, location *time.Location) (time.Time, error) {
	t := after.In(location).Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)

	for t.Before(limit) {
		if s.month&(1<<uint(t.Month())) == 0 {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, location)
			continue
		}
		if !s.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, location)
			continue
		}
		if s.hour&(1<<uint(t.Hour())) == 0 {
			next := time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, location)
			if !next.After(t) {
				next = t.Add(time.Hour).Truncate(time.Hour)
			}
			t = next
			continue
		}
		if s.minute&(1<<uint(t.Minute())) == 0 {
			t = t.Add(time.Minute)
			continue
		}
		return t, nil
	}
	return time.Time{}, ErrNever
}

func (s Spec) dayMatches(t time.Time) bool {
	dom := s.dom&(1<<uint(t.Day())) != 0
	dow := s.dow&(1<<uint(t.Weekday())) != 0
	switch {
	case s.domAny && s.dowAny:
		return true
	case s.domAny:
		return dow
	case s.dowAny:
		return dom
	default:
		return dom || dow
	}
}
