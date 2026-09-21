package cli

import (
	"errors"
	"net/http"
	"regexp"

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
	cmd.AddCommand(newOrgViewCmd(a), newOrgEditCmd(a))
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
