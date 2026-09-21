package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Money renders minor units the Norwegian way: 825000 NOK is "8 250,00 kr".
func Money(minor int64, currency string) string {
	neg := minor < 0
	if neg {
		minor = -minor
	}
	whole := strconv.FormatInt(minor/100, 10)
	var b strings.Builder
	for n, r := range whole {
		if n > 0 && (len(whole)-n)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	s := fmt.Sprintf("%s,%02d", b.String(), minor%100)
	if neg {
		s = "−" + s
	}
	if currency == "" || currency == "NOK" {
		return s + " kr"
	}
	return s + " " + currency
}

// SignedMoney always shows the sign, for deltas.
func SignedMoney(minor int64, currency string) string {
	if minor > 0 {
		return "+" + Money(minor, currency)
	}
	return Money(minor, currency)
}

// Bytes renders a size in binary units.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// When renders an RFC 3339 instant for a person: relative when recent, a date
// otherwise. When stdout is piped the instant is passed through untouched, so
// scripts get something parseable.
func (i *IO) When(s string) string {
	if s == "" {
		return ""
	}
	if !i.OutTTY {
		return s
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return Relative(t, time.Now())
}

func Relative(t, now time.Time) string {
	d := now.Sub(t)
	future := d < 0
	d = time.Duration(math.Abs(float64(d)))
	var s string
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		s = fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		s = fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		s = fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return t.Local().Format("2 Jan 2006")
	}
	if future {
		return "in " + s
	}
	return s + " ago"
}
