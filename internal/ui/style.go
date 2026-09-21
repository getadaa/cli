package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Palette colours text for one stream. Colours are ANSI indexes so they follow
// the person's terminal theme instead of fighting it.
type Palette struct{ r *lipgloss.Renderer }

func (i *IO) S() Palette { return Palette{i.out} }
func (i *IO) E() Palette { return Palette{i.err} }

func (p Palette) style(fg string) lipgloss.Style {
	return p.r.NewStyle().Foreground(lipgloss.Color(fg))
}

func (p Palette) Bold(s string) string   { return p.r.NewStyle().Bold(true).Render(s) }
func (p Palette) Dim(s string) string    { return p.r.NewStyle().Faint(true).Render(s) }
func (p Palette) Green(s string) string  { return p.style("2").Render(s) }
func (p Palette) Red(s string) string    { return p.style("1").Render(s) }
func (p Palette) Yellow(s string) string { return p.style("3").Render(s) }
func (p Palette) Blue(s string) string   { return p.style("4").Render(s) }
func (p Palette) Cyan(s string) string   { return p.style("6").Render(s) }

// Status colours a status, severity or state word by what it means for the
// reader: green is fine, yellow wants attention eventually, red wants it now.
func (p Palette) Status(s string) string {
	if s == "" {
		return ""
	}
	label := strings.ReplaceAll(s, "_", " ")
	switch tone(s) {
	case toneGood:
		return p.Green(label)
	case toneWarn:
		return p.Yellow(label)
	case toneBad:
		return p.Red(label)
	case toneInfo:
		return p.Blue(label)
	case toneMuted:
		return p.Dim(label)
	}
	return label
}

type toneKind int

const (
	toneNone toneKind = iota
	toneGood
	toneWarn
	toneBad
	toneInfo
	toneMuted
)

func tone(s string) toneKind {
	switch s {
	case "ok", "active", "in_use", "done", "completed", "succeeded", "success", "healthy",
		"verified", "published", "resolved", "fulfilled", "running", "connected", "enabled",
		"passed", "delivered", "in_stock", "current", "up_to_date", "approved", "answered":
		return toneGood
	case "warning", "pending", "planned", "queued", "scheduled", "in_progress", "awaiting_approval",
		"waiting", "waiting_on_customer", "onboarding", "offboarding", "degraded", "provisioning",
		"in_repair", "attention", "triaged", "expiring", "stale", "open", "new", "migrating", "suspended", "draft", "unverified":
		return toneWarn
	case "critical", "failed", "error", "blocked", "lost", "stolen", "missing", "mismatched",
		"down", "expired", "denied", "rejected", "revoked", "broken":
		return toneBad
	case "info":
		return toneInfo
	case "unknown", "retired", "closed", "archived", "ignored", "cancelled", "canceled",
		"departed", "disabled", "skipped", "none":
		return toneMuted
	}
	return toneNone
}

// Stream messages. These go to stderr: they narrate, they are not the result.

func (i *IO) Successf(format string, a ...any) {
	fmt.Fprintln(i.Err, i.E().Green("✓")+" "+fmt.Sprintf(format, a...))
}

func (i *IO) Warnf(format string, a ...any) {
	fmt.Fprintln(i.Err, i.E().Yellow("!")+" "+fmt.Sprintf(format, a...))
}

func (i *IO) Failf(format string, a ...any) {
	fmt.Fprintln(i.Err, i.E().Red("✗")+" "+fmt.Sprintf(format, a...))
}

func (i *IO) Infof(format string, a ...any) {
	fmt.Fprintln(i.Err, fmt.Sprintf(format, a...))
}

// Hint suggests the next command. Suggestions are how a person learns the tool
// without reading the manual.
func (i *IO) Hint(format string, a ...any) {
	fmt.Fprintln(i.Err, i.E().Dim("→ "+fmt.Sprintf(format, a...)))
}
