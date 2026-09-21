package cli

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() {
	register(newCostCmd)
	register(newTimeCmd)
}

func newCostCmd(a *App) *cobra.Command {
	var status string
	cmd := &cobra.Command{
		Use:   "cost",
		Short: "What your IT costs, line by line",
		Long: `What your IT costs. Yearly lines count as a twelfth per month; one-time costs
are kept apart so setup work never inflates the monthly figure.`,
		Example: `  adaa cost
  adaa cost --status active_and_pending`,
		GroupID: groupCore,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := oneOf("status", status, "active", "active_and_pending", "all"); err != nil {
				return err
			}
			path, err := a.OrgPath(ctx, "/cost-summary")
			if err != nil {
				return err
			}
			q := url.Values{}
			setQuery(q, cmd, "status", "status", status)
			c, raw, err := a.Get(ctx, path, q)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, c, a.renderCostSummary)
		},
	}
	cmd.Flags().StringVar(&status, "status", "active", "Which subscriptions to count: active, active_and_pending or all")
	enumFlag(cmd, "status", "active", "active_and_pending", "all")
	return cmd
}

func (a *App) renderCostSummary(c obj.Obj) {
	s := a.IO.S()
	cur := c.Str("currency")
	amt := func(field string) string {
		n, _ := c.Int(field)
		return ui.Money(n, cur)
	}
	awaiting, _ := c.Int("lines_awaiting_price")
	d := a.IO.NewDetail("", "")
	monthly := s.Bold(amt("recurring_monthly_minor")) + " per month"
	if awaiting > 0 {
		monthly += s.Yellow(fmt.Sprintf("  estimate: %d line(s) not priced yet", awaiting))
	}
	d.Field("Recurring", monthly)
	d.Field("Per year", amt("recurring_yearly_minor"))
	if n, _ := c.Int("one_time_minor"); n > 0 {
		d.Field("One-time", amt("one_time_minor"))
	}
	if n, _ := c.Int("wasted_monthly_minor"); n > 0 {
		d.Field("Wasted", s.Yellow(amt("wasted_monthly_minor")+" per month")+s.Dim(" on seats nobody holds"))
	}
	d.Render()

	lines := c.List("lines")
	if len(lines) == 0 {
		return
	}
	slices.SortStableFunc(lines, func(x, y obj.Obj) int {
		xm, _ := x.Int("monthly_equivalent_minor")
		ym, _ := y.Int("monthly_equivalent_minor")
		return int(ym - xm)
	})
	fmt.Fprintln(a.IO.Out)
	t := a.IO.NewTable("Service", "Qty", "Unit price", "Billed", "Per month", "Subscription").Flex(0)
	for _, l := range lines {
		qty := l.Str("quantity")
		if o, ok := l.Int("observed_count"); ok {
			if q, _ := l.Int("quantity"); o != q {
				qty += s.Yellow(fmt.Sprintf(" (%d exist)", o))
			}
		}
		perMonth := money(l, "monthly_equivalent_minor")
		if !l.Bool("priced") {
			perMonth = s.Yellow("not priced")
		} else if l.Str("billing_period") == "one_time" {
			perMonth = s.Dim(money(l, "total_minor") + " once")
		}
		t.Row(l.Str("service_name"), qty, money(l, "unit_price_minor"), strings.ReplaceAll(l.Str("billing_period"), "_", " "),
			perMonth, a.dim(l.Str("subscription_id")))
	}
	t.Render()
}

func newTimeCmd(a *App) *cobra.Command {
	var from, to string
	cmd := &cobra.Command{
		Use:   "time",
		Short: "Support time used, against what your retainer covers",
		Example: `  adaa time
  adaa time --from 2026-07-01 --to 2026-09-30`,
		GroupID: groupCore,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			now := time.Now()
			if from == "" {
				from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local).Format(time.DateOnly)
			}
			if to == "" {
				to = now.Format(time.DateOnly)
			}
			for flag, v := range map[string]string{"from": from, "to": to} {
				if _, err := time.Parse(time.DateOnly, v); err != nil {
					return usagef("--%s wants a date like 2026-09-01", flag)
				}
			}
			path, err := a.OrgPath(ctx, "/time-summary")
			if err != nil {
				return err
			}
			t, raw, err := a.Get(ctx, path, url.Values{"from": {from}, "to": {to}})
			if err != nil {
				return err
			}
			return a.PrintObj(raw, t, a.renderTimeSummary)
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "First day, as YYYY-MM-DD (default: the 1st of this month)")
	cmd.Flags().StringVar(&to, "to", "", "Last day, as YYYY-MM-DD (default: today)")
	return cmd
}

func minutes(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprintf("%dh %02dm", n/60, n%60)
	if n < 60 {
		s = fmt.Sprintf("%dm", n)
	}
	if neg {
		s = "−" + s
	}
	return s
}

func (a *App) renderTimeSummary(t obj.Obj) {
	s := a.IO.S()
	n := func(f string) int64 { v, _ := t.Int(f); return v }
	d := a.IO.NewDetail("Support time", t.Str("from")+" – "+t.Str("to"))
	d.Field("Logged", minutes(n("minutes_logged")))
	d.Field("Billable", minutes(n("minutes_billable")))
	if t.Has("minutes_non_billable") {
		d.Field("Not billed", minutes(n("minutes_non_billable"))+s.Dim(" (including fixing our own mistakes)"))
	}
	if inc, ok := t.Int("retainer_minutes_included"); ok {
		d.Field("Retainer covers", minutes(inc))
		if rem, ok := t.Int("minutes_remaining"); ok {
			txt := minutes(rem) + " left"
			if rem < 0 {
				txt = s.Red(minutes(-rem) + " over what the retainer covers")
			} else if inc > 0 && rem*5 < inc {
				txt = s.Yellow(txt)
			}
			d.Field("Remaining", txt)
		}
	} else {
		d.Field("Retainer", s.Dim("none; all billable time is invoiced"))
	}
	if kinds := t.List("by_task_kind"); len(kinds) > 0 {
		d.Section("By kind of work")
		for _, k := range kinds {
			m, _ := k.Int("minutes")
			d.Field(strings.ReplaceAll(k.Str("kind"), "_", " "), minutes(m))
		}
	}
	d.Render()
}
