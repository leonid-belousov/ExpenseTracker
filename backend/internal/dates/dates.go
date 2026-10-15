// Package dates provides a calendar-date type and period arithmetic shared by
// the REST API and the Telegram bot so both interpret ranges identically.
//
// Conventions:
//   - A Date has no time-of-day and no zone.
//   - "Today" is always computed in the deployment's configured time zone.
//   - Weeks run Monday through Sunday.
//   - Ranges are half-open: From is inclusive, To is exclusive.
package dates

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const layout = "2006-01-02"

// Date is a calendar date. The zero value is invalid.
type Date struct {
	t time.Time // always midnight UTC
}

var ErrInvalidDate = errors.New("date must be in YYYY-MM-DD format")

// New builds a date from components, normalising overflow (e.g. Feb 30).
func New(year int, month time.Month, day int) Date {
	return Date{t: time.Date(year, month, day, 0, 0, 0, 0, time.UTC)}
}

// Parse parses a strict YYYY-MM-DD string.
func Parse(s string) (Date, error) {
	t, err := time.Parse(layout, s)
	if err != nil {
		return Date{}, ErrInvalidDate
	}
	return Date{t: t}, nil
}

// Of returns the calendar date of instant t as observed in loc.
func Of(t time.Time, loc *time.Location) Date {
	y, m, d := t.In(loc).Date()
	return New(y, m, d)
}

func (d Date) IsZero() bool                { return d.t.IsZero() }
func (d Date) String() string              { return d.t.Format(layout) }
func (d Date) Time() time.Time             { return d.t }
func (d Date) AddDays(n int) Date          { return Date{t: d.t.AddDate(0, 0, n)} }
func (d Date) AddMonths(n int) Date        { return Date{t: d.t.AddDate(0, n, 0)} }
func (d Date) Before(o Date) bool          { return d.t.Before(o.t) }
func (d Date) After(o Date) bool           { return d.t.After(o.t) }
func (d Date) Equal(o Date) bool           { return d.t.Equal(o.t) }
func (d Date) Year() int                   { return d.t.Year() }
func (d Date) Month() time.Month           { return d.t.Month() }
func (d Date) Day() int                    { return d.t.Day() }
func (d Date) Weekday() time.Weekday       { return d.t.Weekday() }
func (d Date) Format(layout string) string { return d.t.Format(layout) }

// StartOfWeek returns the Monday on or before d.
func (d Date) StartOfWeek() Date {
	offset := (int(d.t.Weekday()) + 6) % 7 // Monday=0 ... Sunday=6
	return d.AddDays(-offset)
}

// StartOfMonth returns the first day of d's month.
func (d Date) StartOfMonth() Date { return New(d.Year(), d.Month(), 1) }

func (d Date) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return ErrInvalidDate
	}
	v, err := Parse(s)
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// Range is a half-open date range [From, To).
type Range struct {
	From Date
	To   Date
}

// Valid reports whether the range is non-empty.
func (r Range) Valid() bool { return !r.From.IsZero() && !r.To.IsZero() && r.From.Before(r.To) }

// Contains reports whether d lies in [From, To).
func (r Range) Contains(d Date) bool { return !d.Before(r.From) && d.Before(r.To) }

// LastDay is the inclusive end date, useful for human display.
func (r Range) LastDay() Date { return r.To.AddDays(-1) }

// Preset names a period relative to today.
type Preset string

const (
	Today      Preset = "today"
	Yesterday  Preset = "yesterday"
	ThisWeek   Preset = "this_week"
	LastWeek   Preset = "last_week"
	ThisMonth  Preset = "this_month"
	LastMonth  Preset = "last_month"
	ThisYear   Preset = "this_year"
	Last7Days  Preset = "last_7_days"
	Last30Days Preset = "last_30_days"
)

// Presets lists every supported preset, for prompt construction and validation.
var Presets = []Preset{Today, Yesterday, ThisWeek, LastWeek, ThisMonth, LastMonth, ThisYear, Last7Days, Last30Days}

// Resolve converts a preset to a half-open range relative to today.
func (p Preset) Resolve(today Date) (Range, error) {
	switch p {
	case Today:
		return Range{today, today.AddDays(1)}, nil
	case Yesterday:
		return Range{today.AddDays(-1), today}, nil
	case ThisWeek:
		s := today.StartOfWeek()
		return Range{s, s.AddDays(7)}, nil
	case LastWeek:
		s := today.StartOfWeek().AddDays(-7)
		return Range{s, s.AddDays(7)}, nil
	case ThisMonth:
		s := today.StartOfMonth()
		return Range{s, s.AddMonths(1)}, nil
	case LastMonth:
		s := today.StartOfMonth().AddMonths(-1)
		return Range{s, s.AddMonths(1)}, nil
	case ThisYear:
		s := New(today.Year(), time.January, 1)
		return Range{s, New(today.Year()+1, time.January, 1)}, nil
	case Last7Days:
		return Range{today.AddDays(-6), today.AddDays(1)}, nil
	case Last30Days:
		return Range{today.AddDays(-29), today.AddDays(1)}, nil
	}
	return Range{}, fmt.Errorf("unknown period %q", string(p))
}

// Clock abstracts "now" so tests can pin the current date.
type Clock interface{ Now() time.Time }

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

// FixedClock always returns the same instant.
type FixedClock struct{ T time.Time }

func (c FixedClock) Now() time.Time { return c.T }
