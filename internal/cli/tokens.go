package cli

import (
	"fmt"
	"net/http"

	"github.com/getadaa/cli/internal/obj"
	"github.com/spf13/cobra"
)

func init() { register(newTokensCmd) }

func newTokensCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "tokens",
		Aliases: []string{"token"},
		Short:   "Manage API tokens for integrations and automation",
		Long: `API tokens let an integration or a script act as a person, with exactly that
person's role and never more. Set one as ADAA_TOKEN, or store it with
'adaa login --with-token'.`,
		GroupID: groupSetup,
	}
	cmd.AddCommand(newTokensListCmd(a), newTokensCreateCmd(a), newTokensRevokeCmd(a))
	return cmd
}

func newTokensListCmd(a *App) *cobra.Command {
	var lo ListOpts
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List tokens",
		Example: "  adaa tokens list",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			l, err := a.List(cmd.Context(), "/tokens", nil, lo)
			if err != nil {
				return err
			}
			return a.PrintListing(l, "No tokens. Create one with `adaa tokens create`.",
				[]string{"ID", "Name", "Prefix", "Last used", "Expires", "State"},
				func(t obj.Obj) []string {
					state := a.status("active")
					if t.Str("revoked_at") != "" {
						state = a.status("revoked")
					}
					last := a.IO.When(t.Str("last_used_at"))
					if last == "" {
						last = a.dim("never")
					}
					return []string{a.dim(t.Str("id")), t.Str("name"), t.Str("prefix"), last, a.IO.When(t.Str("expires_at")), state}
				})
		},
	}
	addListFlags(cmd, &lo)
	return cmd
}

func newTokensCreateCmd(a *App) *cobra.Command {
	var name, forPerson string
	var days int
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Issue a token",
		Long: `Issue a token. The secret is printed once, to stdout, and cannot be shown again:
only a hash is stored, so a lost token is replaced rather than recovered.`,
		Example: `  adaa tokens create --name "HR system" --expires-in-days 365
  adaa tokens create --name ci --for kari@firma.no > token.txt`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := a.need(&name, "What is the token for?", "--name", nil); err != nil {
				return err
			}
			if days < 0 {
				return usagef("--expires-in-days must be positive")
			}
			pid, err := a.Resolve(ctx, kindPerson, forPerson)
			if err != nil {
				return err
			}
			body := map[string]any{"person_id": pid, "name": name}
			if days > 0 {
				body["expires_in_days"] = days
			}
			resp, t, err := a.Send(ctx, http.MethodPost, "/tokens", body)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			fmt.Fprintln(a.IO.Out, t.Str("secret"))
			a.IO.Successf("Created token %q %s", t.Str("name"), a.IO.E().Dim(t.Str("id")))
			a.IO.Warnf("Copy the secret now; it will not be shown again.")
			a.IO.Hint("export ADAA_TOKEN=…   or   adaa login --with-token < token.txt")
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "What the token is for, e.g. \"HR system\"")
	cmd.Flags().StringVar(&forPerson, "for", "me", "Whose role the token carries (email, name or id)")
	cmd.Flags().IntVar(&days, "expires-in-days", 0, "Expire after this many days (default: never)")
	_ = cmd.RegisterFlagCompletionFunc("for", a.complete(kindPerson))
	return cmd
}

func newTokensRevokeCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "revoke [token]",
		Short:             "Revoke a token so it stops working",
		Example:           "  adaa tokens revoke \"HR system\"\n  adaa tokens revoke tok_01J…",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindToken),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindToken, arg(args))
			if err != nil {
				return err
			}
			if err := a.Confirm("Revoke " + id + "? Anything using it stops working."); err != nil {
				return err
			}
			if _, err := a.Do(ctx, http.MethodDelete, "/tokens/"+id, nil, nil); err != nil {
				return err
			}
			a.IO.Successf("Revoked %s.", id)
			return nil
		},
	}
}
