package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/getadaa/cli/internal/api"
	"github.com/getadaa/cli/internal/auth"
	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() {
	register(newLoginCmd)
	register(newLogoutCmd)
	register(newWhoamiCmd)
	register(newSwitchCmd)
}

func newLoginCmd(a *App) *cobra.Command {
	var email, code, apiURL string
	var withToken bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in with a link sent to your email",
		Long: `Sign in to adaa.

adaa has no passwords. You give your work email, adaa sends a sign-in link,
and you paste the link (or the code in it) back here. The session is kept in
your system keychain.

Without a terminal, sign-in is two steps: --email sends the link, then --code
finishes. For automation, create a token with 'adaa tokens create' and either
set ADAA_TOKEN or pipe it to 'adaa login --with-token'.`,
		Example: `  adaa login
  adaa login --email kari@firma.no
  adaa login --code 'https://adaa.no/login?code=…'
  echo "$ADAA_TOKEN" | adaa login --with-token`,
		GroupID: groupSetup,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if apiURL != "" {
				a.Cfg.APIURL = strings.TrimRight(apiURL, "/")
				if err := a.Cfg.Save(); err != nil {
					return err
				}
			}
			if withToken {
				token, err := readToken(a.IO.In)
				if err != nil {
					return err
				}
				return a.finishLogin(ctx, token, "")
			}

			if code == "" {
				if err := a.need(&email, "Your work email", "--email", validEmail); err != nil {
					return err
				}
				_, err := ui.Spin(a.IO, "Sending a sign-in link…", func() (*api.Response, error) {
					return a.Anonymous().Do(ctx, api.Request{Method: http.MethodPost, Path: "/auth/magic-link",
						Body: map[string]string{"email": email}})
				})
				if err != nil {
					return err
				}
				a.IO.Successf("If %s has an adaa account, a sign-in link is on its way.", email)
				if !a.IO.Interactive() {
					a.IO.Hint("adaa login --code <link or code from the email>")
					return nil
				}
				if err := a.IO.Secret("Paste the link or code from the email", "--code", &code); err != nil {
					return err
				}
			}

			c := codeFromInput(code)
			if len(c) < 16 {
				return usagef("that does not look like a sign-in code; paste the whole link from the email")
			}
			resp, err := ui.Spin(a.IO, "Signing in…", func() (*api.Response, error) {
				return a.Anonymous().Do(ctx, api.Request{Method: http.MethodPost, Path: "/auth/session",
					Body: map[string]string{"code": c}})
			})
			if err != nil {
				var p *api.Problem
				if errors.As(err, &p) && p.Code() == "sign-in-link-expired" {
					a.IO.Hint("adaa login   (each link works once; ask for a new one)")
				}
				return err
			}
			session, err := obj.Parse(resp.Body)
			if err != nil {
				return err
			}
			return a.finishLogin(ctx, session.Str("token"), session.Str("expires_at"))
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "Work email to send the sign-in link to")
	cmd.Flags().StringVar(&code, "code", "", "The sign-in link or code from the email")
	cmd.Flags().BoolVar(&withToken, "with-token", false, "Read an API token from stdin instead")
	cmd.Flags().StringVar(&apiURL, "api-url", "", "Use a different API server, and remember it")
	cmd.MarkFlagsMutuallyExclusive("with-token", "email")
	cmd.MarkFlagsMutuallyExclusive("with-token", "code")
	return cmd
}

func validEmail(s string) error {
	if _, err := mail.ParseAddress(strings.TrimSpace(s)); err != nil || !strings.Contains(s, "@") {
		return errors.New("enter an email address")
	}
	return nil
}

// codeFromInput accepts the whole link from the email, since that is what a
// person copies, as well as the bare code.
func codeFromInput(s string) string {
	s = strings.TrimSpace(s)
	if u, err := url.Parse(s); err == nil && u.Scheme != "" {
		for _, k := range []string{"code", "token"} {
			if v := u.Query().Get(k); v != "" {
				return v
			}
		}
		if f, err := url.ParseQuery(u.Fragment); err == nil {
			if v := f.Get("code"); v != "" {
				return v
			}
		}
		if parts := strings.Split(strings.Trim(u.Path, "/"), "/"); len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}
	return s
}

func readToken(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	t := strings.TrimSpace(line)
	if t == "" {
		return "", usagef("--with-token reads the token from stdin, and stdin was empty")
	}
	return t, nil
}

// finishLogin checks the credential works, then stores it with who it belongs to.
func (a *App) finishLogin(ctx context.Context, token, expiresAt string) error {
	a.client = a.newClient(token)
	a.identity = nil
	me, err := a.Identity(ctx)
	if err != nil {
		a.client = nil
		return err
	}
	a.Cfg.OrganizationID = me.Str("organization.id")
	if a.Cfg.OrganizationID == "" {
		// The membership is where a credential acts, and it is the authority on
		// that: the organization block is a convenience the API fills in beside
		// it and may omit if it cannot read the record.
		a.Cfg.OrganizationID = me.Str("membership.organization_id")
	}
	a.Cfg.OrganizationName = me.Str("organization.name")
	a.Cfg.Email = me.Str("identity.email")
	a.Cfg.Name = me.Str("identity.full_name")
	where, err := auth.Save(a.Cfg, token)
	if err != nil {
		return err
	}
	if err := a.Cfg.Save(); err != nil {
		return err
	}
	who := a.Cfg.Name
	if a.Cfg.OrganizationName != "" {
		who += " (" + a.Cfg.OrganizationName + ")"
	}
	a.IO.Successf("Logged in as %s.", who)
	if others := len(me.List("memberships")); others > 1 {
		a.IO.Infof("%s", a.dim(fmt.Sprintf(
			"You can also act on %d other organizations. `adaa switch` moves between them.", others-1)))
	}
	if where == auth.SourceFile {
		a.IO.Warnf("No system keychain was available, so the session is in the config file.")
	}
	if expiresAt != "" {
		if t, err := time.Parse(time.RFC3339, expiresAt); err == nil {
			a.IO.Infof("%s", a.IO.E().Dim("The session lasts until "+t.Local().Format("2 Jan 2006 15:04")+"."))
		}
	}
	a.IO.Hint("adaa status")
	return nil
}

func newLogoutCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:     "logout",
		Short:   "Sign out and forget the stored session",
		GroupID: groupSetup,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			token, src := auth.Token(a.Cfg)
			if token == "" {
				a.IO.Infof("You are not logged in.")
				return nil
			}
			if src == auth.SourceEnv {
				return errors.New("the token comes from ADAA_TOKEN; unset it to log out")
			}
			// Ending the session server-side is a courtesy; forgetting it
			// locally is what the person asked for, so that always happens.
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			_, _ = a.newClient(token).Do(ctx, api.Request{Method: http.MethodDelete, Path: "/auth/session"})
			if err := auth.Clear(a.Cfg); err != nil {
				return err
			}
			a.IO.Successf("Logged out.")
			return nil
		},
	}
}

// newSwitchCmd moves to another organization the same person may act on.
//
// It is an exchange rather than a sign-in: the session already proves who they
// are, and what comes back is a credential for somewhere else. The one it
// replaces locally keeps working server-side until the sign-in expires, which is
// what lets two terminals sit on two customers.
func newSwitchCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:     "switch [organization]",
		Short:   "Act on another organization you belong to",
		GroupID: groupSetup,
		Long: `Move to another organization you may act on, by handle, name or id.

Nothing is re-authenticated: your session already proves who you are, so this
exchanges it for one at the other organization. Somebody working for one customer
has nothing to switch to, and is told so.

An API token cannot switch: it belongs to one organization by construction, so
issue one where you need it instead.`,
		Example: "  adaa switch\n  adaa switch bjerk",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Whether the credential in hand can switch at all is the API's
			// answer, not a guess from where it was stored: a session exported
			// into ADAA_TOKEN is still a session, and the API refuses a real
			// token with a message saying what to do instead.
			ctx := cmd.Context()
			me, err := a.Identity(ctx)
			if err != nil {
				return err
			}
			here := me.Str("membership.organization_id")

			elsewhere := make([]obj.Obj, 0, 2)
			for _, m := range me.List("memberships") {
				if m.Str("organization_id") != here {
					elsewhere = append(elsewhere, m)
				}
			}
			if len(elsewhere) == 0 {
				a.IO.Infof("You only belong to %s, so there is nowhere to switch to.",
					orNone(me.Str("organization.name"), "this organization"))
				return nil
			}

			target, err := a.pickOrganization(elsewhere, arg(args))
			if err != nil {
				return err
			}

			resp, session, err := a.Send(ctx, http.MethodPost, "/auth/session/switch",
				map[string]any{"organization_id": target})
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			return a.finishLogin(ctx, session.Str("token"), session.Str("expires_at"))
		},
	}
}

// pickOrganization matches what somebody typed against the organizations they
// may act on, by handle first because that is the one chosen once and stable.
func (a *App) pickOrganization(choices []obj.Obj, input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		opts := make([]ui.Option, len(choices))
		for n, m := range choices {
			opts[n] = ui.Option{
				Label: join(" · ", m.Str("organization_name"), peopleRoleLabel(m.Str("role"))),
				Value: m.Str("organization_id"),
			}
		}
		return a.IO.Select("Which organization?", "the organization as an argument", opts)
	}

	var matches []obj.Obj
	for _, m := range choices {
		if strings.EqualFold(m.Str("organization_slug"), input) ||
			strings.EqualFold(m.Str("organization_id"), input) ||
			strings.EqualFold(m.Str("organization_name"), input) {
			return m.Str("organization_id"), nil
		}
		if strings.Contains(strings.ToLower(m.Str("organization_name")), strings.ToLower(input)) {
			matches = append(matches, m)
		}
	}
	if len(matches) == 1 {
		return matches[0].Str("organization_id"), nil
	}
	names := make([]string, 0, len(choices))
	for _, m := range choices {
		names = append(names, orNone(m.Str("organization_slug"), m.Str("organization_name")))
	}
	return "", usagef("no organization of yours matches %q; you may act on: %s",
		input, strings.Join(names, ", "))
}

func newWhoamiCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:     "whoami",
		Short:   "Show who you are logged in as, and what you may do",
		GroupID: groupSetup,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := a.Do(cmd.Context(), http.MethodGet, "/me", nil, nil)
			if err != nil {
				return err
			}
			me, err := obj.Parse(resp.Body)
			if err != nil {
				return err
			}
			return a.PrintObj(resp.Body, me, func(me obj.Obj) {
				_, src := auth.Token(a.Cfg)
				d := a.IO.NewDetail(me.Str("identity.full_name"), me.Str("identity.id"))
				d.Field("Email", me.Str("identity.email"))
				d.Field("Role", strings.ReplaceAll(me.Str("membership.role"), "_", " "))
				d.Field("Organization", join(" ", me.Str("organization.name"),
					a.dim(me.Str("membership.organization_id"))))
				// Empty for somebody who may act on this company without working
				// here, which is a real state rather than a missing record.
				if p := me.Str("person.id"); p != "" {
					d.Field("Employee record", a.dim(p))
				} else {
					d.Field("Employee record", a.dim("none — you are not employed here"))
				}
				d.Field("API", a.Cfg.EffectiveAPIURL())
				d.Field("Credential", string(src))
				if others := me.List("memberships"); len(others) > 1 {
					d.Section("Also yours")
					for _, m := range others {
						if m.Str("organization_id") == me.Str("membership.organization_id") {
							continue
						}
						d.Line(join(" · ", m.Str("organization_name"),
							peopleRoleLabel(m.Str("role")), a.dim(m.Str("organization_slug"))))
					}
				}
				d.Section("Permissions")
				d.Line(wrapWords(me.Strings("permissions"), a.IO.Width()))
				d.Render()
			})
		},
	}
}

// wrapWords joins words with spaces, wrapping at width (or 80).
func wrapWords(words []string, width int) string {
	if width <= 0 {
		width = 80
	}
	width -= 4
	var b strings.Builder
	line := 0
	for n, w := range words {
		if n > 0 {
			if line+1+len(w) > width {
				b.WriteString("\n")
				line = 0
			} else {
				b.WriteString(" ")
				line++
			}
		}
		b.WriteString(w)
		line += len(w)
	}
	return b.String()
}
