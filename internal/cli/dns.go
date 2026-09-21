package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/getadaa/cli/internal/api"
	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

var dnsTypes = []string{"A", "AAAA", "CNAME", "MX", "TXT", "SRV", "CAA", "NS"}

func newDNSCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dns",
		Short: "A domain's DNS records",
		Long: `The DNS records a domain should have. Automatic records come from the services
using the domain and keep themselves current; manual ones are yours. An
automatic record can be switched off or overridden, and both are remembered.`,
	}
	cmd.AddCommand(newDNSListCmd(a), newDNSViewCmd(a), newDNSAddCmd(a), newDNSEditCmd(a),
		newDNSRemoveCmd(a), newDNSRestoreCmd(a), newDNSExportCmd(a))
	return cmd
}

// dnsRecordID accepts only record ids: records have no name unique enough to
// look them up by, and `dns list` shows every id.
func dnsRecordID(args []string) (string, error) {
	id := strings.TrimSpace(arg(args))
	if !strings.HasPrefix(id, "dns_") {
		return "", usagef("pass a DNS record id (dns_…); `adaa domains dns list <domain>` shows them")
	}
	return id, nil
}

func newDNSListCmd(a *App) *cobra.Command {
	var source, status string
	var includeDisabled bool
	cmd := &cobra.Command{
		Use:     "list [domain]",
		Aliases: []string{"ls"},
		Short:   "List a domain's DNS records and whether they are published",
		Example: `  adaa domains dns list firma.no
  adaa domains dns list firma.no --status missing
  adaa domains dns list firma.no --source manual`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindDomain),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := oneOf("source", source, "automatic", "manual"); err != nil {
				return err
			}
			if err := oneOf("status", status, "published", "missing", "mismatch", "disabled", "pending"); err != nil {
				return err
			}
			id, err := a.Resolve(cmd.Context(), kindDomain, arg(args))
			if err != nil {
				return err
			}
			set, raw, err := a.Get(cmd.Context(), "/domains/"+id+"/dns", nil)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(raw)
			}
			if s := set.Str("summary"); s != "" {
				a.IO.Infof("%s", s)
			}
			// The API returns every record; the filters narrow what is shown.
			l := filterListing(&Listing{Items: set.List("records")}, func(r obj.Obj) bool {
				disabled := r.Has("enabled") && !r.Bool("enabled")
				switch {
				case disabled && !includeDisabled && status != "disabled":
					return false
				case source != "" && r.Str("source") != source:
					return false
				case status == "disabled":
					return disabled || r.Str("status") == "disabled"
				case status != "" && r.Str("status") != status:
					return false
				}
				return true
			})
			if err := a.PrintListing(l, "No records match.", []string{"Status", "Type", "Name", "Value", "Source", "ID"},
				func(r obj.Obj) []string {
					src := r.Str("source")
					if p := r.Str("source_product"); p != "" {
						src = p
					}
					if r.Bool("overridden") {
						src += " (overridden)"
					}
					st := r.Str("status")
					if r.Has("enabled") && !r.Bool("enabled") {
						st = "disabled"
					}
					return []string{a.status(st), r.Str("type"), r.Str("name"), dnsValue(r), src, a.dim(r.Str("id"))}
				}); err != nil {
				return err
			}
			for _, c := range set.List("conflicts") {
				a.IO.Warnf("%s", c.Str("summary"))
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&source, "source", "", "Only automatic or manual records")
	f.StringVar(&status, "status", "", "Only records in this state: published, missing, mismatch, disabled, pending")
	f.BoolVar(&includeDisabled, "include-disabled", true, "Include records that were switched off")
	enumFlag(cmd, "source", "automatic", "manual")
	enumFlag(cmd, "status", "published", "missing", "mismatch", "disabled", "pending")
	return cmd
}

func dnsValue(r obj.Obj) string {
	v := r.Str("value")
	if p := r.Str("priority"); p != "" && (r.Str("type") == "MX" || r.Str("type") == "SRV") {
		v = p + " " + v
	}
	return v
}

func newDNSViewCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:     "view <record>",
		Short:   "Show one DNS record, and how to publish it",
		Example: `  adaa domains dns view dns_01JATX3M4K7Q2YV8N0RCBEZ5HS`,
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := dnsRecordID(args)
			if err != nil {
				return err
			}
			r, raw, err := a.Get(cmd.Context(), "/dns-records/"+id, nil)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, r, a.renderDNSRecord)
		},
	}
}

func (a *App) renderDNSRecord(r obj.Obj) {
	s := a.IO.S()
	v := a.IO.NewDetail(r.Str("type")+" "+r.Str("name"), r.Str("id"))
	v.Field("Value", r.Str("value"))
	v.Field("Priority", r.Str("priority"))
	v.Field("TTL", r.Str("ttl"))
	st := r.Str("status")
	if r.Has("enabled") && !r.Bool("enabled") {
		st = "disabled"
	}
	v.Field("Status", a.status(st))
	if obs := r.Str("observed_value"); obs != "" && obs != r.Str("value") {
		v.Field("Published now", s.Yellow(obs))
	}
	src := r.Str("source")
	if p := r.Str("source_product"); p != "" {
		src += " (" + p + ")"
	}
	v.Field("Source", src)
	if r.Bool("overridden") {
		v.Field("Overridden", "yes; the service would publish "+r.Str("generated_value"))
	}
	v.Field("Conflicts", r.Str("conflict_policy"))
	v.Field("Needed by", strings.Join(r.Strings("required_by"), ", "))
	v.Field("Last checked", a.IO.When(r.Str("last_checked_at")))
	if in := r.Str("instruction"); in != "" {
		v.Section("How to publish it")
		v.Line(in)
	}
	v.Render()
}

func newDNSAddCmd(a *App) *cobra.Command {
	var typ, name, value string
	var ttl, priority int
	cmd := &cobra.Command{
		Use:   "add [domain]",
		Short: "Add a DNS record of your own",
		Long: `Add a manual record. It is refused if it would collide with a record a service
already owns, and the error names that service.`,
		Example: `  adaa domains dns add firma.no --type A --name www --value 203.0.113.10
  adaa domains dns add firma.no --type TXT --name @ --value "google-site-verification=…"
  adaa domains dns add firma.no --type MX --name @ --value mx.example.net --priority 10`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindDomain),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			typ = strings.ToUpper(typ)
			if err := oneOf("type", typ, dnsTypes...); err != nil {
				return err
			}
			id, err := a.Resolve(ctx, kindDomain, arg(args))
			if err != nil {
				return err
			}
			if typ == "" {
				opts := make([]ui.Option, len(dnsTypes))
				for n, t := range dnsTypes {
					opts[n] = ui.Option{Label: t, Value: t}
				}
				if typ, err = a.IO.Select("Record type", "--type", opts); err != nil {
					return err
				}
			}
			if name == "" && a.IO.Interactive() {
				name = "@"
			}
			if err := a.need(&name, "Name (@ for the domain itself)", "--name", nil); err != nil {
				return err
			}
			if err := a.need(&value, "Value", "--value", nil); err != nil {
				return err
			}
			body := fields{"type": typ, "name": name, "value": value}
			body.integer(cmd, "ttl", "ttl", ttl)
			body.integer(cmd, "priority", "priority", priority)
			path := "/domains/" + id + "/dns"
			resp, r, err := a.Send(ctx, http.MethodPost, path, body)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Added %s %s → %s %s", r.Str("type"), r.Str("name"), r.Str("value"), a.IO.E().Dim(r.Str("id")))
			if in := r.Str("instruction"); in != "" && r.Str("status") != "published" {
				a.IO.Infof("  %s", in)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&typ, "type", "", "Record type: "+strings.Join(dnsTypes, ", "))
	f.StringVar(&name, "name", "", "Name relative to the domain; @ is the domain itself")
	f.StringVar(&value, "value", "", "Record value")
	f.IntVar(&ttl, "ttl", 0, "Time to live, in seconds")
	f.IntVar(&priority, "priority", 0, "Priority, for MX and SRV")
	enumFlag(cmd, "type", dnsTypes...)
	return cmd
}

func newDNSEditCmd(a *App) *cobra.Command {
	var value string
	var ttl, priority int
	var enable, disable bool
	cmd := &cobra.Command{
		Use:   "edit <record>",
		Short: "Change, override, or switch off a DNS record",
		Long: `Change a record. On an automatic record a new --value becomes an override, and
--disable switches it off; both survive the service regenerating its records.
Switching off a record a service needs is allowed, and raises a finding.`,
		Example: `  adaa domains dns edit dns_01J… --value 203.0.113.11
  adaa domains dns edit dns_01J… --disable`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := dnsRecordID(args)
			if err != nil {
				return err
			}
			if enable && disable {
				return usagef("pass --enable or --disable, not both")
			}
			body := fields{}
			body.str(cmd, "value", "value", value)
			body.integer(cmd, "ttl", "ttl", ttl)
			body.integer(cmd, "priority", "priority", priority)
			if enable {
				body["enabled"] = true
			}
			if disable {
				body["enabled"] = false
			}
			if len(body) == 0 {
				return usagef("nothing to change; pass at least one flag (see --help)")
			}
			if disable {
				cur, _, err := a.Get(cmd.Context(), "/dns-records/"+id, nil)
				if err != nil {
					return err
				}
				if need := cur.Strings("required_by"); len(need) > 0 {
					if err := a.Confirm(fmt.Sprintf("%s needs this record. Switch it off anyway?", strings.Join(need, ", "))); err != nil {
						return err
					}
				}
			}
			resp, r, err := a.Send(cmd.Context(), http.MethodPatch, "/dns-records/"+id, body)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Updated %s %s", r.Str("type"), r.Str("name"))
			if r.Bool("overridden") {
				a.IO.Hint("adaa domains dns restore %s   (to go back to the service's value)", id)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&value, "value", "", "New value (an override, on an automatic record)")
	f.IntVar(&ttl, "ttl", 0, "Time to live, in seconds")
	f.IntVar(&priority, "priority", 0, "Priority, for MX and SRV")
	f.BoolVar(&enable, "enable", false, "Switch the record back on")
	f.BoolVar(&disable, "disable", false, "Switch the record off")
	return cmd
}

func newDNSRemoveCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:     "remove <record>",
		Aliases: []string{"rm"},
		Short:   "Delete a DNS record of your own",
		Long: `Delete a manual record. Automatic records cannot be deleted — the service would
put them back — so switch them off with 'adaa domains dns edit --disable', or
turn the service off with 'adaa domains service'.`,
		Example: `  adaa domains dns remove dns_01JATX3M4K7Q2YV8N0RCBEZ5HS`,
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := dnsRecordID(args)
			if err != nil {
				return err
			}
			if err := a.Confirm("Delete this DNS record?"); err != nil {
				return err
			}
			if _, err := a.Do(cmd.Context(), http.MethodDelete, "/dns-records/"+id, nil, nil); err != nil {
				var p *api.Problem
				if errors.As(err, &p) && (p.Code() == "service-owned-record" || p.Status == http.StatusConflict) {
					a.printError(err)
					a.IO.Hint("adaa domains dns edit %s --disable", id)
					return errSilentWith(err)
				}
				return err
			}
			a.IO.Successf("Deleted %s", id)
			return nil
		},
	}
}

func newDNSRestoreCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:     "restore <record>",
		Short:   "Drop an override and put the service's own value back",
		Example: `  adaa domains dns restore dns_01JATX3M4K7Q2YV8N0RCBEZ5HS`,
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := dnsRecordID(args)
			if err != nil {
				return err
			}
			resp, r, err := a.Send(cmd.Context(), http.MethodPost, "/dns-records/"+id+"/restore", nil)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Restored %s %s → %s", r.Str("type"), r.Str("name"), r.Str("value"))
			return nil
		},
	}
}

func newDNSExportCmd(a *App) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "export [domain]",
		Short: "Print the records to paste into whoever hosts your DNS",
		Long: `Print the record set for a domain whose DNS is kept elsewhere: take this, put
it in at your registrar, then run 'adaa domains check'.

--format table is a plain list for typing into a registrar's web form, zone is
BIND syntax, json is the full record set.`,
		Example: `  adaa domains dns export firma.no
  adaa domains dns export firma.no --format zone > firma.no.zone`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindDomain),
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.JSON {
				format = "json"
			}
			if err := oneOf("format", format, "zone", "table", "json"); err != nil {
				return err
			}
			id, err := a.Resolve(cmd.Context(), kindDomain, arg(args))
			if err != nil {
				return err
			}
			c, err := a.Client()
			if err != nil {
				return err
			}
			resp, err := c.Do(cmd.Context(), api.Request{Method: http.MethodGet, Path: "/domains/" + id + "/dns/export",
				Query: url.Values{"format": {format}}, Accept: "text/plain, application/json, application/problem+json"})
			if err != nil {
				return err
			}
			if format == "json" || json.Valid(resp.Body) && strings.HasPrefix(strings.TrimSpace(string(resp.Body)), "{") {
				return a.PrintJSON(resp.Body)
			}
			body := string(resp.Body)
			if !strings.HasSuffix(body, "\n") {
				body += "\n"
			}
			_, err = fmt.Fprint(a.IO.Out, body)
			return err
		},
	}
	cmd.Flags().StringVar(&format, "format", "table", "table, zone or json")
	enumFlag(cmd, "format", "table", "zone", "json")
	return cmd
}
