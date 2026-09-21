package ui

import (
	"testing"
	"time"
)

func TestMoney(t *testing.T) {
	for _, c := range []struct {
		minor int64
		cur   string
		want  string
	}{
		{825000, "NOK", "8 250,00 kr"},
		{5, "NOK", "0,05 kr"},
		{-129900, "NOK", "−1 299,00 kr"},
		{123456789, "EUR", "1 234 567,89 EUR"},
	} {
		if got := Money(c.minor, c.cur); got != c.want {
			t.Errorf("Money(%d, %s) = %q, want %q", c.minor, c.cur, got, c.want)
		}
	}
}

func TestRelative(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		t    time.Time
		want string
	}{
		{now.Add(-30 * time.Second), "just now"},
		{now.Add(-5 * time.Minute), "5m ago"},
		{now.Add(3 * time.Hour), "in 3h"},
		{now.Add(-50 * 24 * time.Hour), "2 Aug 2026"},
	} {
		if got := Relative(c.t, now); got != c.want {
			t.Errorf("Relative(%s) = %q, want %q", c.t, got, c.want)
		}
	}
}
