package cli

import (
	"fmt"
	"strings"

	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() { register(newStatusCmd) }

func newStatusCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "See how your IT is doing, in one screen",
		Long: `The front page: whether things are fine, what needs attention, what is waiting
for you, and what it all costs.`,
		GroupID: groupCore,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			path, err := a.OrgPath(ctx, "/status")
			if err != nil {
				return err
			}
			st, raw, err := a.Get(ctx, path, nil)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, st, a.renderStatus)
		},
	}
}

func (a *App) renderStatus(st obj.Obj) {
	io := a.IO
	s := io.S()
	w := io.Out

	overall := st.Str("overall")
	dot := map[string]string{"ok": s.Green("●"), "attention": s.Yellow("●"), "critical": s.Red("●"), "unknown": s.Dim("●")}[overall]
	name := a.Cfg.OrganizationName
	if name == "" {
		name = st.Str("organization_id")
	}
	fmt.Fprintf(w, "%s %s  %s\n", dot, s.Bold(name), s.Status(overall))
	fmt.Fprintf(w, "  %s\n\n", st.Str("headline"))

	counts := func(parts ...string) string { return join(s.Dim(" · "), parts...) }
	n := func(path string) int64 { v, _ := st.Int(path); return v }
	when := func(v int64, text string, color func(string) string) string {
		if v == 0 {
			return ""
		}
		return color(fmt.Sprintf("%d %s", v, text))
	}
	plain := func(x string) string { return x }

	d := io.NewDetail("", "")
	d.Field("People", counts(fmt.Sprintf("%d active", n("people.active")),
		when(n("people.planned"), "starting soon", plain),
		when(n("people.departed_with_access"), "left but still have access", s.Red)))
	d.Field("Findings", orNone(counts(when(n("findings.critical"), "critical", s.Red),
		when(n("findings.warning"), "warning", s.Yellow), when(n("findings.info"), "info", s.Blue)), s.Green("nothing open")))
	d.Field("Tasks", orNone(counts(when(n("tasks.awaiting_approval"), "waiting for your approval", s.Yellow),
		when(n("tasks.in_progress"), "in progress", plain), when(n("tasks.blocked"), "blocked", s.Red)), s.Dim("nothing running")))
	d.Field("Signals", counts(when(n("signals.ok"), "ok", s.Green), when(n("signals.warning"), "warning", s.Yellow),
		when(n("signals.critical"), "critical", s.Red), when(n("signals.unknown"), "unknown", s.Dim)))
	cov := fmt.Sprintf("%d of %d sources healthy", n("coverage.sources_healthy"), n("coverage.sources_total"))
	if stale := n("coverage.sources_stale"); stale > 0 {
		cov += s.Yellow(fmt.Sprintf(", %d stale", stale))
	}
	d.Field("Coverage", cov)
	for _, b := range st.Strings("coverage.blind_spots") {
		d.Field("", s.Yellow("! ")+b)
	}
	if v, ok := st.Int("cost.recurring_monthly_minor"); ok {
		c := ui.Money(v, st.Str("cost.currency")) + " per month"
		if p := n("cost.lines_awaiting_price"); p > 0 {
			c += s.Dim(fmt.Sprintf(" (estimate; %d not priced yet)", p))
		}
		d.Field("Cost", c)
	}
	d.Render()

	if top := st.List("top_findings"); len(top) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, s.Bold("Needs attention"))
		for _, f := range top {
			fmt.Fprintf(w, "  %s  %s  %s\n", severityDot(s, f.Str("severity")), f.Str("summary"), s.Dim(f.Str("id")))
		}
	}

	switch {
	case n("tasks.awaiting_approval") > 0:
		io.Hint("adaa tasks list --awaiting-approval")
	case n("findings.open_total") > 0:
		io.Hint("adaa findings list")
	}
}

func orNone(s, none string) string {
	if strings.TrimSpace(s) == "" {
		return none
	}
	return s
}

func severityDot(s ui.Palette, severity string) string {
	switch severity {
	case "critical":
		return s.Red("●")
	case "warning":
		return s.Yellow("●")
	case "info":
		return s.Blue("●")
	}
	return s.Dim("●")
}
