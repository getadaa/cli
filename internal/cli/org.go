package cli

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/getadaa/cli/internal/obj"
	"github.com/spf13/cobra"
)

func init() { register(newOrgCmd) }

func newOrgCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "org",
		Aliases: []string{"organization"},
		Short:   "View and edit your organization",
		GroupID: groupSetup,
	}
	cmd.AddCommand(newOrgViewCmd(a), newOrgEditCmd(a), newOrgAgreementCmd(a), newOrgServicesCmd(a),
		newOrgGrantCmd(a), newOrgRevokeCmd(a))
	return cmd
}

func newOrgViewCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:     "view",
		Short:   "Show your organization",
		Example: "  adaa org view\n  adaa org view --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			path, err := a.OrgPath(ctx, "")
			if err != nil {
				return err
			}
			o, raw, err := a.Get(ctx, path, nil)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, o, func(o obj.Obj) {
				d := a.IO.NewDetail(o.Str("name"), o.Str("id"))
				d.Field("Status", a.status(o.Str("status")))
				d.Field("Org number", o.Str("org_number"))
				d.Field("Contact", o.Str("primary_contact_email"))
				d.Field("Customer since", a.IO.When(o.Str("created_at")))
				if n := o.Str("notes"); n != "" {
					d.Section("Notes")
					d.Line(n)
				}
				d.Render()
			})
		},
	}
}

var orgNumberRe = regexp.MustCompile(`^[0-9]{9}$`)

func newOrgEditCmd(a *App) *cobra.Command {
	var name, contact, orgNumber, notes string
	cmd := &cobra.Command{
		Use:     "edit",
		Short:   "Change your organization's details",
		Long:    "Change your organization's details. Only the flags you pass are changed; pass an empty value to clear a field.",
		Example: "  adaa org edit --contact-email it@firma.no\n  adaa org edit --org-number 987654321",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if orgNumber != "" && !orgNumberRe.MatchString(orgNumber) {
				return usagef("--org-number is the nine-digit organisasjonsnummer")
			}
			if contact != "" {
				if err := validEmail(contact); err != nil {
					return usagef("--contact-email: %v", err)
				}
			}
			body := fields{}
			body.str(cmd, "name", "name", name)
			body.str(cmd, "contact-email", "primary_contact_email", contact)
			body.str(cmd, "org-number", "org_number", orgNumber)
			body.str(cmd, "notes", "notes", notes)
			if len(body) == 0 {
				return usagef("nothing to change; pass at least one of --name, --contact-email, --org-number, --notes")
			}
			if v, ok := body["name"]; ok && v == nil {
				return errors.New("the organization needs a name")
			}
			path, err := a.OrgPath(ctx, "")
			if err != nil {
				return err
			}
			resp, o, err := a.Send(ctx, http.MethodPatch, path, map[string]any(body))
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Updated %s.", o.Str("name"))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "Company name")
	f.StringVar(&contact, "contact-email", "", "Where adaa should reach you")
	f.StringVar(&orgNumber, "org-number", "", "Norwegian organisasjonsnummer, nine digits")
	f.StringVar(&notes, "notes", "", "Free-text notes")
	return cmd
}

func newOrgAgreementCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:     "agreement",
		Short:   "Show the agreement with adaa: term, notice and invoicing",
		Example: "  adaa org agreement",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			path, err := a.OrgPath(ctx, "/agreement")
			if err != nil {
				return err
			}
			o, raw, err := a.Get(ctx, path, nil)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, o, func(o obj.Obj) {
				d := a.IO.NewDetail("Agreement", o.Str("terms_version"))
				d.Field("Started", o.Str("starts_on"))
				term := fmt.Sprintf("%s months, %s", o.Str("term_months"), o.Str("renewal"))
				if o.Bool("in_initial_term") {
					term += a.dim(", initial term until " + o.Str("initial_term_ends_on"))
				}
				d.Field("Term", term)
				d.Field("Renews", o.Str("renews_on"))
				d.Field("Ends", o.Str("ends_on"))
				notice := fmt.Sprintf("%s months from the %s", o.Str("notice_months"), strings.ReplaceAll(o.Str("notice_from"), "_", " "))
				d.Field("Notice", notice)
				d.Field("Earliest end", o.Str("earliest_end_on"))
				d.Field("Give notice by", o.Str("latest_notice_for_term_end"))
				d.Section("Invoicing")
				d.Field("Interval", o.Str("invoice_interval"))
				d.Field("Charged", "in "+o.Str("invoice_timing"))
				d.Field("Payment terms", o.Str("payment_terms_days")+" days")
				d.Section("Signature")
				d.Field("Signed by", o.Str("signed_by_name"))
				d.Field("Signed", join(" ", a.IO.When(o.Str("signed_at")), a.dim(o.Str("signature_method"))))
				d.Field("Terms", o.Str("terms_url"))
				d.Render()
			})
		},
	}
}

func newOrgServicesCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:     "services",
		Short:   "List services the company as a whole should have",
		Long:    "Services held by the company rather than by any one person: an office network, a shared printer plan, a domain.",
		Example: "  adaa org services",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			path, err := a.OrgPath(ctx, "/entitlements")
			if err != nil {
				return err
			}
			l, err := a.ListItems(ctx, path, nil)
			if err != nil {
				return err
			}
			return a.PrintListing(l, "No company-wide services. Add one with `adaa org grant <service>`.",
				[]string{"Service", "Quantity", "In place", "Note", "ID"},
				func(e obj.Obj) []string {
					inPlace := a.IO.S().Green("yes")
					if !e.Bool("fulfilled") {
						inPlace = a.IO.S().Yellow("not yet")
					}
					return []string{orNone(e.Str("service_name"), e.Str("service_code")), e.Str("quantity"), inPlace,
						e.Str("note"), a.dim(e.Str("id"))}
				})
		},
	}
}

func newOrgGrantCmd(a *App) *cobra.Command {
	var quantity int
	var note string
	var dryRun bool
	cmd := &cobra.Command{
		Use:               "grant [service]",
		Short:             "Say the company should have a service",
		Long:              "Add a company-wide service. The work to put it in place is planned from this, with a cost preview first.",
		Example:           "  adaa org grant office-network\n  adaa org grant printer-plan --quantity 2 --dry-run",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.peopleCompleteServices,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			code := arg(args)
			if code == "" {
				var err error
				if code, err = a.peoplePickService(ctx); err != nil {
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
			path, err := a.OrgPath(ctx, "/entitlements")
			if err != nil {
				return err
			}
			resp, e, err := a.Apply(ctx, Change{Method: http.MethodPost, Path: path, Body: body,
				Question: "Add " + code + " for the company?", DryRun: dryRun})
			if err != nil || dryRun {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Added %s %s", orNone(e.Str("service_name"), code), a.IO.E().Dim(e.Str("id")))
			return nil
		},
	}
	cmd.Flags().IntVar(&quantity, "quantity", 0, "How many (default 1)")
	cmd.Flags().StringVar(&note, "note", "", "Why, for the record")
	addDryRunFlag(cmd, &dryRun)
	return cmd
}

func newOrgRevokeCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:     "revoke <service>",
		Short:   "Take a company-wide service away",
		Long:    "Take a company-wide service away, by entitlement id or service code. The resulting removal waits for approval.",
		Example: "  adaa org revoke office-network\n  adaa org revoke ent_01J…",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			path, err := a.OrgPath(ctx, "/entitlements")
			if err != nil {
				return err
			}
			l, err := a.ListItems(ctx, path, nil)
			if err != nil {
				return err
			}
			var ent obj.Obj
			for _, e := range l.Items {
				if e.Str("id") == args[0] || strings.EqualFold(e.Str("service_code"), args[0]) {
					ent = e
					break
				}
			}
			if ent == nil {
				return fmt.Errorf("the company has no service matching %q (see `adaa org services`)", args[0])
			}
			label := orNone(ent.Str("service_name"), ent.Str("service_code"))
			if err := a.Confirm("Revoke " + label + " for the company?"); err != nil {
				return err
			}
			if _, err := a.Do(ctx, http.MethodDelete, path+"/"+ent.Str("id"), nil, nil); err != nil {
				return err
			}
			a.IO.Successf("Revoked %s. Removing it waits for approval.", label)
			a.IO.Hint("adaa tasks list --awaiting-approval")
			return nil
		},
	}
}
