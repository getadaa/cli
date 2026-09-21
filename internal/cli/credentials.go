package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/getadaa/cli/internal/api"
	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() { register(newCredentialsCmd) }

var (
	credentialKinds   = []string{"password", "ssh_key", "api_key", "certificate", "wifi_psk", "license_key", "connection_string", "other"}
	credentialTargets = []string{"organization", "device", "server", "mail"}
)

func newCredentialsCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "credentials",
		Aliases: []string{"credential", "creds", "secrets"},
		Short:   "Stored passwords, keys and certificates",
		Long: `Stored secrets: router passwords, Wi-Fi keys, API keys, certificates.

Everything except 'reveal' works with metadata only. Revealing a secret is
logged with who, when and why, and only a person may do it; tokens used by
agents are refused.`,
		GroupID: groupCore,
	}
	cmd.AddCommand(newCredentialsListCmd(a), newCredentialsViewCmd(a), newCredentialsAddCmd(a),
		newCredentialsEditCmd(a), newCredentialsRotateCmd(a), newCredentialsDeleteCmd(a), newCredentialsRevealCmd(a))
	return cmd
}

func newCredentialsListCmd(a *App) *cobra.Command {
	var lo ListOpts
	var kind, device, server, targetType string
	var due bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List credentials, without their secrets",
		Example: `  adaa credentials list
  adaa credentials list --device office-router
  adaa credentials list --rotation-due`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := oneOf("kind", kind, credentialKinds...); err != nil {
				return err
			}
			if err := oneOf("target-type", targetType, credentialTargets...); err != nil {
				return err
			}
			if device != "" && server != "" {
				return usagef("--device and --server cannot be combined")
			}
			q := url.Values{}
			setQuery(q, cmd, "kind", "kind", kind)
			setQuery(q, cmd, "target-type", "target_type", targetType)
			if device != "" {
				id, err := a.Resolve(ctx, kindDevice, device)
				if err != nil {
					return err
				}
				q.Set("target_type", "device")
				q.Set("target_id", id)
			}
			if server != "" {
				id, err := a.Resolve(ctx, kindServer, server)
				if err != nil {
					return err
				}
				q.Set("target_type", "server")
				q.Set("target_id", id)
			}
			path, err := a.OrgPath(ctx, "/credentials")
			if err != nil {
				return err
			}
			l, err := a.List(ctx, path, q, lo)
			if err != nil {
				return err
			}
			now := time.Now()
			if due {
				// The API has no rotation filter, so this narrows what was fetched.
				l = filterListing(l, func(c obj.Obj) bool {
					t, err := time.Parse(time.RFC3339, c.Str("rotation_due_at"))
					return err == nil && !t.After(now)
				})
			}
			return a.PrintListing(l, "No credentials match.", []string{"Name", "Kind", "Username", "For", "Rotate by", "ID"}, func(c obj.Obj) []string {
				return []string{c.Str("name"), c.Str("kind"), c.Str("username"), credTarget(c),
					credDue(a.IO.S(), c.Str("rotation_due_at"), now), a.dim(c.Str("id"))}
			})
		},
	}
	addListFlags(cmd, &lo)
	f := cmd.Flags()
	f.StringVar(&kind, "kind", "", "Only this kind: "+strings.Join(credentialKinds, ", "))
	f.StringVar(&device, "device", "", "Only credentials for this device")
	f.StringVar(&server, "server", "", "Only credentials for this server")
	f.StringVar(&targetType, "target-type", "", "Only credentials for this kind of thing: "+strings.Join(credentialTargets, ", "))
	enumFlag(cmd, "target-type", credentialTargets...)
	f.BoolVar(&due, "rotation-due", false, "Only credentials past their rotation interval")
	enumFlag(cmd, "kind", credentialKinds...)
	_ = cmd.RegisterFlagCompletionFunc("device", a.complete(kindDevice))
	_ = cmd.RegisterFlagCompletionFunc("server", a.complete(kindServer))
	return cmd
}

func credTarget(c obj.Obj) string {
	if n := c.Str("target_name"); n != "" {
		return n
	}
	return c.Str("target_type")
}

func credDue(s ui.Palette, due string, now time.Time) string {
	if due == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, due)
	if err != nil {
		return due
	}
	date := t.Local().Format("2 Jan 2006")
	if t.Before(now) {
		return s.Red("overdue, " + date)
	}
	return date
}

func newCredentialsViewCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "view [credential]",
		Short:             "Show a credential's details, not its secret",
		Example:           "  adaa credentials view \"Office router\"",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindCredential),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindCredential, arg(args))
			if err != nil {
				return err
			}
			c, raw, err := a.Get(ctx, "/credentials/"+id, nil)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, c, func(c obj.Obj) {
				s := a.IO.S()
				d := a.IO.NewDetail(c.Str("name"), c.Str("id"))
				d.Field("Kind", c.Str("kind"))
				d.Field("For", join(" ", credTarget(c), s.Dim(c.Str("target_id"))))
				d.Field("Username", c.Str("username"))
				d.Field("URL", c.Str("url"))
				if h := c.Str("secret_hint"); h != "" {
					d.Field("Secret", s.Dim("…"+h))
				}
				d.Field("Description", c.Str("description"))
				d.Section("Rotation")
				if n := c.Str("rotation_interval_days"); n != "" {
					d.Field("Every", n+" days")
				}
				d.Field("Last rotated", a.IO.When(c.Str("rotated_at")))
				d.Field("Due", credDue(s, c.Str("rotation_due_at"), time.Now()))
				d.Field("Expires", a.IO.When(c.Str("expires_at")))
				d.Section("Access")
				d.Field("Revealed", credCount(c, "reveal_count", "time"))
				d.Field("Last revealed", a.IO.When(c.Str("last_revealed_at")))
				d.Render()
				a.IO.Hint("adaa credentials reveal %s --reason \"…\"", c.Str("id"))
			})
		},
	}
}

func credCount(o obj.Obj, field, unit string) string {
	n, ok := o.Int(field)
	switch {
	case !ok:
		return ""
	case n == 1:
		return "once"
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// readSecret gets a secret from stdin or a hidden prompt. Never from a flag
// value, which would end up in shell history and process listings.
func (a *App) readSecret(fromStdin bool, title string) (string, error) {
	if fromStdin {
		b, err := io.ReadAll(io.LimitReader(a.IO.In, 16384+1))
		if err != nil {
			return "", err
		}
		s := strings.TrimRight(string(b), "\r\n")
		if s == "" {
			return "", usagef("--secret-stdin read nothing from stdin")
		}
		return s, nil
	}
	var s string
	if err := a.IO.Secret(title, "--secret-stdin", &s); err != nil {
		return "", err
	}
	if s == "" {
		return "", usagef("the secret cannot be empty")
	}
	return s, nil
}

type credentialFlags struct {
	name, description, username, url, expires string
	rotateDays                                int
}

func (cf *credentialFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&cf.name, "name", "", "Name, e.g. \"Office router admin\"")
	f.StringVar(&cf.description, "description", "", "What it is for")
	f.StringVar(&cf.username, "username", "", "Username that goes with the secret")
	f.StringVar(&cf.url, "url", "", "Where it is used")
	f.IntVar(&cf.rotateDays, "rotate-every", 0, "Raise a finding when it has not been changed for this many days")
	f.StringVar(&cf.expires, "expires", "", "When it stops working, YYYY-MM-DD or RFC 3339 (certificates, tokens)")
}

func (cf *credentialFlags) body(cmd *cobra.Command) (fields, error) {
	b := fields{}
	b.str(cmd, "name", "name", cf.name)
	b.str(cmd, "description", "description", cf.description)
	b.str(cmd, "username", "username", cf.username)
	b.str(cmd, "url", "url", cf.url)
	b.integer(cmd, "rotate-every", "rotation_interval_days", cf.rotateDays)
	if cmd.Flags().Changed("expires") {
		if cf.expires == "" {
			b["expires_at"] = nil
		} else {
			t, err := credParseInstant(cf.expires)
			if err != nil {
				return nil, err
			}
			b["expires_at"] = t
		}
	}
	return b, nil
}

func credParseInstant(s string) (string, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	return "", usagef("--expires must be a date like 2027-01-31")
}

func newCredentialsAddCmd(a *App) *cobra.Command {
	var cf credentialFlags
	var kind, targetType, target string
	var secretStdin bool
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Store a credential",
		Long: `Store a secret. It is encrypted at rest and never shown again except through
'adaa credentials reveal'. The secret is typed at a hidden prompt, or read
from stdin with --secret-stdin; it is never accepted as a flag value.`,
		Example: `  adaa credentials add
  adaa credentials add --name "Office router" --kind password --target-type device --target office-router --username admin
  pbpaste | adaa credentials add --name "Stripe key" --kind api_key --secret-stdin`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := oneOf("kind", kind, credentialKinds...); err != nil {
				return err
			}
			if err := oneOf("target-type", targetType, credentialTargets...); err != nil {
				return err
			}
			if err := a.need(&cf.name, "Name", "--name", nil); err != nil {
				return err
			}
			if kind == "" {
				opts := make([]ui.Option, len(credentialKinds))
				for n, k := range credentialKinds {
					opts[n] = ui.Option{Label: strings.ReplaceAll(k, "_", " "), Value: k}
				}
				var err error
				if kind, err = a.IO.Select("What kind of secret?", "--kind", opts); err != nil {
					return err
				}
			}
			if targetType == "" {
				targetType = "organization"
				if target != "" {
					return usagef("--target needs --target-type")
				}
			}
			body, err := cf.body(cmd)
			if err != nil {
				return err
			}
			body["name"] = cf.name
			body["kind"] = kind
			body["target_type"] = targetType
			if targetType != "organization" {
				k := map[string]Kind{"device": kindDevice, "server": kindServer, "mail": kindMailbox}[targetType]
				id, err := a.Resolve(ctx, k, target)
				if err != nil {
					return err
				}
				body["target_id"] = id
			}
			secret, err := a.readSecret(secretStdin, "Secret")
			if err != nil {
				return err
			}
			body["secret"] = secret
			path, err := a.OrgPath(ctx, "/credentials")
			if err != nil {
				return err
			}
			resp, c, err := a.Send(ctx, http.MethodPost, path, map[string]any(body))
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Stored %s %s", c.Str("name"), a.IO.E().Dim(c.Str("id")))
			return nil
		},
	}
	cf.register(cmd)
	f := cmd.Flags()
	f.StringVar(&kind, "kind", "", "Kind: "+strings.Join(credentialKinds, ", "))
	f.StringVar(&targetType, "target-type", "", "What it opens: "+strings.Join(credentialTargets, ", ")+" (default organization)")
	f.StringVar(&target, "target", "", "The device, server or mailbox it opens")
	f.BoolVar(&secretStdin, "secret-stdin", false, "Read the secret from stdin")
	enumFlag(cmd, "kind", credentialKinds...)
	enumFlag(cmd, "target-type", credentialTargets...)
	return cmd
}

func newCredentialsEditCmd(a *App) *cobra.Command {
	var cf credentialFlags
	cmd := &cobra.Command{
		Use:   "edit [credential]",
		Short: "Change a credential's details",
		Long: `Change a credential's name, username, URL or rotation schedule. To change
the secret itself, use 'adaa credentials rotate'.`,
		Example:           "  adaa credentials edit \"Office router\" --username admin2 --rotate-every 180",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindCredential),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := cf.body(cmd)
			if err != nil {
				return err
			}
			if len(body) == 0 {
				return usagef("nothing to change; pass at least one flag, such as --username")
			}
			return a.patchCredential(cmd.Context(), arg(args), map[string]any(body), "Updated")
		},
	}
	cf.register(cmd)
	return cmd
}

func newCredentialsRotateCmd(a *App) *cobra.Command {
	var secretStdin bool
	cmd := &cobra.Command{
		Use:   "rotate [credential]",
		Short: "Replace a credential's secret",
		Long: `Store a new secret for a credential, after you have changed it wherever it is
used. The previous value is gone for good.`,
		Example: `  adaa credentials rotate "Office router"
  openssl rand -base64 24 | adaa credentials rotate "Office router" --secret-stdin --yes`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindCredential),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindCredential, arg(args))
			if err != nil {
				return err
			}
			secret, err := a.readSecret(secretStdin, "New secret")
			if err != nil {
				return err
			}
			if err := a.Confirm("Replace the stored secret? The old one cannot be recovered."); err != nil {
				return err
			}
			return a.patchCredential(ctx, id, map[string]any{"secret": secret}, "Rotated")
		},
	}
	cmd.Flags().BoolVar(&secretStdin, "secret-stdin", false, "Read the new secret from stdin")
	return cmd
}

func (a *App) patchCredential(ctx context.Context, ref string, body map[string]any, verb string) error {
	id, err := a.Resolve(ctx, kindCredential, ref)
	if err != nil {
		return err
	}
	resp, c, err := a.Send(ctx, http.MethodPatch, "/credentials/"+id, body)
	if err != nil {
		return err
	}
	if a.JSON {
		return a.PrintJSON(resp.Body)
	}
	a.IO.Successf("%s %s", verb, c.Str("name"))
	return nil
}

func newCredentialsDeleteCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "delete [credential]",
		Aliases:           []string{"rm"},
		Short:             "Delete a credential",
		Example:           "  adaa credentials delete \"Old NAS\"",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindCredential),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindCredential, arg(args))
			if err != nil {
				return err
			}
			if err := a.Confirm("Delete this credential? The secret cannot be recovered."); err != nil {
				return err
			}
			if _, err := a.Do(ctx, http.MethodDelete, "/credentials/"+id, nil, nil); err != nil {
				return err
			}
			a.IO.Successf("Deleted %s", id)
			return nil
		},
	}
	return cmd
}

func newCredentialsRevealCmd(a *App) *cobra.Command {
	var reason string
	var printIt bool
	cmd := &cobra.Command{
		Use:   "reveal [credential]",
		Short: "Show a credential's secret (logged)",
		Long: `Reveal a stored secret. The reveal is logged with who, when and your reason,
before the secret is sent.

In a terminal the secret is copied to the clipboard rather than shown; pass
--print to show it. When output is piped, the secret alone is written to
stdout, so it can be passed on without touching the screen.

Only a person may reveal a credential: tokens that act as agents are refused.`,
		Example: `  adaa credentials reveal "Office router" --reason "Reconfiguring VPN after power cut"
  adaa credentials reveal crd_01J… --reason "…" | ssh-add -`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindCredential),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindCredential, arg(args))
			if err != nil {
				return err
			}
			if err := a.need(&reason, "Why do you need it? (logged)", "--reason", nil); err != nil {
				return err
			}
			resp, sec, err := a.Send(ctx, http.MethodPost, "/credentials/"+id+"/reveal", map[string]any{"reason": reason})
			if err != nil {
				var p *api.Problem
				if errors.As(err, &p) && p.Code() == "agents-may-not-reveal" {
					return fmt.Errorf("secrets are only revealed to people, and this credential acts as an agent; ask a person to run `adaa credentials reveal %s`: %w", id, err)
				}
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			secret := sec.Str("secret")
			if a.IO.OutTTY && !printIt {
				if err := clipboard.WriteAll(secret); err == nil {
					user := ""
					if u := sec.Str("username"); u != "" {
						user = " (username " + u + ")"
					}
					a.IO.Successf("Copied the secret to the clipboard%s. This reveal is logged.", user)
					return nil
				}
				a.IO.Warnf("No clipboard available; printing instead.")
			}
			if a.IO.OutTTY {
				if u := sec.Str("username"); u != "" {
					a.IO.Infof("%s %s", a.IO.E().Dim("Username:"), u)
				}
			}
			fmt.Fprintln(a.IO.Out, secret)
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "Why you need it; stored in the activity log (required)")
	cmd.Flags().BoolVar(&printIt, "print", false, "Print the secret instead of copying it to the clipboard")
	return cmd
}
