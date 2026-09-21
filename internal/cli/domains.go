package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/getadaa/cli/internal/api"
	"github.com/getadaa/cli/internal/dnscheck"
	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() { register(newDomainsCmd) }

var productCodes = []string{"mail", "workspace", "server", "backup", "license", "device"}

func newDomainsCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "domains",
		Aliases: []string{"domain"},
		Short:   "Domains, their DNS records, SPF and registrar transfers",
		Long: `Domains the company uses, and the DNS records its services need.

A domain is either managed by adaa (we run the zone) or external (DNS stays at
your registrar; adaa says what should be there and checks that it is).`,
		GroupID: groupProducts,
	}
	cmd.AddCommand(
		newDomainsListCmd(a), newDomainsViewCmd(a), newDomainsAddCmd(a), newDomainsEditCmd(a),
		newDomainsRemoveCmd(a), newDomainsVerifyCmd(a), newDomainsServiceCmd(a), newDomainsCheckCmd(a),
		newDNSCmd(a), newSPFCmd(a), newTransferCmd(a), newAuthCodeCmd(a),
	)
	return cmd
}

func newDomainsListCmd(a *App) *cobra.Command {
	var lo ListOpts
	var management string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List domains",
		Example: `  adaa domains list
  adaa domains list --management external`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := oneOf("management", management, "adaa", "external"); err != nil {
				return err
			}
			path, err := a.OrgPath(cmd.Context(), "/domains")
			if err != nil {
				return err
			}
			q := url.Values{}
			setQuery(q, cmd, "management", "management", management)
			l, err := a.List(cmd.Context(), path, q, lo)
			if err != nil {
				return err
			}
			return a.PrintListing(l, "No domains yet. Add one with `adaa domains add <name>`.",
				[]string{"Name", "Management", "Verification", "Records", "Registered until", "ID"},
				func(d obj.Obj) []string {
					return []string{d.Str("name"), d.Str("management"), a.status(d.Str("verification_status")),
						domainRecordSummary(a, d), d.Str("registered_until"), a.dim(d.Str("id"))}
				})
		},
	}
	addListFlags(cmd, &lo)
	cmd.Flags().StringVar(&management, "management", "", "Only adaa or external domains")
	enumFlag(cmd, "management", "adaa", "external")
	return cmd
}

func domainRecordSummary(a *App, d obj.Obj) string {
	total, _ := d.Int("records_total")
	published, _ := d.Int("records_published")
	missing, _ := d.Int("records_missing")
	mismatched, _ := d.Int("records_mismatched")
	s := fmt.Sprintf("%d/%d published", published, total)
	if missing > 0 {
		s += ", " + a.IO.S().Red(fmt.Sprintf("%d missing", missing))
	}
	if mismatched > 0 {
		s += ", " + a.IO.S().Red(fmt.Sprintf("%d wrong", mismatched))
	}
	return s
}

func newDomainsViewCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "view [domain]",
		Short:             "Show a domain",
		Example:           `  adaa domains view firma.no`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindDomain),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.Resolve(cmd.Context(), kindDomain, arg(args))
			if err != nil {
				return err
			}
			d, raw, err := a.Get(cmd.Context(), "/domains/"+id, nil)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, d, a.renderDomain)
		},
	}
}

func (a *App) renderDomain(d obj.Obj) {
	yesNo := func(path string) string {
		if !d.Has(path) {
			return ""
		}
		if d.Bool(path) {
			return "on"
		}
		return "off"
	}
	v := a.IO.NewDetail(d.Str("name"), d.Str("id"))
	v.Field("Management", d.Str("management"))
	v.Field("Verification", a.status(d.Str("verification_status")))
	v.Field("Summary", d.Str("summary"))
	v.Field("Records", domainRecordSummary(a, d))
	v.Field("Nameservers", strings.Join(d.Strings("nameservers"), ", "))
	v.Field("Last checked", a.IO.When(d.Str("last_checked_at")))
	v.Section("Registration")
	v.Field("Registrar", d.Str("registrar"))
	v.Field("Registered until", d.Str("registered_until"))
	v.Field("Auto-renew", yesNo("auto_renew"))
	v.Field("Transfer lock", yesNo("transfer_lock"))
	v.Field("Transfer", a.status(d.Str("transfer_status")))
	v.Field("Transfer possible", d.Str("transfer_eligible_on"))
	if svcs := d.List("services"); len(svcs) > 0 {
		v.Section("Services publishing here")
		for _, s := range svcs {
			state := a.IO.S().Green("on")
			if !s.Bool("enabled") {
				state = a.IO.S().Dim("off")
			}
			v.Field(s.Str("product_code"), fmt.Sprintf("%s, %s records", state, s.Str("record_count")))
		}
	}
	v.Field("Notes", d.Str("notes"))
	v.Render()
	missing, _ := d.Int("records_missing")
	mismatched, _ := d.Int("records_mismatched")
	if missing+mismatched > 0 {
		a.IO.Hint("adaa domains dns list %s --status missing", d.Str("name"))
	}
}

func newDomainsAddCmd(a *App) *cobra.Command {
	var management, registrar, registeredUntil, notes string
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a domain",
		Long: `Add a domain. It starts out pending verification.

--management adaa means adaa runs the zone. --management external keeps DNS
where it is; adaa then tells you what records to publish and checks them.`,
		Example: `  adaa domains add firma.no --management external
  adaa domains add firma.no --management adaa --registrar Domeneshop`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := arg(args)
			if err := a.need(&name, "Domain name", "a domain name as the argument", nil); err != nil {
				return err
			}
			if management == "" && a.IO.Interactive() {
				m, err := a.IO.Select("Who runs DNS for "+name+"?", "--management", []ui.Option{
					{Label: "Keep DNS where it is (external)", Value: "external"},
					{Label: "Let adaa run the zone", Value: "adaa"},
				})
				if err != nil {
					return err
				}
				management = m
			}
			if err := oneOf("management", management, "adaa", "external"); err != nil {
				return err
			}
			body := fields{"name": strings.ToLower(strings.TrimSpace(name))}
			if management != "" {
				body["management"] = management
			}
			body.str(cmd, "registrar", "registrar", registrar)
			body.str(cmd, "registered-until", "registered_until", registeredUntil)
			body.str(cmd, "notes", "notes", notes)
			path, err := a.OrgPath(cmd.Context(), "/domains")
			if err != nil {
				return err
			}
			resp, d, err := a.Send(cmd.Context(), http.MethodPost, path, body)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Added %s %s", d.Str("name"), a.IO.E().Dim(d.Str("id")))
			if d.Str("management") == "external" {
				a.IO.Hint("adaa domains dns export %s   (the records to publish at your registrar)", d.Str("name"))
			}
			a.IO.Hint("adaa domains verify %s", d.Str("name"))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&management, "management", "", "Who runs DNS: adaa or external")
	f.StringVar(&registrar, "registrar", "", "Where the domain is registered")
	f.StringVar(&registeredUntil, "registered-until", "", "Registration expiry, YYYY-MM-DD")
	f.StringVar(&notes, "notes", "", "Free-text notes")
	enumFlag(cmd, "management", "adaa", "external")
	return cmd
}

func newDomainsEditCmd(a *App) *cobra.Command {
	var management, registrar, registeredUntil, notes string
	var autoRenew, transferLock bool
	cmd := &cobra.Command{
		Use:   "edit [domain]",
		Short: "Change a domain's settings",
		Example: `  adaa domains edit firma.no --auto-renew
  adaa domains edit firma.no --transfer-lock=false`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindDomain),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := oneOf("management", management, "adaa", "external"); err != nil {
				return err
			}
			body := fields{}
			body.str(cmd, "management", "management", management)
			body.str(cmd, "registrar", "registrar", registrar)
			body.str(cmd, "registered-until", "registered_until", registeredUntil)
			body.str(cmd, "notes", "notes", notes)
			body.boolean(cmd, "auto-renew", "auto_renew", autoRenew)
			body.boolean(cmd, "transfer-lock", "transfer_lock", transferLock)
			if len(body) == 0 {
				return usagef("nothing to change; pass at least one flag (see --help)")
			}
			id, err := a.Resolve(cmd.Context(), kindDomain, arg(args))
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("transfer-lock") && !transferLock {
				if err := a.Confirm("Turn off the transfer lock? Anyone with the auth code can then move the domain."); err != nil {
					return err
				}
			}
			resp, d, err := a.Send(cmd.Context(), http.MethodPatch, "/domains/"+id, body)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Updated %s", d.Str("name"))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&management, "management", "", "Who runs DNS: adaa or external")
	f.StringVar(&registrar, "registrar", "", "Where the domain is registered")
	f.StringVar(&registeredUntil, "registered-until", "", "Registration expiry, YYYY-MM-DD")
	f.StringVar(&notes, "notes", "", "Free-text notes")
	f.BoolVar(&autoRenew, "auto-renew", false, "Renew the registration automatically")
	f.BoolVar(&transferLock, "transfer-lock", false, "Registrar lock against transfers (only where adaa is the registrar)")
	enumFlag(cmd, "management", "adaa", "external")
	return cmd
}

func newDomainsRemoveCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:     "remove [domain]",
		Aliases: []string{"rm"},
		Short:   "Remove a domain",
		Long: `Remove a domain from adaa. This is refused while a service still publishes
records to it; turn the service off first with 'adaa domains service'.`,
		Example:           `  adaa domains remove old-firma.no`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindDomain),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.Resolve(cmd.Context(), kindDomain, arg(args))
			if err != nil {
				return err
			}
			if err := a.Confirm("Remove this domain from adaa?"); err != nil {
				return err
			}
			if _, err := a.Do(cmd.Context(), http.MethodDelete, "/domains/"+id, nil, nil); err != nil {
				return err
			}
			a.IO.Successf("Removed %s", id)
			return nil
		},
	}
}

func newDomainsVerifyCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "verify [domain]",
		Short: "Ask adaa to re-check the domain's DNS now",
		Long: `Ask adaa to re-read public DNS and compare it with what should be there. This
is the "I have added the records" button; it also runs on a schedule.

To check from this computer instead, use 'adaa domains check'.`,
		Example:           `  adaa domains verify firma.no`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindDomain),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.Resolve(cmd.Context(), kindDomain, arg(args))
			if err != nil {
				return err
			}
			resp, err := ui.Spin(a.IO, "Checking public DNS…", func() (*api.Response, error) {
				return a.Do(cmd.Context(), http.MethodPost, "/domains/"+id+"/verify", nil, nil)
			})
			if err != nil {
				return err
			}
			d, err := obj.Parse(resp.Body)
			if err != nil {
				return err
			}
			return a.PrintObj(resp.Body, d, a.renderDomain)
		},
	}
}

func newDomainsServiceCmd(a *App) *cobra.Command {
	var enable, disable, dryRun bool
	cmd := &cobra.Command{
		Use:   "service <domain> <product>",
		Short: "Turn a product's DNS records on or off for a domain",
		Long: `Publish or withdraw all of a product's records at once. Enabling mail publishes
its MX, DKIM and SPF contribution together; disabling withdraws them all.

Do this rather than switching records off one by one: a half-published service
is worse than an absent one.`,
		Example: `  adaa domains service firma.no mail --enable
  adaa domains service firma.no mail --disable --dry-run`,
		Args: cobra.ExactArgs(2),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
			if len(args) == 1 {
				return productCodes, cobra.ShellCompDirectiveNoFileComp
			}
			return a.complete(kindDomain)(cmd, args, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if enable == disable {
				return usagef("pass exactly one of --enable or --disable")
			}
			if err := oneOf("product", args[1], productCodes...); err != nil {
				return err
			}
			id, err := a.Resolve(cmd.Context(), kindDomain, args[0])
			if err != nil {
				return err
			}
			verb := "Enable"
			if disable {
				verb = "Disable"
			}
			resp, set, err := a.Apply(cmd.Context(), Change{
				Method: http.MethodPut, Path: "/domains/" + id + "/services/" + args[1],
				Body:     map[string]any{"enabled": enable},
				Question: fmt.Sprintf("%s %s records on %s?", verb, args[1], args[0]),
				DryRun:   dryRun,
			})
			if err != nil || dryRun {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("%sd %s records on %s", verb, args[1], orNone(set.Str("domain_name"), args[0]))
			if s := set.Str("summary"); s != "" {
				a.IO.Infof("  %s", s)
			}
			for _, c := range set.List("conflicts") {
				a.IO.Warnf("%s", c.Str("summary"))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&enable, "enable", false, "Publish the product's records")
	cmd.Flags().BoolVar(&disable, "disable", false, "Withdraw the product's records")
	addDryRunFlag(cmd, &dryRun)
	return cmd
}

// ---- domains check -------------------------------------------------------

// domainLookups builds the resolvers to check against. Tests replace it.
var domainLookups = func(servers []string) map[string]dnscheck.Lookup {
	out := map[string]dnscheck.Lookup{}
	for _, s := range servers {
		out[s] = dnscheck.Resolver(s)
	}
	return out
}

func newDomainsCheckCmd(a *App) *cobra.Command {
	var resolvers []string
	var watch bool
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "check [domain]",
		Short: "Check from this computer that the domain's DNS records are live",
		Long: `Look up every record the domain should have, from this computer, against
public resolvers, and compare the answers with what adaa expects.

This is the check to run right after editing DNS at a registrar: it asks the
resolvers directly, so the answer is not an old value cached on this machine.
--watch repeats it until everything is live.

Exits 0 when every record is published, 1 when something is missing or wrong.`,
		Example: `  adaa domains check firma.no
  adaa domains check firma.no --watch
  adaa domains check firma.no --resolver 9.9.9.9 --resolver system`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindDomain),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindDomain, arg(args))
			if err != nil {
				return err
			}
			set, _, err := a.Get(ctx, "/domains/"+id+"/dns", nil)
			if err != nil {
				return err
			}
			domain := set.Str("domain_name")
			var records []dnscheck.Record
			for _, r := range set.List("records") {
				if r.Has("enabled") && !r.Bool("enabled") {
					continue
				}
				rec := dnscheck.Record{ID: r.Str("id"), Type: r.Str("type"), Name: r.Str("name"), Value: r.Str("value")}
				if p, ok := r.Int("priority"); ok {
					rec.Priority = &p
				}
				records = append(records, rec)
			}
			if len(records) == 0 {
				a.IO.Infof("%s has no records to check.", domain)
				return nil
			}
			lookups := domainLookups(resolvers)
			for {
				results, _ := ui.Spin(a.IO, fmt.Sprintf("Looking up %d records on %s…", len(records), strings.Join(resolvers, ", ")),
					func() ([]dnscheck.Result, error) { return dnscheck.Check(ctx, domain, records, lookups), nil })
				bad := a.renderDNSCheck(domain, results)
				if bad == 0 {
					a.IO.Successf("All %d records on %s are live.", len(results), domain)
					return nil
				}
				if !watch {
					a.IO.Hint("adaa domains check %s --watch   (repeats until they are)", domain)
					return errSilent
				}
				a.IO.Infof("%s", a.IO.E().Dim(fmt.Sprintf("%d not live yet; checking again in %s (Ctrl-C to stop)", bad, interval)))
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(interval):
				}
			}
		},
	}
	cmd.Flags().StringArrayVar(&resolvers, "resolver", []string{"1.1.1.1", "8.8.8.8"}, "DNS server to ask (repeatable); \"system\" uses this computer's resolver")
	cmd.Flags().BoolVar(&watch, "watch", false, "Keep checking until every record is live")
	cmd.Flags().DurationVar(&interval, "interval", 30*time.Second, "Time between checks with --watch")
	return cmd
}

// renderDNSCheck prints results and returns how many are not live.
func (a *App) renderDNSCheck(domain string, results []dnscheck.Result) int {
	bad := 0
	if a.JSON {
		type row struct {
			ID       string   `json:"id,omitempty"`
			Type     string   `json:"type"`
			Name     string   `json:"name"`
			Expected string   `json:"expected"`
			Seen     []string `json:"seen"`
			Status   string   `json:"status"`
			Detail   string   `json:"detail,omitempty"`
		}
		rows := []row{}
		for _, r := range results {
			if r.Status != dnscheck.OK && r.Status != dnscheck.Unchecked {
				bad++
			}
			seen := r.Seen
			if seen == nil {
				seen = []string{}
			}
			rows = append(rows, row{r.Record.ID, r.Record.Type, r.FQDN, r.Record.Value, seen, string(r.Status), r.Detail})
		}
		b, _ := json.Marshal(map[string]any{"domain": domain, "records": rows})
		_ = a.PrintJSON(b)
		return bad
	}
	s := a.IO.S()
	t := a.IO.NewTable("Status", "Type", "Name", "Expected", "Seen")
	for _, r := range results {
		var st string
		switch r.Status {
		case dnscheck.OK:
			st = s.Green("✓ ok")
		case dnscheck.Propagating:
			st = s.Yellow("… propagating")
			bad++
		case dnscheck.Unchecked:
			st = s.Dim("– cannot check")
		case dnscheck.Missing:
			st = s.Red("✗ missing")
			bad++
		default:
			st = s.Red("✗ " + string(r.Status))
			bad++
		}
		seen := strings.Join(r.Seen, ", ")
		if r.Detail != "" && r.Status != dnscheck.OK {
			seen = join(" ", seen, s.Dim(r.Detail))
		}
		t.Row(st, r.Record.Type, r.FQDN, r.Record.Value, seen)
	}
	t.Render()
	return bad
}

// ---- SPF -----------------------------------------------------------------

func newSPFCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "spf",
		Short: "The domain's single SPF record, composed from every sender",
		Long: `A domain may publish exactly one SPF record, and it may cost at most ten DNS
lookups. adaa composes it from every service that sends mail as the domain,
plus senders of your own.`,
	}
	cmd.AddCommand(newSPFViewCmd(a), newSPFEditCmd(a))
	return cmd
}

func newSPFViewCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "view [domain]",
		Short:             "Show the SPF policy and what it costs in lookups",
		Example:           `  adaa domains spf view firma.no`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindDomain),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.Resolve(cmd.Context(), kindDomain, arg(args))
			if err != nil {
				return err
			}
			p, raw, err := a.Get(cmd.Context(), "/domains/"+id+"/spf", nil)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, p, a.renderSPF)
		},
	}
}

func (a *App) renderSPF(p obj.Obj) {
	s := a.IO.S()
	v := a.IO.NewDetail("SPF", p.Str("domain_id"))
	v.Field("Status", a.status(p.Str("status")))
	v.Field("Summary", p.Str("summary"))
	v.Field("Record", join(" ", p.Str("record_name"), p.Str("record_value")))
	if obs := p.Str("observed_value"); obs != "" && obs != p.Str("record_value") {
		v.Field("Published", s.Yellow(obs))
	}
	v.Field("Lookups", fmt.Sprintf("%s of %s", p.Str("lookup_count"), p.Str("lookup_limit")))
	v.Field("Everything else", p.Str("all_qualifier"))
	v.Field("Last checked", a.IO.When(p.Str("last_checked_at")))
	v.Render()

	if ms := p.List("mechanisms"); len(ms) > 0 {
		fmt.Fprintln(a.IO.Out)
		t := a.IO.NewTable("Mechanism", "Qualifier", "From", "Lookups", "State", "ID")
		for _, m := range ms {
			var from []string
			for _, src := range m.List("sources") {
				from = append(from, src.Str("name"))
			}
			if len(from) == 0 {
				from = []string{m.Str("source_kind")}
			}
			state := s.Green("on")
			switch {
			case !m.Bool("enabled"):
				state = s.Dim("off")
			case m.Bool("flatten_stale"):
				state = s.Yellow("flattened, stale")
			case m.Bool("flattened"):
				state = "flattened"
			}
			t.Row(m.Str("type")+":"+m.Str("value"), m.Str("qualifier"), strings.Join(from, ", "), m.Str("lookups"), state, a.dim(m.Str("id")))
		}
		t.Render()
	}
	for _, pr := range p.List("problems") {
		a.IO.Warnf("%s", pr.Str("summary"))
		if act := pr.Str("suggested_action"); act != "" {
			fmt.Fprintln(a.IO.Err, "  "+act)
		}
	}
}

func newSPFEditCmd(a *App) *cobra.Command {
	var all string
	var add, remove, disable, enable, flatten []string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "edit [domain]",
		Short: "Add your own senders, switch service senders off, or change -all",
		Long: `Change the SPF policy. The one record is recomposed and re-counted; a result
over the lookup limit is refused rather than published.

--add and --remove take your own senders as type:value, e.g. include:_spf.crm.io
or ip4:203.0.113.4 (optionally with :qualifier). --disable, --enable and
--flatten take mechanism ids from 'adaa domains spf view'.`,
		Example: `  adaa domains spf edit firma.no --add include:_spf.mailchimp.com
  adaa domains spf edit firma.no --all fail --dry-run
  adaa domains spf edit firma.no --flatten spm_123`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindDomain),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := oneOf("all", all, "pass", "fail", "softfail", "neutral"); err != nil {
				return err
			}
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindDomain, arg(args))
			if err != nil {
				return err
			}
			cur, _, err := a.Get(ctx, "/domains/"+id+"/spf", nil)
			if err != nil {
				return err
			}
			body, err := spfUpdate(cur, all, add, remove, disable, enable, flatten)
			if err != nil {
				return err
			}
			if len(body) == 0 {
				return usagef("nothing to change; pass at least one flag (see --help)")
			}
			resp, p, err := a.Apply(ctx, Change{
				Method: http.MethodPatch, Path: "/domains/" + id + "/spf", Body: body,
				Question: "Publish the new SPF record?", DryRun: dryRun,
			})
			if err != nil || dryRun {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("SPF updated")
			a.renderSPF(p)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&all, "all", "", "How to treat all other senders: pass, fail, softfail or neutral")
	f.StringArrayVar(&add, "add", nil, "Add your own sender, type:value[:qualifier] (repeatable)")
	f.StringArrayVar(&remove, "remove", nil, "Remove one of your own senders, type:value or id (repeatable)")
	f.StringArrayVar(&disable, "disable", nil, "Leave out a service's sender, by mechanism id (repeatable)")
	f.StringArrayVar(&enable, "enable", nil, "Bring back a service's sender, by mechanism id (repeatable)")
	f.StringArrayVar(&flatten, "flatten", nil, "Replace an include with its addresses to save lookups, by id (repeatable)")
	addDryRunFlag(cmd, &dryRun)
	enumFlag(cmd, "all", "pass", "fail", "softfail", "neutral")
	return cmd
}

// spfUpdate turns flag edits into a SpfPolicyUpdate. The arrays in that body
// replace what is stored, so each one starts from the current policy.
func spfUpdate(cur obj.Obj, all string, add, remove, disable, enable, flatten []string) (map[string]any, error) {
	body := map[string]any{}
	if all != "" {
		body["all_qualifier"] = all
	}
	mechs := cur.List("mechanisms")

	if len(add)+len(remove) > 0 {
		manual := []map[string]any{}
		for _, m := range mechs {
			if m.Str("source_kind") != "manual" {
				continue
			}
			key := m.Str("type") + ":" + m.Str("value")
			drop := false
			for _, r := range remove {
				if r == m.Str("id") || strings.EqualFold(r, key) {
					drop = true
				}
			}
			if drop {
				continue
			}
			e := map[string]any{"type": m.Str("type"), "value": m.Str("value")}
			if q := m.Str("qualifier"); q != "" {
				e["qualifier"] = q
			}
			if n := m.Str("note"); n != "" {
				e["note"] = n
			}
			manual = append(manual, e)
		}
		for _, s := range add {
			parts := strings.SplitN(s, ":", 3)
			if len(parts) < 2 || parts[1] == "" {
				return nil, usagef("--add wants type:value, e.g. include:_spf.example.com, got %q", s)
			}
			if err := oneOf("add type", parts[0], "include", "ip4", "ip6", "a", "mx", "exists"); err != nil {
				return nil, err
			}
			e := map[string]any{"type": parts[0], "value": parts[1]}
			if len(parts) == 3 {
				if err := oneOf("add qualifier", parts[2], "pass", "fail", "softfail", "neutral"); err != nil {
					return nil, err
				}
				e["qualifier"] = parts[2]
			}
			manual = append(manual, e)
		}
		body["manual_mechanisms"] = manual
	}

	if len(disable)+len(enable) > 0 {
		off := map[string]bool{}
		for _, m := range mechs {
			if m.Str("source_kind") != "manual" && !m.Bool("enabled") {
				off[m.Str("id")] = true
			}
		}
		for _, id := range disable {
			off[id] = true
		}
		for _, id := range enable {
			delete(off, id)
		}
		ids := []string{}
		for id := range off {
			ids = append(ids, id)
		}
		body["disabled_mechanism_ids"] = ids
	}

	if len(flatten) > 0 {
		ids := []string{}
		seen := map[string]bool{}
		for _, m := range mechs {
			if m.Bool("flattened") && !seen[m.Str("id")] {
				seen[m.Str("id")] = true
				ids = append(ids, m.Str("id"))
			}
		}
		for _, id := range flatten {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		body["flatten_mechanism_ids"] = ids
	}
	return body, nil
}

// ---- Transfers -----------------------------------------------------------

func newTransferCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "transfer",
		Short: "Move a domain's registration to or from adaa",
		Long: `Move a domain's registration between registrars. Transfers take days, and
most of that is waiting for someone to click a link in an email, so 'view'
says plainly who has the next move.`,
	}
	cmd.AddCommand(newTransferViewCmd(a), newTransferStartCmd(a), newTransferCancelCmd(a))
	return cmd
}

func newTransferViewCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "view [domain]",
		Short:             "Show the transfer in progress, or the last one",
		Example:           `  adaa domains transfer view firma.no`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindDomain),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.Resolve(cmd.Context(), kindDomain, arg(args))
			if err != nil {
				return err
			}
			t, raw, err := a.Get(cmd.Context(), "/domains/"+id+"/transfer", nil)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, t, a.renderTransfer)
		},
	}
}

func (a *App) renderTransfer(t obj.Obj) {
	s := a.IO.S()
	title := "Transfer " + t.Str("direction")
	if n := t.Str("domain_name"); n != "" {
		title = n + ": transfer " + t.Str("direction")
	}
	v := a.IO.NewDetail(title, t.Str("id"))
	v.Field("Status", a.status(t.Str("status")))
	v.Field("Summary", t.Str("summary"))
	if w := t.Str("waiting_on"); w != "" {
		v.Field("Waiting on", s.Bold(strings.ReplaceAll(w, "_", " ")))
	}
	v.Field("From", t.Str("from_registrar"))
	v.Field("To", t.Str("to_registrar"))
	v.Field("Approval sent to", t.Str("approval_sent_to"))
	v.Field("Possible from", t.Str("eligible_on"))
	v.Field("Expected done", a.IO.When(t.Str("estimated_completion_at")))
	v.Field("Failure", join(": ", t.Str("failure_code"), t.Str("failure_reason")))
	v.Field("Cancelled", t.Str("cancelled_reason"))
	if dc := t.Obj("dns_continuity"); dc != nil {
		v.Section("DNS during the move")
		strategy := strings.ReplaceAll(dc.Str("strategy"), "_", " ")
		if dc.Str("strategy") == "at_risk" {
			strategy = s.Red(strategy)
		}
		v.Field("Strategy", strategy)
		v.Field("Summary", dc.Str("summary"))
		if n, _ := dc.Int("records_unknown"); n > 0 {
			v.Field("Unknown records", s.Yellow(fmt.Sprintf("%d records at the old registrar we cannot see", n)))
		}
	}
	if reqs := t.List("requirements"); len(reqs) > 0 {
		v.Section("Requirements")
		for _, r := range reqs {
			mark := s.Green("✓")
			if !r.Bool("satisfied") {
				mark = s.Yellow("!")
				if r.Bool("blocking") {
					mark = s.Red("✗")
				}
			}
			line := mark + " " + r.Str("summary")
			if !r.Bool("satisfied") && r.Str("how_to_resolve") != "" {
				line += "\n  " + s.Dim(r.Str("how_to_resolve"))
			}
			v.Line(line)
		}
	}
	v.Render()
}

func newTransferStartCmd(a *App) *cobra.Command {
	var direction, toRegistrar, authCode string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "start [domain]",
		Short: "Start moving a domain's registration",
		Long: `Start a transfer: --direction in brings a domain to adaa (needs the auth code
from the current registrar), --direction out moves it elsewhere.

Every requirement is checked first — registrar lock, auth code, the sixty-day
rule, whether the approval email can arrive — and so is DNS: if records at the
old registrar cannot be seen, they may vanish during the move, and adaa says so
before anything is submitted.`,
		Example: `  adaa domains transfer start firma.no --direction in --auth-code 'X9#…'
  adaa domains transfer start firma.no --direction out --to-registrar Domeneshop --dry-run`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindDomain),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := oneOf("direction", direction, "in", "out"); err != nil {
				return err
			}
			if direction == "out" && toRegistrar == "" {
				if err := a.need(&toRegistrar, "Registrar to move it to", "--to-registrar", nil); err != nil {
					return err
				}
			}
			id, err := a.Resolve(ctx, kindDomain, arg(args))
			if err != nil {
				return err
			}
			if direction == "in" && authCode == "" && a.IO.Interactive() && !dryRun {
				if err := a.IO.Secret("Auth code from the current registrar (leave empty to add later)", "--auth-code", &authCode); err != nil {
					return err
				}
			}
			body := map[string]any{"direction": direction}
			if toRegistrar != "" {
				body["to_registrar"] = toRegistrar
			}
			if authCode != "" {
				body["auth_code"] = authCode
			}
			path := "/domains/" + id + "/transfer"

			if dryRun || (!a.Yes && a.IO.Interactive()) {
				body["dry_run"] = true
				resp, err := ui.Spin(a.IO, "Checking requirements…", func() (*api.Response, error) {
					return a.Do(ctx, http.MethodPost, path, nil, body)
				})
				if err != nil {
					return a.transferError(err)
				}
				t, err := obj.Parse(resp.Body)
				if err != nil {
					return err
				}
				if dryRun {
					return a.PrintObj(resp.Body, t, a.renderTransfer)
				}
				a.renderTransfer(t)
				if t.Str("dns_continuity.strategy") == "at_risk" {
					a.IO.Warnf("Some DNS records may stop working during the move.")
				}
				if err := a.Confirm("Start the transfer?"); err != nil {
					return err
				}
				delete(body, "dry_run")
			} else if !a.Yes {
				return &ui.NoInputError{What: "confirmation", Flag: "--yes (or --dry-run to check requirements)"}
			}
			resp, t, err := a.Send(ctx, http.MethodPost, path, body)
			if err != nil {
				return a.transferError(err)
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Transfer started: %s", t.Str("summary"))
			a.IO.Hint("adaa domains transfer view %s", orNone(t.Str("domain_name"), id))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&direction, "direction", "in", "in (to adaa) or out (to another registrar)")
	f.StringVar(&toRegistrar, "to-registrar", "", "Where it is going (required for --direction out)")
	f.StringVar(&authCode, "auth-code", "", "Auth code from the current registrar, for --direction in")
	addDryRunFlag(cmd, &dryRun)
	enumFlag(cmd, "direction", "in", "out")
	return cmd
}

func (a *App) transferError(err error) error {
	var p *api.Problem
	if errors.As(err, &p) && p.Code() == "dns-continuity-at-risk" {
		a.IO.Warnf("Records at the current registrar cannot all be seen, so moving now could take parts of the domain offline.")
		a.IO.Hint("adaa domains dns list <domain>   (make sure every record in use is listed first)")
	}
	return err
}

func newTransferCancelCmd(a *App) *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "cancel [domain]",
		Short: "Stop a transfer that has not completed",
		Long: `Stop a transfer. Possible until the registry acts on it; after that the domain
has moved, and moving it back is a new transfer under the sixty-day rule.`,
		Example:           `  adaa domains transfer cancel firma.no --reason "Changed our minds"`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindDomain),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.Resolve(cmd.Context(), kindDomain, arg(args))
			if err != nil {
				return err
			}
			if err := a.need(&reason, "Why cancel?", "--reason", nil); err != nil {
				return err
			}
			if err := a.Confirm("Cancel the transfer?"); err != nil {
				return err
			}
			resp, t, err := a.Send(cmd.Context(), http.MethodPost, "/domains/"+id+"/transfer/cancel", map[string]any{"reason": reason})
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Transfer cancelled: %s", t.Str("summary"))
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "Why, for the record")
	return cmd
}

func newAuthCodeCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "auth-code [domain]",
		Short: "Get the code that lets another registrar take the domain",
		Long: `Print the transfer authorisation code for a domain adaa is registrar for.

Anyone with this code (and the lock off) can move the domain, so the request is
logged like any other secret. Getting the code does not start a transfer; the
new registrar does that. Only the code goes to stdout.`,
		Example: `  adaa domains auth-code firma.no
  adaa domains auth-code firma.no --yes | pbcopy`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindDomain),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.Resolve(cmd.Context(), kindDomain, arg(args))
			if err != nil {
				return err
			}
			if err := a.Confirm("Reveal the transfer code? Anyone holding it can move the domain away."); err != nil {
				return err
			}
			resp, c, err := a.Send(cmd.Context(), http.MethodPost, "/domains/"+id+"/auth-code", nil)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			fmt.Fprintln(a.IO.Out, c.Str("auth_code"))
			if c.Bool("transfer_lock") {
				a.IO.Warnf("The transfer lock is still on; the code will not work until it is off.")
				a.IO.Hint("adaa domains edit %s --transfer-lock=false", orNone(c.Str("domain_name"), id))
			}
			return nil
		},
	}
}
