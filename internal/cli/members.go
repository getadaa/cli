package cli

import (
	"context"
	"net/http"
	"strings"

	"github.com/getadaa/cli/internal/obj"
	"github.com/spf13/cobra"
)

func init() { register(newMembersCmd) }

// Who may act on the company, which is a different list from who works there.
//
// `adaa people` is the estate: an employee with a mailbox and a laptop. Most of a
// company is on that list and not this one -- a warehouse employee has no reason
// to open the portal. Somebody can be on this list and not that one, which is
// what an external bookkeeper or IT contact is.
func newMembersCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "members",
		Aliases: []string{"member"},
		Short:   "Manage who may sign in to your organization, and as what",
		Long: `Who may act on your organization, and as what.

This is not the same list as 'adaa people'. Adding somebody to the estate does
not let them in; letting them in is granting a membership. A role is held in an
organization rather than by a person, so somebody working for two customers holds
one at each and moves between them with 'adaa switch'.`,
		GroupID: groupSetup,
	}
	cmd.AddCommand(
		newMembersListCmd(a),
		newMembersGrantCmd(a),
		newMembersRoleCmd(a),
		newMembersRevokeCmd(a),
	)
	return cmd
}

func newMembersListCmd(a *App) *cobra.Command {
	var lo ListOpts
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List everybody who may sign in",
		Example: "  adaa members list",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			path, err := a.OrgPath(ctx, "/members")
			if err != nil {
				return err
			}
			l, err := a.List(ctx, path, nil, lo)
			if err != nil {
				return err
			}
			return a.PrintListing(l, "Nobody can sign in yet. Grant access with `adaa members grant`.",
				[]string{"ID", "Name", "Email", "Role", "Employee", "State"},
				func(m obj.Obj) []string {
					state := a.status("active")
					if m.Str("membership.revoked_at") != "" {
						state = a.status("revoked")
					}
					// Blank rather than "no": an external member is a legitimate
					// state, not a missing employee record.
					employee := a.dim("external")
					if m.Str("membership.person_id") != "" {
						employee = "yes"
					}
					return []string{
						a.dim(m.Str("membership.id")),
						m.Str("identity.full_name"),
						m.Str("identity.email"),
						peopleRoleLabel(m.Str("membership.role")),
						employee,
						state,
					}
				})
		},
	}
	addListFlags(cmd, &lo)
	return cmd
}

func newMembersGrantCmd(a *App) *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use:   "grant [email]",
		Short: "Let somebody sign in, as a role",
		Long: `Let somebody sign in to your organization.

The address has to belong to somebody who already works here, so add them with
'adaa people add' first. Granting access to somebody who does not work here is
not something this API issues yet.`,
		Example: "  adaa members grant kari@firma.no --role org_admin",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := oneOf("role", role, "org_admin", "org_member"); err != nil {
				return err
			}

			email := arg(args)
			if email == "" {
				// Picked from the people who work here, because that is who can
				// be granted one at all.
				id, err := a.Resolve(ctx, kindPerson, "")
				if err != nil {
					return err
				}
				p, _, err := a.Get(ctx, "/people/"+id, nil)
				if err != nil {
					return err
				}
				email = p.Str("email")
			}
			if err := a.need(&role, "Which role?", "--role", nil); err != nil {
				return err
			}

			path, err := a.OrgPath(ctx, "/members")
			if err != nil {
				return err
			}
			resp, m, err := a.Send(ctx, http.MethodPost, path,
				map[string]any{"email": email, "role": role})
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("%s can now sign in as %s.",
				m.Str("identity.full_name"), peopleRoleLabel(m.Str("membership.role")))
			a.IO.Hint("They get a sign-in link with `adaa login` on their own machine.")
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "org_admin or org_member")
	enumFlag(cmd, "role", "org_admin", "org_member")
	return cmd
}

func newMembersRoleCmd(a *App) *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use:   "role [member]",
		Short: "Change what somebody may do here",
		Long: `Change a member's role.

Nobody may grant a role above their own, and the last administrator who can still
sign in cannot be demoted: give somebody else the role first.`,
		Example:           "  adaa members role ola@firma.no --role org_admin",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindMembership),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := oneOf("role", role, "org_admin", "org_member"); err != nil {
				return err
			}
			id, err := a.Resolve(ctx, kindMembership, arg(args))
			if err != nil {
				return err
			}
			if err := a.need(&role, "Which role?", "--role", nil); err != nil {
				return err
			}
			resp, m, err := a.Send(ctx, http.MethodPatch, "/members/"+id, map[string]any{"role": role})
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("%s is now %s.",
				m.Str("identity.full_name"), peopleRoleLabel(m.Str("membership.role")))
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "org_admin or org_member")
	enumFlag(cmd, "role", "org_admin", "org_member")
	return cmd
}

func newMembersRevokeCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke [member]",
		Short: "Take somebody's access away",
		Long: `Take somebody's access away, effective immediately: every session and token of
theirs stops working now rather than when it next expires. Their employee record
and everything they hold are untouched -- this is about signing in, not about
what they have.`,
		Example:           "  adaa members revoke ola@firma.no",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindMembership),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindMembership, arg(args))
			if err != nil {
				return err
			}
			if err := a.Confirm("Revoke " + id + "? Their sessions and tokens stop working now."); err != nil {
				return err
			}
			if _, err := a.Do(ctx, http.MethodDelete, "/members/"+id, nil, nil); err != nil {
				return err
			}
			a.IO.Successf("Revoked %s.", id)
			return nil
		},
	}
}

// membershipOfPerson finds the membership an employee record is behind, so
// `adaa people` can speak about a role without anybody having to know that the
// role lives somewhere else.
//
// Empty and no error means they have none, which is the ordinary state for most
// of a company.
func (a *App) membershipOfPerson(ctx context.Context, personID string) (obj.Obj, error) {
	path, err := a.OrgPath(ctx, "/members")
	if err != nil {
		return nil, err
	}
	items, err := a.ListItems(ctx, path, nil)
	if err != nil {
		return nil, err
	}
	for _, m := range items.Items {
		if m.Str("membership.person_id") == personID && m.Str("membership.revoked_at") == "" {
			return m, nil
		}
	}
	return nil, nil
}

// setPersonRole is what `--role` on a person means now: grant a membership, or
// change the one they have.
func (a *App) setPersonRole(ctx context.Context, person obj.Obj, role string) error {
	existing, err := a.membershipOfPerson(ctx, person.Str("id"))
	if err != nil {
		return err
	}

	if strings.TrimSpace(role) == "" || role == "none" {
		if existing == nil {
			return nil
		}
		_, err := a.Do(ctx, http.MethodDelete, "/members/"+existing.Str("membership.id"), nil, nil)
		return err
	}

	if existing != nil {
		if existing.Str("membership.role") == role {
			return nil
		}
		_, _, err := a.Send(ctx, http.MethodPatch, "/members/"+existing.Str("membership.id"),
			map[string]any{"role": role})
		return err
	}

	path, err := a.OrgPath(ctx, "/members")
	if err != nil {
		return err
	}
	_, _, err = a.Send(ctx, http.MethodPost, path,
		map[string]any{"email": person.Str("email"), "role": role, "person_id": person.Str("id")})
	return err
}
