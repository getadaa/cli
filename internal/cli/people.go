package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() { register(newPeopleCmd) }

var (
	employmentStatuses = []string{"planned", "active", "departed"}
	portalRoles        = []string{"none", "org_member", "org_admin"}
)

func newPeopleCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "people",
		Aliases: []string{"person"},
		Short:   "Onboard, change and offboard the people in your company",
		Long: `Everyone who works at the company, and what they should have.

Onboarding is adding a person with the services they need; adaa works out
what has to be created and does it. Offboarding is the same in reverse.`,
		GroupID: groupCore,
	}
	cmd.AddCommand(
		newPeopleListCmd(a),
		newPeopleViewCmd(a),
		newPeopleAddCmd(a),
		newPeopleEditCmd(a),
		newPeopleOffboardCmd(a),
		newPeopleGrantCmd(a),
		newPeopleRevokeCmd(a),
		newPeopleResourcesCmd(a),
	)
	return cmd
}

func newPeopleListCmd(a *App) *cobra.Command {
	var lo ListOpts
	var status string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List people",
		Example: `  adaa people list
  adaa people list --status planned
  adaa people list --all --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := oneOf("status", status, employmentStatuses...); err != nil {
				return err
			}
			ctx := cmd.Context()
			path, err := a.OrgPath(ctx, "/people")
			if err != nil {
				return err
			}
			q := url.Values{}
			setQuery(q, cmd, "status", "employment_status", status)
			l, err := a.List(ctx, path, q, lo)
			if err != nil {
				return err
			}
			return a.PrintListing(l, "No people yet. Add someone with `adaa people add`.",
				[]string{"ID", "Name", "Email", "Title", "Status", "Role"},
				func(p obj.Obj) []string {
					return []string{a.dim(p.Str("id")), p.Str("full_name"), p.Str("email"), p.Str("job_title"),
						a.status(p.Str("employment_status")), peopleRoleLabel(p.Str("portal_role"))}
				})
		},
	}
	addListFlags(cmd, &lo)
	cmd.Flags().StringVar(&status, "status", "", "Only people with this employment status: planned, active or departed")
	enumFlag(cmd, "status", employmentStatuses...)
	return cmd
}

func peopleRoleLabel(r string) string {
	switch r {
	case "org_admin":
		return "admin"
	case "org_member":
		return "member"
	}
	return ""
}

func newPeopleViewCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "view [person]",
		Short:             "Show a person and everything they have",
		Example:           "  adaa people view kari@firma.no\n  adaa people view me",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindPerson),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindPerson, arg(args))
			if err != nil {
				return err
			}
			p, raw, err := a.Get(ctx, "/people/"+id, nil)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(raw)
			}
			// The extra sections are a courtesy; a caller without the
			// permission to read them still gets the person.
			ents, _ := a.ListItems(ctx, "/people/"+id+"/entitlements", nil)
			asg, _, _ := a.Get(ctx, "/people/"+id+"/assignments", nil)
			a.renderPerson(p, ents, asg)
			return nil
		},
	}
}

func (a *App) renderPerson(p obj.Obj, ents *Listing, asg obj.Obj) {
	s := a.IO.S()
	d := a.IO.NewDetail(p.Str("full_name"), p.Str("id"))
	d.Field("Email", p.Str("email"))
	d.Field("Title", p.Str("job_title"))
	d.Field("Phone", p.Str("phone"))
	d.Field("Status", a.status(p.Str("employment_status")))
	role := peopleRoleLabel(p.Str("portal_role"))
	if role == "" {
		role = s.Dim("cannot sign in")
	}
	d.Field("Portal", role)
	d.Field("Started", p.Str("started_on"))
	d.Field("Ended", p.Str("ended_on"))

	if ents != nil && len(ents.Items) > 0 {
		d.Section("Should have")
		for _, e := range ents.Items {
			name := e.Str("service_name")
			if name == "" {
				name = e.Str("service_code")
			}
			if q, _ := e.Int("quantity"); q > 1 {
				name += fmt.Sprintf(" ×%d", q)
			}
			mark := s.Green("✓")
			if e.Has("fulfilled") && !e.Bool("fulfilled") {
				mark = s.Yellow("…")
				name += " " + s.Dim("(not in place yet)")
			}
			d.Line(mark + " " + name + " " + s.Dim(e.Str("id")))
		}
	}

	if asg != nil {
		accounts := asg.List("workspace_accounts")
		licenses := asg.List("license_seats")
		subs := asg.List("subscription_seats")
		devices := asg.List("devices")
		others := asg.List("other_resources")
		if len(accounts)+len(licenses)+len(subs)+len(devices)+len(others) > 0 {
			d.Section("Has")
		}
		for _, w := range accounts {
			line := w.Str("user_principal_name") + " " + s.Dim(w.Str("vendor")+" account") + " " + a.status(w.Str("status"))
			if w.Has("mfa_enabled") && !w.Bool("mfa_enabled") {
				line += " " + s.Yellow("no MFA")
			}
			d.Line(line)
		}
		for _, l := range licenses {
			line := l.Str("plan_name") + " " + s.Dim(l.Str("vendor")+" seat")
			if n, ok := l.Int("inactive_days"); ok && n > 0 {
				line += " " + s.Yellow(fmt.Sprintf("unused for %d days", n))
			}
			d.Line(line)
		}
		for _, sub := range subs {
			line := sub.Str("service_name")
			if m, ok := sub.Int("monthly_cost_minor"); ok {
				line += " " + s.Dim(ui.Money(m, "")+"/month")
			}
			d.Line(line)
		}
		for _, dev := range devices {
			d.Line(dev.Str("name") + " " + s.Dim(dev.Str("kind")) + " " + a.status(dev.Str("status")))
		}
		for _, r := range others {
			d.Line(r.Str("display_name") + " " + s.Dim(r.Str("kind")) + " " + a.status(r.Str("state")))
		}
		if c := asg.Obj("cost"); c != nil {
			if m, ok := c.Int("monthly_minor"); ok {
				d.Section("Cost")
				line := ui.Money(m, c.Str("currency")) + " per month"
				if w, ok := c.Int("wasted_monthly_minor"); ok && w > 0 {
					line += ", " + s.Yellow(ui.Money(w, c.Str("currency"))+" of it unused")
				}
				if n, _ := c.Int("lines_awaiting_price"); n > 0 {
					line += s.Dim(" (estimate)")
				}
				d.Line(line)
			}
		}
	}
	d.Render()
}

// peopleServiceSpecs turns --service code[:qty] into entitlement objects.
func peopleServiceSpecs(specs []string) ([]map[string]any, error) {
	var out []map[string]any
	for _, spec := range specs {
		for _, part := range strings.Split(spec, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			code, qty, hasQty := strings.Cut(part, ":")
			e := map[string]any{"service_code": code}
			if hasQty {
				n, err := strconv.Atoi(qty)
				if err != nil || n < 1 {
					return nil, usagef("--service %q: the quantity after : must be a positive number", part)
				}
				e["quantity"] = n
			}
			out = append(out, e)
		}
	}
	return out, nil
}

func peopleValidDate(s string) error {
	if s == "" {
		return nil
	}
	if _, err := time.Parse(time.DateOnly, s); err != nil {
		return errors.New("use YYYY-MM-DD")
	}
	return nil
}

func peopleIsFuture(date string) bool {
	t, err := time.ParseInLocation(time.DateOnly, date, time.Local)
	if err != nil {
		return false
	}
	y, m, d := time.Now().Date()
	return t.After(time.Date(y, m, d, 0, 0, 0, 0, time.Local))
}

func newPeopleAddCmd(a *App) *cobra.Command {
	var name, email, title, phone, startsOn, role, status string
	var services []string
	var dryRun bool
	cmd := &cobra.Command{
		Use:     "add",
		Aliases: []string{"onboard", "create"},
		Short:   "Onboard someone new",
		Long: `Onboard someone: say who they are and what they should have, and adaa creates
the mailbox, accounts and seats that are missing.

In a terminal this is a short form, followed by a preview of the work and what
it costs before anything happens. Without one, pass the flags and --yes; use
--dry-run to see the preview first.

A start date in the future prepares everything without provisioning early.`,
		Example: `  adaa people add
  adaa people add --name "Ola Nordmann" --email ola@firma.no --starts-on 2026-10-01 \
    --service office-suite --service laptop:1 --dry-run
  adaa people add --name "Ola Nordmann" --email ola@firma.no --role org_member --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			for flag, v := range map[string]string{"role": role, "status": status} {
				allowed := portalRoles
				if flag == "status" {
					allowed = employmentStatuses[:2]
				}
				if err := oneOf(flag, v, allowed...); err != nil {
					return err
				}
			}
			if err := peopleValidDate(startsOn); err != nil {
				return usagef("--starts-on: %v", err)
			}
			ents, err := peopleServiceSpecs(services)
			if err != nil {
				return err
			}

			if a.IO.Interactive() && (name == "" || email == "") {
				codes, err := a.onboardingForm(ctx, &name, &email, &title, &phone, &startsOn, &role, len(ents) == 0)
				if err != nil {
					return err
				}
				for _, c := range codes {
					ents = append(ents, map[string]any{"service_code": c})
				}
			}
			if err := a.need(&name, "Full name", "--name", nil); err != nil {
				return err
			}
			if err := a.need(&email, "Work email", "--email", validEmail); err != nil {
				return err
			}

			body := map[string]any{"full_name": name, "email": email}
			if title != "" {
				body["job_title"] = title
			}
			if phone != "" {
				body["phone"] = phone
			}
			if startsOn != "" {
				body["started_on"] = startsOn
			}
			switch {
			case status != "":
				body["employment_status"] = status
			case startsOn != "" && peopleIsFuture(startsOn):
				body["employment_status"] = "planned"
			}
			if role != "" && role != "none" {
				body["portal_role"] = role
			}
			if len(ents) > 0 {
				body["entitlements"] = ents
			}

			path, err := a.OrgPath(ctx, "/people")
			if err != nil {
				return err
			}
			resp, p, err := a.Apply(ctx, Change{Method: http.MethodPost, Path: path, Body: body,
				Question: "Onboard " + name + "?", DryRun: dryRun})
			if err != nil || dryRun {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Added %s %s", p.Str("full_name"), a.IO.E().Dim(p.Str("id")))
			a.ReportResult(p)
			a.IO.Hint("adaa people view %s", p.Str("id"))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "Full name")
	f.StringVar(&email, "email", "", "Work email address")
	f.StringVar(&title, "title", "", "Job title")
	f.StringVar(&phone, "phone", "", "Phone number")
	f.StringVar(&startsOn, "starts-on", "", "First working day, YYYY-MM-DD")
	f.StringVar(&role, "role", "", "Portal access: none, org_member or org_admin")
	f.StringVar(&status, "status", "", "Employment status: planned or active (default: planned when --starts-on is in the future)")
	f.StringArrayVar(&services, "service", nil, "Service to give them, as code or code:quantity (repeatable; see `adaa services list`)")
	addDryRunFlag(cmd, &dryRun)
	enumFlag(cmd, "role", portalRoles...)
	enumFlag(cmd, "status", "planned", "active")
	_ = cmd.RegisterFlagCompletionFunc("service", a.peopleCompleteServices)
	return cmd
}

func (a *App) peopleCompleteServices(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	l, err := a.ListItems(cmd.Context(), "/services", url.Values{"active": {"true"}, "limit": {"200"}})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveError
	}
	var out []cobra.Completion
	for _, s := range l.Items {
		out = append(out, cobra.CompletionWithDesc(s.Str("code"), s.Str("name")))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// onboardingForm asks for everything at once, prefilled from any flags given,
// and returns the service codes picked.
func (a *App) onboardingForm(ctx context.Context, name, email, title, phone, startsOn, role *string, askServices bool) ([]string, error) {
	if *role == "" {
		*role = "none"
	}
	groups := []*huh.Group{
		huh.NewGroup(
			huh.NewInput().Title("Full name").Value(name).Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("a name is needed")
				}
				return nil
			}),
			huh.NewInput().Title("Work email").Value(email).Validate(validEmail),
			huh.NewInput().Title("Job title").Description("Optional").Value(title),
			huh.NewInput().Title("Phone").Description("Optional").Value(phone),
			huh.NewInput().Title("First working day").Description("YYYY-MM-DD, empty for today").Value(startsOn).Validate(peopleValidDate),
			huh.NewSelect[string]().Title("Can they sign in to adaa?").Value(role).Options(
				huh.NewOption("No — they just need their tools", "none"),
				huh.NewOption("Yes, as a member (report problems, see their own things)", "org_member"),
				huh.NewOption("Yes, as an admin (manage people, approve changes, see costs)", "org_admin"),
			),
		).Title("Who is starting?"),
	}

	var codes []string
	if askServices {
		l, err := ui.Spin(a.IO, "Fetching the service catalog…", func() (*Listing, error) {
			return a.ListItems(ctx, "/services", url.Values{"active": {"true"}, "limit": {"200"}})
		})
		if err == nil && len(l.Items) > 0 {
			var opts []huh.Option[string]
			for _, s := range l.Items {
				label := s.Str("name")
				if p, ok := s.Int("unit_price_minor"); ok {
					label += "  " + a.IO.E().Dim(ui.Money(p, s.Str("currency"))+" per "+strings.ReplaceAll(s.Str("pricing_unit"), "_", " "))
				}
				opts = append(opts, huh.NewOption(label, s.Str("code")))
			}
			ms := huh.NewMultiSelect[string]().Title("What should they have?").
				Description("Space to select, enter to continue. You see the cost before anything happens.").
				Options(opts...).Value(&codes)
			if len(opts) > 8 {
				ms = ms.Height(14).Filterable(true)
			}
			groups = append(groups, huh.NewGroup(ms))
		}
	}
	if err := a.IO.Run(groups...); err != nil {
		return nil, err
	}
	return codes, nil
}

func newPeopleEditCmd(a *App) *cobra.Command {
	var name, email, title, phone, startsOn, role, status string
	cmd := &cobra.Command{
		Use:               "edit [person]",
		Short:             "Change a person's details",
		Long:              "Change a person's details. Only the flags you pass are changed; pass an empty value to clear a field.",
		Example:           "  adaa people edit kari@firma.no --title \"Head of Sales\"\n  adaa people edit ola@firma.no --role org_admin\n  adaa people edit ola@firma.no --phone \"\"",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindPerson),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := oneOf("role", role, portalRoles...); err != nil {
				return err
			}
			if err := oneOf("status", status, employmentStatuses...); err != nil {
				return err
			}
			if err := peopleValidDate(startsOn); err != nil {
				return usagef("--starts-on: %v", err)
			}
			body := fields{}
			body.str(cmd, "name", "full_name", name)
			body.str(cmd, "email", "email", email)
			body.str(cmd, "title", "job_title", title)
			body.str(cmd, "phone", "phone", phone)
			body.str(cmd, "starts-on", "started_on", startsOn)
			body.str(cmd, "status", "employment_status", status)
			if cmd.Flags().Changed("role") {
				if role == "none" || role == "" {
					body["portal_role"] = nil
				} else {
					body["portal_role"] = role
				}
			}
			if len(body) == 0 {
				return usagef("nothing to change; pass at least one of --name, --email, --title, --phone, --starts-on, --role, --status")
			}
			id, err := a.Resolve(ctx, kindPerson, arg(args))
			if err != nil {
				return err
			}
			resp, p, err := a.Send(ctx, http.MethodPatch, "/people/"+id, map[string]any(body))
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Updated %s.", p.Str("full_name"))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "Full name")
	f.StringVar(&email, "email", "", "Work email address")
	f.StringVar(&title, "title", "", "Job title")
	f.StringVar(&phone, "phone", "", "Phone number")
	f.StringVar(&startsOn, "starts-on", "", "First working day, YYYY-MM-DD")
	f.StringVar(&role, "role", "", "Portal access: none, org_member or org_admin")
	f.StringVar(&status, "status", "", "Employment status: planned, active or departed")
	enumFlag(cmd, "role", portalRoles...)
	enumFlag(cmd, "status", employmentStatuses...)
	return cmd
}

func newPeopleOffboardCmd(a *App) *cobra.Command {
	var lastDay, forwardTo, note string
	var keepDays int
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "offboard [person]",
		Short: "Offboard someone who is leaving",
		Long: `Offboard someone: adaa works out everything they hold and raises the work to
take it back. Anything that revokes or deletes waits for approval.

Somebody usually still needs the mail, so the mailbox can be kept for a while
and forwarded to a colleague.`,
		Example: `  adaa people offboard ola@firma.no
  adaa people offboard ola@firma.no --last-day 2026-10-31 --keep-mailbox-days 90 \
    --forward-mail-to kari@firma.no --dry-run`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindPerson),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := peopleValidDate(lastDay); err != nil {
				return usagef("--last-day: %v", err)
			}
			id, err := a.Resolve(ctx, kindPerson, arg(args))
			if err != nil {
				return err
			}
			p, _, err := a.Get(ctx, "/people/"+id, nil)
			if err != nil {
				return err
			}
			name := p.Str("full_name")

			anySet := cmd.Flags().Changed("last-day") || cmd.Flags().Changed("keep-mailbox-days") || cmd.Flags().Changed("forward-mail-to")
			if a.IO.Interactive() && !a.Yes && !anySet {
				if err := a.offboardForm(ctx, id, name, &lastDay, &keepDays, &forwardTo); err != nil {
					return err
				}
			}

			body := map[string]any{}
			if lastDay != "" {
				body["ended_on"] = lastDay
			}
			if keepDays > 0 {
				body["keep_mailbox_days"] = keepDays
			}
			if note != "" {
				body["note"] = note
			}
			if forwardTo != "" && forwardTo != "-" {
				fid, err := a.Resolve(ctx, kindPerson, forwardTo)
				if err != nil {
					return err
				}
				body["forward_mail_to"] = fid
			}

			resp, r, err := a.Apply(ctx, Change{Method: http.MethodPost, Path: "/people/" + id + "/offboard", Body: body,
				Question: "Offboard " + name + "?", DryRun: dryRun})
			if err != nil || dryRun {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.ReportResult(r)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&lastDay, "last-day", "", "Last working day, YYYY-MM-DD (default today)")
	f.IntVar(&keepDays, "keep-mailbox-days", 0, "Keep the mailbox this many days before archiving it")
	f.StringVar(&forwardTo, "forward-mail-to", "", "Forward their mail to this person (email, name or id)")
	f.StringVar(&note, "note", "", "Note for whoever does the work")
	addDryRunFlag(cmd, &dryRun)
	_ = cmd.RegisterFlagCompletionFunc("forward-mail-to", a.complete(kindPerson))
	return cmd
}

func (a *App) offboardForm(ctx context.Context, id, name string, lastDay *string, keepDays *int, forwardTo *string) error {
	if *lastDay == "" {
		*lastDay = time.Now().Format(time.DateOnly)
	}
	keep := "30"
	if *keepDays > 0 {
		keep = strconv.Itoa(*keepDays)
	}
	opts := []huh.Option[string]{huh.NewOption("Don't forward it", "-")}
	path, err := a.OrgPath(ctx, "/people")
	if err != nil {
		return err
	}
	if l, err := a.List(ctx, path, url.Values{"employment_status": {"active"}}, ListOpts{Limit: 500}); err == nil {
		for _, p := range l.Items {
			if p.Str("id") != id {
				opts = append(opts, huh.NewOption(kindPerson.Label(p), p.Str("id")))
			}
		}
	}
	fwd := "-"
	sel := huh.NewSelect[string]().Title("Forward their mail to").Options(opts...).Value(&fwd)
	if len(opts) > 8 {
		sel = sel.Height(10).Filtering(true)
	}
	err = a.IO.Run(huh.NewGroup(
		huh.NewInput().Title("Last working day").Description("YYYY-MM-DD").Value(lastDay).Validate(peopleValidDate),
		huh.NewInput().Title("Keep the mailbox for how many days?").Description("0 archives it straight away").Value(&keep).
			Validate(func(s string) error {
				if n, err := strconv.Atoi(strings.TrimSpace(s)); err != nil || n < 0 {
					return errors.New("a number of days")
				}
				return nil
			}),
		sel,
	).Title("Offboarding " + name))
	if err != nil {
		return err
	}
	*keepDays, _ = strconv.Atoi(strings.TrimSpace(keep))
	*forwardTo = fwd
	return nil
}

func newPeopleGrantCmd(a *App) *cobra.Command {
	var quantity int
	var note string
	var dryRun bool
	cmd := &cobra.Command{
		Use:     "grant <person> <service-code>",
		Short:   "Give a person a service",
		Long:    "Give a person a service from the catalog. adaa provisions it; you see the cost before anything happens.",
		Example: "  adaa people grant kari@firma.no office-suite\n  adaa people grant kari@firma.no phone-plan --dry-run",
		Args:    cobra.RangeArgs(0, 2),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return a.complete(kindPerson)(cmd, args, toComplete)
			}
			if len(args) == 1 {
				return a.peopleCompleteServices(cmd, args, toComplete)
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindPerson, arg(args))
			if err != nil {
				return err
			}
			code := ""
			if len(args) > 1 {
				code = args[1]
			} else {
				code, err = a.peoplePickService(ctx)
				if err != nil {
					return err
				}
			}
			body := map[string]any{"service_code": code}
			if quantity > 0 {
				body["quantity"] = quantity
			}
			if note != "" {
				body["note"] = note
			}
			resp, e, err := a.Apply(ctx, Change{Method: http.MethodPost, Path: "/people/" + id + "/entitlements", Body: body,
				Question: "Grant " + code + "?", DryRun: dryRun})
			if err != nil || dryRun {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			svc := e.Str("service_name")
			if svc == "" {
				svc = code
			}
			a.IO.Successf("Granted %s %s", svc, a.IO.E().Dim(e.Str("id")))
			a.IO.Hint("adaa people view %s", id)
			return nil
		},
	}
	cmd.Flags().IntVar(&quantity, "quantity", 0, "How many (default 1)")
	cmd.Flags().StringVar(&note, "note", "", "Why, for the record")
	addDryRunFlag(cmd, &dryRun)
	return cmd
}

func (a *App) peoplePickService(ctx context.Context) (string, error) {
	if !a.IO.Interactive() {
		return "", &ui.NoInputError{What: "service code", Flag: "the service code as an argument (see `adaa services list`)"}
	}
	l, err := a.ListItems(ctx, "/services", url.Values{"active": {"true"}, "limit": {"200"}})
	if err != nil {
		return "", err
	}
	var opts []ui.Option
	for _, s := range l.Items {
		opts = append(opts, ui.Option{Label: s.Str("name") + " (" + s.Str("code") + ")", Value: s.Str("code")})
	}
	return a.IO.Select("Which service?", "the service code as an argument", opts)
}

func newPeopleRevokeCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "revoke <person> <entitlement>",
		Short:   "Take a service away from a person",
		Long:    "Take a service away from a person, by entitlement id or service code. The resulting removal waits for approval.",
		Example: "  adaa people revoke kari@firma.no office-suite\n  adaa people revoke kari@firma.no ent_01J…",
		Args:    cobra.RangeArgs(0, 2),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return a.complete(kindPerson)(cmd, args, toComplete)
			}
			if len(args) == 1 {
				id, err := a.Resolve(cmd.Context(), kindPerson, args[0])
				if err != nil {
					return nil, cobra.ShellCompDirectiveNoFileComp
				}
				l, err := a.ListItems(cmd.Context(), "/people/"+id+"/entitlements", nil)
				if err != nil {
					return nil, cobra.ShellCompDirectiveNoFileComp
				}
				var out []cobra.Completion
				for _, e := range l.Items {
					out = append(out, cobra.CompletionWithDesc(e.Str("service_code"), e.Str("service_name")))
				}
				return out, cobra.ShellCompDirectiveNoFileComp
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindPerson, arg(args))
			if err != nil {
				return err
			}
			l, err := a.ListItems(ctx, "/people/"+id+"/entitlements", nil)
			if err != nil {
				return err
			}
			var ent obj.Obj
			if len(args) > 1 {
				for _, e := range l.Items {
					if e.Str("id") == args[1] || strings.EqualFold(e.Str("service_code"), args[1]) {
						ent = e
						break
					}
				}
				if ent == nil {
					return fmt.Errorf("they have no entitlement matching %q (see `adaa people view %s`)", args[1], id)
				}
			} else {
				if len(l.Items) == 0 {
					return errors.New("they have no entitlements to revoke")
				}
				var opts []ui.Option
				for _, e := range l.Items {
					opts = append(opts, ui.Option{Label: e.Str("service_name") + " (" + e.Str("service_code") + ")", Value: e.Str("id")})
				}
				eid, err := a.IO.Select("Which one?", "the entitlement as the second argument", opts)
				if err != nil {
					return err
				}
				for _, e := range l.Items {
					if e.Str("id") == eid {
						ent = e
					}
				}
			}
			label := ent.Str("service_name")
			if label == "" {
				label = ent.Str("service_code")
			}
			if err := a.Confirm("Revoke " + label + "?"); err != nil {
				return err
			}
			if _, err := a.Do(ctx, http.MethodDelete, "/people/"+id+"/entitlements/"+ent.Str("id"), nil, nil); err != nil {
				return err
			}
			a.IO.Successf("Revoked %s. Removing it waits for approval.", label)
			a.IO.Hint("adaa tasks list --awaiting-approval")
			return nil
		},
	}
	return cmd
}

func newPeopleResourcesCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "resources [person]",
		Short:             "List what actually exists for a person",
		Example:           "  adaa people resources kari@firma.no",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindPerson),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindPerson, arg(args))
			if err != nil {
				return err
			}
			l, err := a.ListItems(ctx, "/people/"+id+"/resources", nil)
			if err != nil {
				return err
			}
			return a.PrintListing(l, "Nothing is registered to them.",
				[]string{"ID", "Name", "Kind", "State", "Managed"},
				func(r obj.Obj) []string {
					return []string{a.dim(r.Str("id")), r.Str("display_name"), strings.ReplaceAll(r.Str("kind"), "_", " "),
						a.status(r.Str("state")), r.Str("managed")}
				})
		},
	}
}
