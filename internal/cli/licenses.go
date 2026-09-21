package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/getadaa/cli/internal/api"
	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() { register(newLicensesCmd) }

var (
	licenseVendors = []string{"microsoft_365", "google_workspace", "adobe", "other"}
	licenseTerms   = []string{"monthly", "annual", "perpetual"}
)

func newLicensesCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "licenses",
		Aliases: []string{"license", "lic"},
		Short:   "License pools and who holds the seats",
		Long: `Seats bought from Microsoft, Google, Adobe and others, and who they are handed to.

Two numbers matter most: seats nobody holds, and seats held by people who
never sign in. Both are paid for; 'adaa licenses list' shows what that wastes
every month.`,
		GroupID: groupCore,
	}
	cmd.AddCommand(newLicensesListCmd(a), newLicensesViewCmd(a), newLicensesAddCmd(a), newLicensesSeatsCmd(a),
		newLicensesAssignmentsCmd(a), newLicensesAssignCmd(a), newLicensesReleaseCmd(a))
	return cmd
}

func newLicensesListCmd(a *App) *cobra.Command {
	var lo ListOpts
	var vendor string
	var spare bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List license pools",
		Example: `  adaa licenses list
  adaa licenses list --vendor microsoft_365 --spare-seats`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := oneOf("vendor", vendor, licenseVendors...); err != nil {
				return err
			}
			q := url.Values{}
			setQuery(q, cmd, "vendor", "vendor", vendor)
			path, err := a.OrgPath(ctx, "/licenses")
			if err != nil {
				return err
			}
			l, err := a.List(ctx, path, q, lo)
			if err != nil {
				return err
			}
			if spare {
				l = filterListing(l, func(o obj.Obj) bool { n, _ := o.Int("seats_available"); return n > 0 })
			}
			s := a.IO.S()
			err = a.PrintListing(l, "No license pools recorded.", []string{"Plan", "Vendor", "Seats", "Spare", "Inactive", "Monthly", "Wasted", "Term", "ID"}, func(o obj.Obj) []string {
				return []string{o.Str("plan_name"), strings.ReplaceAll(o.Str("vendor"), "_", " "),
					o.Str("seats_assigned") + "/" + o.Str("seats_purchased"),
					licWarnCount(s, o, "seats_available"), licWarnCount(s, o, "seats_inactive"),
					money(o, "monthly_cost_minor"), licWasted(s, o), o.Str("term"), a.dim(o.Str("id"))}
			})
			if err == nil && !a.JSON {
				var total int64
				cur := ""
				for _, o := range l.Items {
					if w, ok := o.Int("wasted_monthly_minor"); ok {
						total += w
						cur = o.Str("currency")
					}
				}
				if total > 0 {
					a.IO.Warnf("%s a month goes to seats nobody uses.", ui.Money(total, cur))
					a.IO.Hint("adaa licenses view <license>   (lists the inactive seats)")
				}
			}
			return err
		},
	}
	addListFlags(cmd, &lo)
	cmd.Flags().StringVar(&vendor, "vendor", "", "Only this vendor: "+strings.Join(licenseVendors, ", "))
	cmd.Flags().BoolVar(&spare, "spare-seats", false, "Only pools with unassigned seats")
	enumFlag(cmd, "vendor", licenseVendors...)
	return cmd
}

func licWarnCount(s ui.Palette, o obj.Obj, field string) string {
	n, ok := o.Int(field)
	if !ok {
		return ""
	}
	if n > 0 {
		return s.Yellow(strconv.FormatInt(n, 10))
	}
	return "0"
}

func licWasted(s ui.Palette, o obj.Obj) string {
	w, ok := o.Int("wasted_monthly_minor")
	if !ok || w == 0 {
		return ""
	}
	return s.Yellow(ui.Money(w, o.Str("currency")))
}

func newLicensesViewCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "view [license]",
		Short:             "Show a license pool, with its unused seats",
		Example:           "  adaa licenses view \"Microsoft 365 Business Standard\"",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindLicense),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindLicense, arg(args))
			if err != nil {
				return err
			}
			o, raw, err := a.Get(ctx, "/licenses/"+id, nil)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(raw)
			}
			var inactive []obj.Obj
			if n, _ := o.Int("seats_inactive"); n > 0 {
				threshold := o.Str("inactive_threshold_days")
				if threshold == "" {
					threshold = "30"
				}
				if l, err := a.ListItems(ctx, "/licenses/"+id+"/assignments", nil); err == nil {
					days, _ := strconv.Atoi(threshold)
					inactive = licInactive(l, days).Items
				}
			}
			a.renderLicense(o, inactive)
			return nil
		},
	}
}

func (a *App) renderLicense(o obj.Obj, inactive []obj.Obj) {
	s := a.IO.S()
	d := a.IO.NewDetail(o.Str("plan_name"), o.Str("id"))
	d.Field("Vendor", strings.ReplaceAll(o.Str("vendor"), "_", " "))
	d.Field("SKU", o.Str("sku"))
	d.Field("Workspace", o.Str("workspace_domain"))
	d.Section("Seats")
	d.Field("Bought", o.Str("seats_purchased"))
	d.Field("Assigned", o.Str("seats_assigned"))
	d.Field("Spare", licWarnCount(s, o, "seats_available"))
	if n, _ := o.Int("seats_inactive"); n > 0 {
		d.Field("Inactive", s.Yellow(fmt.Sprintf("%d (no sign-in for %s days)", n, orNone(o.Str("inactive_threshold_days"), "30"))))
	}
	d.Section("Cost")
	d.Field("Per seat", money(o, "unit_price_minor"))
	d.Field("Monthly", money(o, "monthly_cost_minor"))
	d.Field("Wasted", licWasted(s, o))
	d.Field("Term", o.Str("term"))
	d.Field("Commitment ends", o.Str("commitment_ends_on"))
	d.Field("Renews", o.Str("renews_on"))
	if o.Has("auto_renew") {
		d.Field("Auto-renew", o.Str("auto_renew"))
	}
	d.Field("Synced", a.IO.When(o.Str("synced_at")))
	if len(inactive) > 0 {
		d.Section("Seats nobody is using")
		for _, as := range inactive {
			last := "never signed in"
			if t := as.Str("last_active_at"); t != "" {
				last = "last active " + a.IO.When(t)
			}
			d.Line(join("  ", as.Str("person_name"), s.Dim(as.Str("person_email")), s.Yellow(last)))
		}
	}
	if notes := o.Str("notes"); notes != "" {
		d.Section("Notes")
		d.Line(notes)
	}
	d.Render()
	if len(inactive) > 0 {
		a.IO.Hint("adaa licenses release %s <person>", o.Str("id"))
	}
}

func newLicensesAddCmd(a *App) *cobra.Command {
	var vendor, plan, sku, workspace, term, price, commitmentEnds, renews, notes string
	var seats int
	var autoRenew bool
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Record a license pool",
		Long: `Record seats bought outside adaa, so they are counted and their waste is visible.
Pools that adaa reads from a connected workspace appear on their own.`,
		Example: `  adaa licenses add
  adaa licenses add --vendor adobe --plan "Creative Cloud All Apps" --seats 3 --term annual --price 899.00 --commitment-ends 2027-03-01`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			for flag, v := range map[string]string{"commitment-ends": commitmentEnds, "renews-on": renews} {
				if err := devValidDate(flag, v); err != nil {
					return err
				}
			}
			if err := oneOf("vendor", vendor, licenseVendors...); err != nil {
				return err
			}
			if err := oneOf("term", term, licenseTerms...); err != nil {
				return err
			}
			if vendor == "" {
				var err error
				if vendor, err = a.IO.Select("Vendor", "--vendor", licOptions(licenseVendors)); err != nil {
					return err
				}
			}
			if err := a.need(&plan, "Plan name", "--plan", nil); err != nil {
				return err
			}
			if !cmd.Flags().Changed("seats") {
				s := ""
				if err := a.IO.Input("Seats bought", "--seats", &s, licValidCount); err != nil {
					return err
				}
				seats, _ = strconv.Atoi(strings.TrimSpace(s))
			}
			if term == "" {
				var err error
				if term, err = a.IO.Select("Term", "--term", licOptions(licenseTerms)); err != nil {
					return err
				}
			}
			body := fields{"vendor": vendor, "plan_name": plan, "seats_purchased": seats, "term": term}
			body.str(cmd, "sku", "sku", sku)
			body.str(cmd, "workspace", "workspace", workspace)
			body.str(cmd, "commitment-ends", "commitment_ends_on", commitmentEnds)
			body.str(cmd, "renews-on", "renews_on", renews)
			body.boolean(cmd, "auto-renew", "auto_renew", autoRenew)
			body.str(cmd, "notes", "notes", notes)
			if price != "" {
				minor, err := devParseMinor(price)
				if err != nil {
					return usagef("--price: %v", err)
				}
				body["unit_price_minor"] = minor
			}
			path, err := a.OrgPath(ctx, "/licenses")
			if err != nil {
				return err
			}
			resp, o, err := a.Send(ctx, http.MethodPost, path, map[string]any(body))
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Recorded %s %s", o.Str("plan_name"), a.IO.E().Dim(o.Str("id")))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&vendor, "vendor", "", "Vendor: "+strings.Join(licenseVendors, ", "))
	f.StringVar(&plan, "plan", "", "Plan name, e.g. \"Microsoft 365 Business Standard\"")
	f.StringVar(&sku, "sku", "", "The vendor's identifier for the plan")
	f.StringVar(&workspace, "workspace", "", "Workspace domain the seats belong to")
	f.IntVar(&seats, "seats", 0, "Seats bought")
	f.StringVar(&term, "term", "", "Term: "+strings.Join(licenseTerms, ", "))
	f.StringVar(&price, "price", "", "Price per seat in kroner, e.g. 149.00")
	f.StringVar(&commitmentEnds, "commitment-ends", "", "When an annual commitment ends, YYYY-MM-DD")
	f.StringVar(&renews, "renews-on", "", "Next renewal, YYYY-MM-DD")
	f.BoolVar(&autoRenew, "auto-renew", false, "Whether it renews by itself")
	f.StringVar(&notes, "notes", "", "Free-text notes")
	enumFlag(cmd, "vendor", licenseVendors...)
	enumFlag(cmd, "term", licenseTerms...)
	return cmd
}

func licOptions(values []string) []ui.Option {
	opts := make([]ui.Option, len(values))
	for n, v := range values {
		opts[n] = ui.Option{Label: strings.ReplaceAll(v, "_", " "), Value: v}
	}
	return opts
}

func licValidCount(s string) error {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return errors.New("enter a whole number")
	}
	return nil
}

func newLicensesSeatsCmd(a *App) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "seats <license> <count>",
		Short: "Change how many seats are bought",
		Long: `Change the number of seats bought. Adding seats takes effect at once.

Reducing may not be possible: an annual commitment cannot shrink before it
ends, and a pool cannot drop below the seats that are assigned. Release seats
first with 'adaa licenses release'.`,
		Example: `  adaa licenses seats "Microsoft 365 Business Standard" 12
  adaa licenses seats lic_01J… 10 --dry-run`,
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: a.complete(kindLicense),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			count, err := strconv.Atoi(args[1])
			if err != nil || count < 0 {
				return usagef("seat count must be a whole number, got %q", args[1])
			}
			id, err := a.Resolve(ctx, kindLicense, args[0])
			if err != nil {
				return err
			}
			resp, o, err := a.Apply(ctx, Change{
				Method:   http.MethodPatch,
				Path:     "/licenses/" + id,
				Body:     map[string]any{"seats_purchased": count},
				Question: fmt.Sprintf("Change to %d seats?", count),
				DryRun:   dryRun,
			})
			if err != nil {
				return licSeatsError(ctx, a, id, err)
			}
			if dryRun {
				return nil
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("%s now has %s seats", o.Str("plan_name"), o.Str("seats_purchased"))
			return nil
		},
	}
	addDryRunFlag(cmd, &dryRun)
	return cmd
}

// licSeatsError adds the next step to the two refusals people hit most.
func licSeatsError(ctx context.Context, a *App, id string, err error) error {
	var p *api.Problem
	if !errors.As(err, &p) {
		return err
	}
	switch p.Code() {
	case "cannot-shrink":
		a.IO.Hint("adaa licenses assignments %s --inactive-days 30   (seats worth releasing first)", id)
	case "commitment-active":
		a.IO.Hint("the seat count can come down once the commitment ends")
	}
	return err
}

func newLicensesAssignmentsCmd(a *App) *cobra.Command {
	var inactiveDays int
	cmd := &cobra.Command{
		Use:   "assignments [license]",
		Short: "List who holds a license's seats",
		Example: `  adaa licenses assignments "Microsoft 365 Business Standard"
  adaa licenses assignments lic_01J… --inactive-days 30`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindLicense),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindLicense, arg(args))
			if err != nil {
				return err
			}
			l, err := a.ListItems(ctx, "/licenses/"+id+"/assignments", nil)
			if err != nil {
				return err
			}
			if inactiveDays > 0 {
				l = licInactive(l, inactiveDays)
			}
			empty := "No seats are assigned."
			if inactiveDays > 0 {
				empty = fmt.Sprintf("Every seat has been used in the last %d days.", inactiveDays)
			}
			s := a.IO.S()
			return a.PrintListing(l, empty, []string{"Person", "Email", "Status", "Last active", "Inactive days"}, func(o obj.Obj) []string {
				last := a.IO.When(o.Str("last_active_at"))
				if last == "" {
					last = s.Yellow("never")
				}
				return []string{o.Str("person_name"), o.Str("person_email"), a.status(o.Str("status")), last, o.Str("inactive_days")}
			})
		},
	}
	cmd.Flags().IntVar(&inactiveDays, "inactive-days", 0, "Only seats unused for at least this many days")
	return cmd
}

// licInactive keeps seats unused for at least days. The API reports
// inactive_days on each seat but no longer filters by it.
func licInactive(l *Listing, days int) *Listing {
	return filterListing(l, func(o obj.Obj) bool {
		n, ok := o.Int("inactive_days")
		return ok && n >= int64(days)
	})
}

// licenseAndPerson resolves the two positional arguments of assign/release,
// asking for whichever is missing.
func (a *App) licenseAndPerson(ctx context.Context, args []string) (string, string, error) {
	lid, err := a.Resolve(ctx, kindLicense, arg(args))
	if err != nil {
		return "", "", err
	}
	person := ""
	if len(args) > 1 {
		person = args[1]
	}
	pid, err := a.Resolve(ctx, kindPerson, person)
	return lid, pid, err
}

func newLicensesAssignCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "assign [license] [person]",
		Short: "Give a person a seat",
		Long: `Give a person a seat from the pool. A full pool refuses; buy a seat first
with 'adaa licenses seats', so the extra cost is a decision rather than a surprise.`,
		Example:           "  adaa licenses assign \"Microsoft 365 Business Standard\" kari@firma.no",
		Args:              cobra.MaximumNArgs(2),
		ValidArgsFunction: a.complete(kindLicense),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			lid, pid, err := a.licenseAndPerson(ctx, args)
			if err != nil {
				return err
			}
			resp, o, err := a.Send(ctx, http.MethodPost, "/licenses/"+lid+"/assignments", map[string]any{"person_id": pid})
			if err != nil {
				var p *api.Problem
				if errors.As(err, &p) && p.Status == http.StatusConflict {
					a.IO.Hint("adaa licenses seats %s <more seats>", lid)
				}
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Assigning a seat to %s at the vendor.", orNone(o.Str("person_name"), pid))
			return nil
		},
	}
}

func newLicensesReleaseCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "release [license] [person]",
		Short: "Take a seat back from a person",
		Long: `Take a seat back. The person loses access to what the license covers.

Releasing a seat does not lower the bill by itself: the seat goes back to the
pool. Lower the count with 'adaa licenses seats' to stop paying for it.`,
		Example:           "  adaa licenses release \"Adobe Creative Cloud\" ola@firma.no",
		Args:              cobra.MaximumNArgs(2),
		ValidArgsFunction: a.complete(kindLicense),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			lid, pid, err := a.licenseAndPerson(ctx, args)
			if err != nil {
				return err
			}
			if err := a.Confirm("Release this seat? The person loses access to what it covers."); err != nil {
				return err
			}
			if _, err := a.Do(ctx, http.MethodDelete, "/licenses/"+lid+"/assignments/"+pid, nil, nil); err != nil {
				return err
			}
			a.IO.Successf("Released the seat; it is back in the pool.")
			a.IO.Hint("adaa licenses seats %s <count>   (to stop paying for it)", lid)
			return nil
		},
	}
}
