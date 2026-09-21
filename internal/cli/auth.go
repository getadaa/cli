package cli

import (
	"bufio"
	"context"
	"errors"
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
		a.Cfg.OrganizationID = me.Str("person.organization_id")
	}
	a.Cfg.OrganizationName = me.Str("organization.name")
	a.Cfg.Email = me.Str("person.email")
	a.Cfg.Name = me.Str("person.full_name")
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
				d := a.IO.NewDetail(me.Str("person.full_name"), me.Str("person.id"))
				d.Field("Email", me.Str("person.email"))
				d.Field("Role", strings.ReplaceAll(me.Str("person.portal_role"), "_", " "))
				d.Field("Organization", join(" ", me.Str("organization.name"), a.dim(me.Str("organization.id"))))
				d.Field("API", a.Cfg.EffectiveAPIURL())
				d.Field("Credential", string(src))
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
