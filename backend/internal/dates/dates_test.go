package dates

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestOfUsesConfiguredZone(t *testing.T) {
	// 2026-10-11 22:30 UTC is already Monday 2026-10-12 in Moscow (UTC+3).
	instant := time.Date(2026, 10, 11, 22, 30, 0, 0, time.UTC)
	if got := Of(instant, time.UTC).String(); got != "2026-10-11" {
		t.Fatalf("UTC date = %s", got)
	}
	if got := Of(instant, mustLoc(t, "Europe/Moscow")).String(); got != "2026-10-12" {
		t.Fatalf("Moscow date = %s", got)
	}
}

func TestPresetsAreHalfOpenAndWeeksStartMonday(t *testing.T) {
	today := New(2026, time.October, 15) // Thursday
	cases := []struct {
		p        Preset
		from, to string
	}{
		{Today, "2026-10-15", "2026-10-16"},
		{Yesterday, "2026-10-14", "2026-10-15"},
		{ThisWeek, "2026-10-12", "2026-10-19"},
		{LastWeek, "2026-10-05", "2026-10-12"},
		{ThisMonth, "2026-10-01", "2026-11-01"},
		{LastMonth, "2026-09-01", "2026-10-01"},
		{ThisYear, "2026-01-01", "2027-01-01"},
		{Last7Days, "2026-10-09", "2026-10-16"},
	}
	for _, c := range cases {
		r, err := c.p.Resolve(today)
		if err != nil {
			t.Fatal(err)
		}
		if r.From.String() != c.from || r.To.String() != c.to {
			t.Errorf("%s = [%s, %s), want [%s, %s)", c.p, r.From, r.To, c.from, c.to)
		}
	}
}

func TestWeekBoundariesOnSundayAndMonday(t *testing.T) {
	sunday := New(2026, time.October, 18)
	r, _ := ThisWeek.Resolve(sunday)
	if r.From.String() != "2026-10-12" || r.To.String() != "2026-10-19" {
		t.Fatalf("Sunday week = [%s, %s)", r.From, r.To)
	}
	monday := New(2026, time.October, 19)
	r, _ = ThisWeek.Resolve(monday)
	if r.From.String() != "2026-10-19" {
		t.Fatalf("Monday week starts %s", r.From)
	}
	if r.Contains(sunday) {
		t.Fatal("previous Sunday must not be in the new week")
	}
	if !r.Contains(monday) || r.Contains(r.To) {
		t.Fatal("range must include From and exclude To")
	}
}

func TestLastMonthFromJanuaryAndMarch31(t *testing.T) {
	r, _ := LastMonth.Resolve(New(2026, time.January, 10))
	if r.From.String() != "2025-12-01" || r.To.String() != "2026-01-01" {
		t.Fatalf("got [%s, %s)", r.From, r.To)
	}
	r, _ = LastMonth.Resolve(New(2026, time.March, 31))
	if r.From.String() != "2026-02-01" || r.To.String() != "2026-03-01" {
		t.Fatalf("got [%s, %s)", r.From, r.To)
	}
}

func TestParseRejectsInvalid(t *testing.T) {
	for _, s := range []string{"", "2026-13-01", "2026-02-30", "15.10.2026", "2026-10-15T00:00:00Z"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) should fail", s)
		}
	}
}
