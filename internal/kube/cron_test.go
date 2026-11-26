package kube

import (
	"testing"
	"time"
)

func TestCronNext(t *testing.T) {
	utc := time.UTC
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, utc)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct{ spec, from, want string }{
		{"*/15 * * * *", "2026-11-26 12:07", "2026-11-26 12:15"},
		{"*/15 * * * *", "2026-11-26 12:45", "2026-11-26 13:00"},
		{"0 2 * * *", "2026-11-26 12:07", "2026-11-27 02:00"},
		{"30 6 * * 1-5", "2026-11-27 07:00", "2026-11-30 06:30"}, // Friday evening, next is Monday
		{"0 0 1 * *", "2026-11-26 12:00", "2026-12-01 00:00"},
		{"0 0 29 2 *", "2026-11-26 12:00", "2028-02-29 00:00"},
		{"@hourly", "2026-11-26 12:07", "2026-11-26 13:00"},
		{"@daily", "2026-11-26 12:07", "2026-11-27 00:00"},
		{"0 9 * * mon", "2026-11-26 12:00", "2026-11-30 09:00"},
		{"0 9 * * 7", "2026-11-26 12:00", "2026-11-29 09:00"}, // 7 is Sunday
		{"5,35 8-9 * * *", "2026-11-26 08:36", "2026-11-26 09:05"},
		{"0 12 1,15 * 5", "2026-11-26 00:00", "2026-11-27 12:00"}, // day-of-month or day-of-week when both are set
		{"CRON_TZ=UTC 0 3 * * *", "2026-11-26 12:00", "2026-11-27 03:00"},
	}
	for _, c := range cases {
		s, err := parseCron(c.spec)
		if err != nil {
			t.Errorf("%s: %v", c.spec, err)
			continue
		}
		if got := s.next(at(c.from)); !got.Equal(at(c.want)) {
			t.Errorf("%s after %s = %s, want %s", c.spec, c.from, got.Format("2006-01-02 15:04"), c.want)
		}
	}
}

func TestCronNeverAndErrors(t *testing.T) {
	s, err := parseCron("0 0 31 2 *")
	if err != nil {
		t.Fatal(err)
	}
	if !s.next(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)).IsZero() {
		t.Error("31 February must never run")
	}
	for _, bad := range []string{"", "* * * *", "61 * * * *", "*/0 * * * *", "a b c d e", "* * * 13 *"} {
		if _, err := parseCron(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestCronTimeZone(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no tz database")
	}
	s, _ := parseCron("0 8 * * *")
	got := s.next(time.Date(2026, 11, 26, 12, 0, 0, 0, loc))
	if got.Hour() != 8 || got.Day() != 27 || got.Location() != loc {
		t.Errorf("next = %s", got)
	}
}
