package cli

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() { register(newWorkspacesCmd) }

var (
	workspaceVendors      = []string{"microsoft_365", "google_workspace"}
	workspaceGroupKinds   = []string{"distribution_list", "shared_mailbox", "security_group", "team"}
	workspaceAcctStatuses = []string{"provisioning", "active", "suspended", "deleted"}
	// wsPollInterval is how often a pending consent is checked. Tests shorten it.
	wsPollInterval = 3 * time.Second
)

func newWorkspacesCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workspaces",
		Aliases: []string{"workspace", "ws"},
		Short:   "Microsoft 365 and Google Workspace: accounts, groups and security",
		Long: `The Microsoft 365 or Google Workspace environments adaa administers for the
company: who has an account, who is in which group, and how secure it is.`,
		GroupID: groupProducts,
	}
	accounts := &cobra.Command{Use: "accounts", Aliases: []string{"account"}, Short: "User accounts inside a workspace"}
	accounts.AddCommand(newWSAccountsListCmd(a), newWSAccountsViewCmd(a), newWSAccountsAddCmd(a), newWSAccountsEditCmd(a),
		newWSAccountsDeleteCmd(a), newWSAccountsResetPasswordCmd(a), newWSAccountsAssignCmd(a), newWSAccountsUnassignCmd(a))
	groups := &cobra.Command{Use: "groups", Aliases: []string{"group"}, Short: "Distribution lists, shared mailboxes, security groups and teams"}
	groups.AddCommand(newWSGroupsListCmd(a), newWSGroupsAddCmd(a), newWSGroupsMemberCmd(a, true), newWSGroupsMemberCmd(a, false))
	cmd.AddCommand(newWSListCmd(a), newWSViewCmd(a), newWSAddCmd(a), newWSEditCmd(a), newWSConnectCmd(a),
		newWSAuthorizationCmd(a), newWSReauthorizeCmd(a), newWSSecurityCmd(a), newWSDrivesCmd(a), accounts, groups)
	return cmd
}

func newWSListCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List workspaces",
		Example: `  adaa workspaces list`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := a.OrgPath(cmd.Context(), "/workspaces")
			if err != nil {
				return err
			}
			l, err := a.ListItems(cmd.Context(), path, nil)
			if err != nil {
				return err
			}
			return a.PrintListing(l, "No workspaces yet. Connect one with `adaa workspaces connect`.",
				[]string{"Domain", "Vendor", "Status", "Accounts", "Synced", "ID"},
				func(w obj.Obj) []string {
					return []string{w.Str("primary_domain"), wsVendor(w.Str("vendor")), a.status(w.Str("status")),
						fmt.Sprintf("%s active", orNone(w.Str("accounts_active"), "0")), a.IO.When(w.Str("synced_at")), a.dim(w.Str("id"))}
				})
		},
	}
}

func wsVendor(v string) string {
	switch v {
	case "microsoft_365":
		return "Microsoft 365"
	case "google_workspace":
		return "Google Workspace"
	}
	return v
}

func newWSViewCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "view [workspace]",
		Short:             "Show a workspace",
		Example:           `  adaa workspaces view firma.no`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindWorkspace),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.Resolve(cmd.Context(), kindWorkspace, arg(args))
			if err != nil {
				return err
			}
			w, raw, err := a.Get(cmd.Context(), "/workspaces/"+id, nil)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, w, a.renderWorkspace)
		},
	}
}

func (a *App) renderWorkspace(w obj.Obj) {
	s := a.IO.S()
	v := a.IO.NewDetail(orNone(w.Str("display_name"), w.Str("primary_domain")), w.Str("id"))
	v.Field("Vendor", wsVendor(w.Str("vendor")))
	v.Field("Status", a.status(w.Str("status")))
	if w.Has("manageable") && !w.Bool("manageable") {
		v.Field("Access", s.Yellow("read only"))
	}
	v.Field("Accounts", fmt.Sprintf("%s total, %s active, %s suspended",
		orNone(w.Str("accounts_total"), "0"), orNone(w.Str("accounts_active"), "0"), orNone(w.Str("accounts_suspended"), "0")))
	v.Field("Synced", a.IO.When(w.Str("synced_at")))
	v.Field("Vendor id", w.Str("external_id"))
	v.Field("Notes", w.Str("notes"))
	if ds := w.List("domains"); len(ds) > 0 {
		v.Section("Domains")
		for _, d := range ds {
			line := d.Str("name")
			if d.Bool("primary") {
				line += s.Dim(" (primary)")
			}
			if !d.Bool("verified") {
				line += " " + s.Yellow("unverified")
			}
			v.Line(line)
		}
	}
	v.Render()
	if w.Str("status") == "unauthorized" {
		a.IO.Hint("adaa workspaces reauthorize %s", w.Str("primary_domain"))
	}
}

func newWSAddCmd(a *App) *cobra.Command {
	var vendor, domain, name, externalID, notes string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Record a workspace adaa administers, without connecting to it",
		Long: `Record a workspace by hand. To give adaa access to it, use 'adaa workspaces
connect' instead, which records it as part of consent.`,
		Example: `  adaa workspaces add --vendor microsoft_365 --domain firma.no`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := oneOf("vendor", vendor, workspaceVendors...); err != nil {
				return err
			}
			if vendor == "" {
				v, err := a.IO.Select("Which vendor?", "--vendor", wsVendorOptions())
				if err != nil {
					return err
				}
				vendor = v
			}
			if err := a.need(&domain, "Primary domain", "--domain", nil); err != nil {
				return err
			}
			body := fields{"vendor": vendor, "primary_domain": domain}
			body.str(cmd, "name", "display_name", name)
			body.str(cmd, "external-id", "external_id", externalID)
			body.str(cmd, "notes", "notes", notes)
			path, err := a.OrgPath(cmd.Context(), "/workspaces")
			if err != nil {
				return err
			}
			resp, w, err := a.Send(cmd.Context(), http.MethodPost, path, body)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Recorded %s %s", w.Str("primary_domain"), a.IO.E().Dim(w.Str("id")))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&vendor, "vendor", "", "microsoft_365 or google_workspace")
	f.StringVar(&domain, "domain", "", "Primary domain")
	f.StringVar(&name, "name", "", "Display name")
	f.StringVar(&externalID, "external-id", "", "The vendor's tenant or customer id")
	f.StringVar(&notes, "notes", "", "Free-text notes")
	enumFlag(cmd, "vendor", workspaceVendors...)
	return cmd
}

func wsVendorOptions() []ui.Option {
	return []ui.Option{{Label: "Microsoft 365", Value: "microsoft_365"}, {Label: "Google Workspace", Value: "google_workspace"}}
}

func newWSEditCmd(a *App) *cobra.Command {
	var name, notes string
	cmd := &cobra.Command{
		Use:               "edit [workspace]",
		Short:             "Change a workspace's name or notes",
		Example:           `  adaa workspaces edit firma.no --notes "Tenant moved in 2025"`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindWorkspace),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := fields{}
			body.str(cmd, "name", "display_name", name)
			body.str(cmd, "notes", "notes", notes)
			if len(body) == 0 {
				return usagef("nothing to change; pass --name or --notes")
			}
			id, err := a.Resolve(cmd.Context(), kindWorkspace, arg(args))
			if err != nil {
				return err
			}
			resp, w, err := a.Send(cmd.Context(), http.MethodPatch, "/workspaces/"+id, body)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Updated %s", w.Str("primary_domain"))
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Display name")
	cmd.Flags().StringVar(&notes, "notes", "", "Free-text notes")
	return cmd
}

// ---- Consent -------------------------------------------------------------

type wsConsentOpts struct {
	redirectURI string
	noBrowser   bool
	noWait      bool
	timeout     time.Duration
}

func addWSConsentFlags(cmd *cobra.Command, o *wsConsentOpts) {
	f := cmd.Flags()
	f.StringVar(&o.redirectURI, "redirect-uri", "", "Where the vendor sends the administrator back (default: a listener on this computer)")
	f.BoolVar(&o.noBrowser, "no-browser", false, "Print the consent link instead of opening it")
	f.BoolVar(&o.noWait, "no-wait", false, "Print the consent link and return without waiting")
	f.DurationVar(&o.timeout, "timeout", 20*time.Minute, "How long to wait for consent")
}

func newWSConnectCmd(a *App) *cobra.Command {
	var vendor, domain string
	var o wsConsentOpts
	cmd := &cobra.Command{
		Use:   "connect",
		Short: "Give adaa access to a Microsoft 365 or Google Workspace",
		Long: `Connect a workspace. A global administrator of the workspace signs in at
Microsoft or Google, sees exactly what is asked for, and grants or refuses it
there. adaa never sees their password.

The consent link opens in the browser. By default this command listens on
this computer for the vendor's redirect and finishes the connection itself;
with --redirect-uri it waits for the authorization to be granted elsewhere.`,
		Example: `  adaa workspaces connect --vendor microsoft_365 --domain firma.no
  adaa workspaces connect --vendor google_workspace --no-browser`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := oneOf("vendor", vendor, workspaceVendors...); err != nil {
				return err
			}
			if vendor == "" {
				v, err := a.IO.Select("Which vendor?", "--vendor", wsVendorOptions())
				if err != nil {
					return err
				}
				vendor = v
			}
			path, err := a.OrgPath(cmd.Context(), "/workspaces/connect")
			if err != nil {
				return err
			}
			return a.wsConsent(cmd.Context(), o, func(redirect string) (obj.Obj, []byte, error) {
				body := map[string]any{"vendor": vendor, "redirect_uri": redirect}
				if domain != "" {
					body["primary_domain"] = domain
				}
				resp, auth, err := a.Send(cmd.Context(), http.MethodPost, path, body)
				if err != nil {
					return nil, nil, err
				}
				return auth, resp.Body, nil
			})
		},
	}
	cmd.Flags().StringVar(&vendor, "vendor", "", "microsoft_365 or google_workspace")
	cmd.Flags().StringVar(&domain, "domain", "", "The domain you expect the workspace to have (checked against what consent grants)")
	enumFlag(cmd, "vendor", workspaceVendors...)
	addWSConsentFlags(cmd, &o)
	return cmd
}

func newWSReauthorizeCmd(a *App) *cobra.Command {
	var o wsConsentOpts
	cmd := &cobra.Command{
		Use:   "reauthorize [workspace]",
		Short: "Ask for consent again after access lapsed or was revoked",
		Long: `Issue a new consent link for a workspace whose status is unauthorized. Consent is
withdrawn at Microsoft or Google, not here, so the company can always cut adaa
off without asking.`,
		Example:           `  adaa workspaces reauthorize firma.no`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindWorkspace),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.Resolve(cmd.Context(), kindWorkspace, arg(args))
			if err != nil {
				return err
			}
			return a.wsConsent(cmd.Context(), o, func(redirect string) (obj.Obj, []byte, error) {
				resp, auth, err := a.Send(cmd.Context(), http.MethodPost, "/workspaces/"+id+"/reauthorize", map[string]any{"redirect_uri": redirect})
				if err != nil {
					return nil, nil, err
				}
				return auth, resp.Body, nil
			})
		},
	}
	addWSConsentFlags(cmd, &o)
	return cmd
}

type wsCallback struct {
	code, state, err string
}

// wsConsent runs the consent flow: start it, show what is being granted, send
// the administrator to the vendor, then finish it from the redirect or wait
// for it to be finished elsewhere.
func (a *App) wsConsent(ctx context.Context, o wsConsentOpts, start func(redirect string) (obj.Obj, []byte, error)) error {
	redirect := o.redirectURI
	var callbacks chan wsCallback
	if redirect == "" && !o.noWait {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("could not listen for the vendor's redirect: %w (pass --redirect-uri)", err)
		}
		callbacks = make(chan wsCallback, 1)
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			cb := wsCallback{code: q.Get("code"), state: q.Get("state"), err: join(": ", q.Get("error"), q.Get("error_description"))}
			if cb.code == "" && cb.err == "" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			msg := "Done. You can close this tab and go back to the terminal."
			if cb.err != "" {
				msg = "Consent was not granted: " + html.EscapeString(cb.err)
			}
			fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>adaa</title><p style=\"font:16px system-ui;margin:3em\">%s</p>", msg)
			select {
			case callbacks <- cb:
			default:
			}
		})}
		go func() { _ = srv.Serve(ln) }()
		defer srv.Close()
		redirect = fmt.Sprintf("http://localhost:%d/callback", ln.Addr().(*net.TCPAddr).Port)
	}

	auth, raw, err := start(redirect)
	if err != nil {
		return err
	}
	if o.noWait {
		if a.JSON {
			return a.PrintJSON(raw)
		}
		fmt.Fprintln(a.IO.Out, auth.Str("consent_url"))
		a.IO.Hint("adaa workspaces authorization %s", auth.Str("id"))
		return nil
	}

	e := a.IO.E()
	if grants := auth.List("grants"); len(grants) > 0 {
		fmt.Fprintln(a.IO.Err, e.Bold("Consent covers:"))
		for _, g := range grants {
			mark := e.Dim("read ")
			if g.Bool("write") {
				mark = e.Yellow("write")
			}
			fmt.Fprintf(a.IO.Err, "  %s  %s\n", mark, g.Str("summary"))
		}
		fmt.Fprintln(a.IO.Err)
	}
	link := auth.Str("consent_url")
	a.IO.Infof("A global administrator of the workspace must open this link:")
	fmt.Fprintln(a.IO.Err, "  "+link)
	if a.IO.Interactive() && !o.noBrowser {
		if err := wsOpenBrowser(link); err == nil {
			a.IO.Infof("%s", e.Dim("Opened it in your browser."))
		}
	}

	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	id := auth.Str("id")
	sp := a.IO.StartSpinner("Waiting for consent…")
	defer sp.Stop()
	for {
		select {
		case <-ctx.Done():
			sp.Stop()
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				a.IO.Hint("adaa workspaces authorization %s", id)
				return fmt.Errorf("consent was not given within %s", o.timeout)
			}
			return ctx.Err()
		case cb := <-callbacks:
			sp.Stop()
			if cb.err != "" {
				return fmt.Errorf("consent was not granted: %s", cb.err)
			}
			return a.wsComplete(ctx, id, cb.code, cb.state)
		case <-time.After(wsPollInterval):
			cur, curRaw, err := a.Get(ctx, "/workspace-authorizations/"+id, nil)
			if err != nil {
				return err
			}
			switch cur.Str("status") {
			case "granted":
				sp.Stop()
				if a.JSON {
					return a.PrintJSON(curRaw)
				}
				a.IO.Successf("Connected %s", orNone(cur.Str("workspace_id"), id))
				if ws := cur.Str("workspace_id"); ws != "" {
					a.IO.Hint("adaa workspaces security %s", ws)
				}
				return nil
			case "denied":
				sp.Stop()
				return fmt.Errorf("consent was refused: %s", orNone(cur.Str("denied_reason"), "no reason given"))
			case "expired":
				sp.Stop()
				return errors.New("the consent link expired; start again")
			}
		}
	}
}

func (a *App) wsComplete(ctx context.Context, id, code, state string) error {
	resp, w, err := a.Send(ctx, http.MethodPost, "/workspace-authorizations/"+id+"/complete",
		map[string]any{"code": code, "state": state})
	if err != nil {
		return err
	}
	if a.JSON {
		return a.PrintJSON(resp.Body)
	}
	a.IO.Successf("Connected %s %s. Accounts, groups and security are being read now.",
		orNone(w.Str("primary_domain"), "the workspace"), a.IO.E().Dim(w.Str("id")))
	a.IO.Hint("adaa workspaces security %s", orNone(w.Str("primary_domain"), w.Str("id")))
	return nil
}

func wsOpenBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}

func newWSAuthorizationCmd(a *App) *cobra.Command {
	var callbackURL, code, state string
	cmd := &cobra.Command{
		Use:   "authorization <authorization>",
		Short: "Show a consent request, or finish it with the vendor's redirect",
		Long: `Show where a consent request got to. If the vendor redirected the administrator
somewhere that did not finish the connection, paste the address they landed on
with --callback-url (or pass --code and --state) to finish it here.`,
		Example: `  adaa workspaces authorization wau_01JATX3M4K7Q2YV8N0RCBEZ5HS
  adaa workspaces authorization wau_01J… --callback-url 'https://…/callback?code=…&state=…'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			if !strings.HasPrefix(id, "wau_") {
				return usagef("pass an authorization id (wau_…)")
			}
			if callbackURL != "" {
				u, err := url.Parse(callbackURL)
				if err != nil {
					return usagef("--callback-url is not a URL: %v", err)
				}
				q := u.Query()
				if e := q.Get("error"); e != "" {
					return fmt.Errorf("consent was not granted: %s", join(": ", e, q.Get("error_description")))
				}
				code, state = q.Get("code"), q.Get("state")
				if code == "" || state == "" {
					return usagef("--callback-url has no code and state in it")
				}
			}
			if code != "" || state != "" {
				if code == "" || state == "" {
					return usagef("pass both --code and --state")
				}
				return a.wsComplete(cmd.Context(), id, code, state)
			}
			au, raw, err := a.Get(cmd.Context(), "/workspace-authorizations/"+id, nil)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, au, func(au obj.Obj) {
				v := a.IO.NewDetail(wsVendor(au.Str("vendor"))+" consent", au.Str("id"))
				v.Field("Status", a.status(au.Str("status")))
				v.Field("Workspace", au.Str("workspace_id"))
				v.Field("Refused because", au.Str("denied_reason"))
				v.Field("Expires", a.IO.When(au.Str("expires_at")))
				if au.Str("status") == "pending" {
					v.Field("Link", au.Str("consent_url"))
				}
				if gs := au.List("grants"); len(gs) > 0 {
					v.Section("Covers")
					for _, g := range gs {
						mode := "read"
						if g.Bool("write") {
							mode = "write"
						}
						v.Line(mode + "  " + g.Str("summary"))
					}
				}
				v.Render()
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&callbackURL, "callback-url", "", "The address the vendor redirected to, with code and state")
	f.StringVar(&code, "code", "", "The code from the vendor's redirect")
	f.StringVar(&state, "state", "", "The state from the vendor's redirect")
	cmd.MarkFlagsMutuallyExclusive("callback-url", "code")
	return cmd
}

// ---- Security and drives -------------------------------------------------

func newWSSecurityCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "security [workspace]",
		Short: "Who lacks MFA, who is admin, and how far files can be shared",
		Long: `The security posture of a workspace, with the accounts behind every number:
who has no multi-factor authentication, who holds administrator rights, who
has not signed in for a long time, and how widely files can be shared.`,
		Example:           `  adaa workspaces security firma.no`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindWorkspace),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.Resolve(cmd.Context(), kindWorkspace, arg(args))
			if err != nil {
				return err
			}
			sec, raw, err := a.Get(cmd.Context(), "/workspaces/"+id+"/security", nil)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, sec, a.renderWSSecurity)
		},
	}
}

func (a *App) renderWSSecurity(sec obj.Obj) {
	s := a.IO.S()
	n := func(p string) int64 { v, _ := sec.Int(p); return v }
	v := a.IO.NewDetail("Security", sec.Str("workspace_id"))
	v.Field("Status", a.status(sec.Str("status")))
	v.Field("Summary", sec.Str("summary"))
	mfa := fmt.Sprintf("%d of %d accounts", n("mfa_enabled_count"), n("accounts_total"))
	if m := n("mfa_missing_count"); m > 0 {
		mfa += ", " + s.Red(fmt.Sprintf("%d without", m))
	}
	if u := n("mfa_unknown_count"); u > 0 {
		mfa += s.Dim(fmt.Sprintf(", %d unknown", u))
	}
	v.Field("MFA", mfa)
	v.Field("Admins", strconv.FormatInt(n("admin_count"), 10))
	switch {
	case !sec.Has("legacy_authentication_allowed"):
	case sec.Bool("legacy_authentication_allowed"):
		v.Field("Legacy sign-in", s.Red("allowed (bypasses MFA)"))
	default:
		v.Field("Legacy sign-in", s.Green("blocked"))
	}
	if es := sec.Str("external_sharing"); es != "" {
		label := strings.ReplaceAll(es, "_", " ")
		if es == "anyone" {
			label = s.Red(label)
		}
		v.Field("External sharing", label)
	}
	if u := n("unlinked_account_count"); u > 0 {
		v.Field("Unlinked accounts", s.Yellow(fmt.Sprintf("%d match no employee", u)))
	}
	v.Field("Checked", a.IO.When(sec.Str("last_checked_at")))
	refs := func(title, path string, color func(string) string) {
		rs := sec.List(path)
		if len(rs) == 0 {
			return
		}
		v.Section(title)
		for _, r := range rs {
			line := color(r.Str("user_principal_name"))
			if dn := r.Str("display_name"); dn != "" {
				line += "  " + dn
			}
			if ls := r.Str("last_sign_in_at"); ls != "" {
				line += s.Dim("  last signed in " + a.IO.When(ls))
			}
			v.Line(line)
		}
	}
	refs("Without MFA", "mfa_missing", s.Red)
	refs("Administrators", "admins", func(x string) string { return x })
	refs("Inactive", "inactive_accounts", s.Yellow)
	v.Render()
}

func newWSDrivesCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "drives [workspace]",
		Short:             "Shared drives and sites, and how exposed each is",
		Example:           `  adaa workspaces drives firma.no`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindWorkspace),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.Resolve(cmd.Context(), kindWorkspace, arg(args))
			if err != nil {
				return err
			}
			l, err := a.ListItems(cmd.Context(), "/workspaces/"+id+"/shared-drives", nil)
			if err != nil {
				return err
			}
			s := a.IO.S()
			return a.PrintListing(l, "No shared drives.", []string{"Name", "Exposure", "Members", "Used", "Last activity"},
				func(d obj.Obj) []string {
					exp := strings.ReplaceAll(d.Str("exposure"), "_", " ")
					switch d.Str("exposure") {
					case "anyone_with_link":
						exp = s.Red(exp)
					case "external_guests":
						exp = s.Yellow(exp)
					}
					used := ""
					if b, ok := d.Int("used_bytes"); ok {
						used = ui.Bytes(b)
					}
					return []string{d.Str("name"), exp, d.Str("member_count"), used, a.IO.When(d.Str("last_activity_at"))}
				})
		},
	}
}

// ---- Accounts ------------------------------------------------------------

// wsAccountID accepts an account id, or a sign-in name looked up in one
// workspace (or all of them, when none is given).
func (a *App) wsAccountID(ctx context.Context, input, workspace string) (string, error) {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, "wac_") {
		return input, nil
	}
	if input == "" {
		return "", usagef("pass an account: its id (wac_…) or sign-in name")
	}
	wsIDs, err := a.wsIDs(ctx, workspace)
	if err != nil {
		return "", err
	}
	var hits []obj.Obj
	for _, ws := range wsIDs {
		l, err := a.List(ctx, "/workspaces/"+ws+"/accounts", nil, ListOpts{Limit: 2000})
		if err != nil {
			return "", err
		}
		for _, acc := range l.Items {
			if strings.EqualFold(acc.Str("user_principal_name"), input) || strings.EqualFold(acc.Str("display_name"), input) ||
				wsContainsFold(acc.Strings("aliases"), input) {
				hits = append(hits, acc)
			}
		}
	}
	switch len(hits) {
	case 0:
		return "", &notFoundError{kind: Kind{Name: "workspace account", Plural: "workspaces accounts"}, input: input}
	case 1:
		return hits[0].Str("id"), nil
	}
	opts := make([]ui.Option, len(hits))
	for n, h := range hits {
		opts[n] = ui.Option{Label: h.Str("user_principal_name") + " · " + h.Str("workspace_id"), Value: h.Str("id")}
	}
	if a.IO.Interactive() {
		return a.IO.Select("Several accounts match. Which one?", "--workspace", opts)
	}
	return "", usagef("%q matches accounts in several workspaces; pass --workspace or the account id", input)
}

func wsContainsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

func (a *App) wsIDs(ctx context.Context, workspace string) ([]string, error) {
	if workspace != "" {
		id, err := a.Resolve(ctx, kindWorkspace, workspace)
		if err != nil {
			return nil, err
		}
		return []string{id}, nil
	}
	path, err := a.OrgPath(ctx, "/workspaces")
	if err != nil {
		return nil, err
	}
	l, err := a.ListItems(ctx, path, nil)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(l.Items))
	for _, w := range l.Items {
		ids = append(ids, w.Str("id"))
	}
	return ids, nil
}

func addWSWorkspaceFlag(a *App, cmd *cobra.Command, v *string) {
	cmd.Flags().StringVar(v, "workspace", "", "Workspace to look the account up in (domain or id)")
	_ = cmd.RegisterFlagCompletionFunc("workspace", a.complete(kindWorkspace))
}

func newWSAccountsListCmd(a *App) *cobra.Command {
	var lo ListOpts
	var status string
	var unlinked bool
	var inactiveDays int
	cmd := &cobra.Command{
		Use:     "list [workspace]",
		Aliases: []string{"ls"},
		Short:   "List accounts in a workspace",
		Long: `List the accounts in a workspace. An account linked to no person is either a
service account or somebody nobody told adaa about; --unlinked finds them.`,
		Example: `  adaa workspaces accounts list firma.no
  adaa workspaces accounts list firma.no --unlinked
  adaa workspaces accounts list firma.no --inactive-days 90`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindWorkspace),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := oneOf("status", status, workspaceAcctStatuses...); err != nil {
				return err
			}
			id, err := a.Resolve(cmd.Context(), kindWorkspace, arg(args))
			if err != nil {
				return err
			}
			q := url.Values{}
			setQuery(q, cmd, "status", "status", status)
			if cmd.Flags().Changed("unlinked") {
				q.Set("unlinked", strconv.FormatBool(unlinked))
			}
			if cmd.Flags().Changed("inactive-days") {
				q.Set("inactive_days", strconv.Itoa(inactiveDays))
			}
			l, err := a.List(cmd.Context(), "/workspaces/"+id+"/accounts", q, lo)
			if err != nil {
				return err
			}
			s := a.IO.S()
			return a.PrintListing(l, "No accounts match.", []string{"Account", "Name", "Status", "Person", "MFA", "Last sign-in", "ID"},
				func(acc obj.Obj) []string {
					mfa := s.Dim("unknown")
					if acc.Has("mfa_enabled") {
						mfa = s.Green("on")
						if !acc.Bool("mfa_enabled") {
							mfa = s.Red("off")
						}
					}
					upn := acc.Str("user_principal_name")
					if acc.Bool("is_admin") {
						upn += s.Yellow(" (admin)")
					}
					person := acc.Str("person_id")
					if person == "" {
						person = s.Yellow("unlinked")
					}
					return []string{upn, acc.Str("display_name"), a.status(acc.Str("status")), person, mfa,
						a.IO.When(acc.Str("last_sign_in_at")), a.dim(acc.Str("id"))}
				})
		},
	}
	addListFlags(cmd, &lo)
	cmd.Flags().StringVar(&status, "status", "", "Only accounts in this state")
	cmd.Flags().BoolVar(&unlinked, "unlinked", false, "Only accounts linked to no person")
	cmd.Flags().IntVar(&inactiveDays, "inactive-days", 0, "Only accounts with no sign-in for this many days")
	enumFlag(cmd, "status", workspaceAcctStatuses...)
	return cmd
}

func newWSAccountsViewCmd(a *App) *cobra.Command {
	var workspace string
	cmd := &cobra.Command{
		Use:   "view <account>",
		Short: "Show a workspace account",
		Example: `  adaa workspaces accounts view kari@firma.no
  adaa workspaces accounts view wac_01JATX3M4K7Q2YV8N0RCBEZ5HS`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.wsAccountID(cmd.Context(), args[0], workspace)
			if err != nil {
				return err
			}
			acc, raw, err := a.Get(cmd.Context(), "/workspace-accounts/"+id, nil)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, acc, func(acc obj.Obj) {
				s := a.IO.S()
				v := a.IO.NewDetail(acc.Str("user_principal_name"), acc.Str("id"))
				v.Field("Name", acc.Str("display_name"))
				v.Field("Status", a.status(acc.Str("status")))
				v.Field("Workspace", acc.Str("workspace_id"))
				v.Field("Person", orNone(acc.Str("person_id"), s.Yellow("not linked to anyone")))
				switch {
				case !acc.Has("mfa_enabled"):
					v.Field("MFA", s.Dim("unknown"))
				case acc.Bool("mfa_enabled"):
					v.Field("MFA", s.Green("on"))
				default:
					v.Field("MFA", s.Red("off"))
				}
				if acc.Bool("is_admin") {
					v.Field("Admin", s.Yellow(orNone(strings.Join(acc.Strings("admin_roles"), ", "), "yes")))
				}
				v.Field("Aliases", strings.Join(acc.Strings("aliases"), ", "))
				v.Field("Licenses", strings.Join(acc.Strings("license_ids"), ", "))
				if used, ok := acc.Int("mailbox_used_bytes"); ok {
					mb := ui.Bytes(used)
					if q, ok := acc.Int("mailbox_quota_bytes"); ok && q > 0 {
						mb += " of " + ui.Bytes(q)
					}
					v.Field("Mailbox", mb)
				}
				v.Field("Last sign-in", a.IO.When(acc.Str("last_sign_in_at")))
				v.Render()
				if acc.Str("person_id") == "" {
					a.IO.Hint("adaa workspaces accounts assign %s <person>", acc.Str("user_principal_name"))
				}
			})
		},
	}
	addWSWorkspaceFlag(a, cmd, &workspace)
	return cmd
}

func newWSAccountsAddCmd(a *App) *cobra.Command {
	var upn, name, person, license string
	var aliases []string
	cmd := &cobra.Command{
		Use:   "add [workspace]",
		Short: "Create an account in the workspace",
		Long: `Create an account at the vendor. Usually adaa does this itself when someone is
entitled to a workspace plan and has no account; this is for doing it by hand.`,
		Example:           `  adaa workspaces accounts add firma.no --upn kari@firma.no --person kari@firma.no --license lic_01J…`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindWorkspace),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindWorkspace, arg(args))
			if err != nil {
				return err
			}
			if err := a.need(&upn, "Sign-in name", "--upn", validEmail); err != nil {
				return err
			}
			body := fields{"user_principal_name": upn}
			body.str(cmd, "name", "display_name", name)
			body.strings(cmd, "alias", "aliases", aliases)
			if person != "" {
				pid, err := a.Resolve(ctx, kindPerson, person)
				if err != nil {
					return err
				}
				body["person_id"] = pid
			}
			if license != "" {
				lid, err := a.Resolve(ctx, kindLicense, license)
				if err != nil {
					return err
				}
				body["license_id"] = lid
			}
			resp, acc, err := a.Send(ctx, http.MethodPost, "/workspaces/"+id+"/accounts", body)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Creating %s %s", acc.Str("user_principal_name"), a.IO.E().Dim(acc.Str("id")))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&upn, "upn", "", "Sign-in name (user principal name)")
	f.StringVar(&name, "name", "", "Display name")
	f.StringVar(&person, "person", "", "The employee this account belongs to")
	f.StringVar(&license, "license", "", "License pool to take a seat from")
	f.StringArrayVar(&aliases, "alias", nil, "Alias address (repeatable)")
	_ = cmd.RegisterFlagCompletionFunc("person", a.complete(kindPerson))
	_ = cmd.RegisterFlagCompletionFunc("license", a.complete(kindLicense))
	return cmd
}

func newWSAccountsEditCmd(a *App) *cobra.Command {
	var workspace, name string
	var aliases []string
	var suspend, activate bool
	cmd := &cobra.Command{
		Use:   "edit <account>",
		Short: "Rename, change aliases, or suspend an account",
		Long: `Change an account. --suspend stops access immediately and keeps the mail and
files, which is almost always what is wanted on someone's last day.`,
		Example: `  adaa workspaces accounts edit ola@firma.no --suspend
  adaa workspaces accounts edit kari@firma.no --alias k@firma.no --alias kari.n@firma.no`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if suspend && activate {
				return usagef("pass --suspend or --activate, not both")
			}
			body := fields{}
			body.str(cmd, "name", "display_name", name)
			body.strings(cmd, "alias", "aliases", aliases)
			if suspend {
				body["status"] = "suspended"
			}
			if activate {
				body["status"] = "active"
			}
			if len(body) == 0 {
				return usagef("nothing to change; pass at least one flag (see --help)")
			}
			id, err := a.wsAccountID(cmd.Context(), args[0], workspace)
			if err != nil {
				return err
			}
			if suspend {
				if err := a.Confirm("Suspend " + args[0] + "? They lose access immediately."); err != nil {
					return err
				}
			}
			resp, acc, err := a.Send(cmd.Context(), http.MethodPatch, "/workspace-accounts/"+id, body)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Updating %s at the vendor", acc.Str("user_principal_name"))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "Display name")
	f.StringArrayVar(&aliases, "alias", nil, "Alias address (repeatable; replaces the list)")
	f.BoolVar(&suspend, "suspend", false, "Stop access now, keeping mail and files")
	f.BoolVar(&activate, "activate", false, "Restore a suspended account")
	addWSWorkspaceFlag(a, cmd, &workspace)
	return cmd
}

func newWSAccountsDeleteCmd(a *App) *cobra.Command {
	var workspace string
	var wait bool
	cmd := &cobra.Command{
		Use:   "delete <account>",
		Short: "Delete an account, with its mail and files",
		Long: `Delete an account permanently, together with its mailbox and files. It always
waits for approval. Suspend instead unless you are certain: "we need that
person's mail" usually arrives a month later.`,
		Example: `  adaa workspaces accounts delete ola@firma.no`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.wsAccountID(cmd.Context(), args[0], workspace)
			if err != nil {
				return err
			}
			if err := a.Confirm("Delete " + args[0] + " and its mail and files? (Suspending keeps them.)"); err != nil {
				return err
			}
			resp, err := a.Do(cmd.Context(), http.MethodDelete, "/workspace-accounts/"+id, nil, nil)
			if err != nil {
				return err
			}
			task, err := obj.Parse(resp.Body)
			if err != nil {
				return err
			}
			return a.Accepted(cmd.Context(), resp, task, wait)
		},
	}
	addWSWorkspaceFlag(a, cmd, &workspace)
	addWaitFlag(cmd, &wait)
	return cmd
}

func newWSAccountsResetPasswordCmd(a *App) *cobra.Command {
	var workspace string
	var keepSessions, noChange bool
	cmd := &cobra.Command{
		Use:   "reset-password <account>",
		Short: "Set a new temporary password for an account",
		Long: `Reset an account's password. The new password is printed once, on stdout and
nowhere else, and the reset is written to the activity log. By default the
person must change it at next sign-in and is signed out everywhere.`,
		Example: `  adaa workspaces accounts reset-password kari@firma.no
  adaa workspaces accounts reset-password kari@firma.no --yes | pbcopy`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.wsAccountID(cmd.Context(), args[0], workspace)
			if err != nil {
				return err
			}
			if err := a.Confirm("Reset the password for " + args[0] + "?"); err != nil {
				return err
			}
			body := map[string]any{"require_change_at_next_sign_in": !noChange, "sign_out_everywhere": !keepSessions}
			resp, r, err := a.Send(cmd.Context(), http.MethodPost, "/workspace-accounts/"+id+"/reset-password", body)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			fmt.Fprintln(a.IO.Out, r.Str("password"))
			var notes []string
			if r.Bool("must_change_at_next_sign_in") {
				notes = append(notes, "must be changed at next sign-in")
			}
			if r.Bool("signed_out_everywhere") {
				notes = append(notes, "signed out everywhere")
			}
			a.IO.Successf("New password for %s, shown once%s.", orNone(r.Str("user_principal_name"), args[0]),
				map[bool]string{true: " (" + strings.Join(notes, ", ") + ")", false: ""}[len(notes) > 0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&keepSessions, "keep-sessions", false, "Do not end existing sessions (leave them ending if compromise is suspected)")
	cmd.Flags().BoolVar(&noChange, "no-require-change", false, "Do not require a new password at next sign-in")
	addWSWorkspaceFlag(a, cmd, &workspace)
	return cmd
}

func newWSAccountsAssignCmd(a *App) *cobra.Command {
	var workspace string
	cmd := &cobra.Command{
		Use:   "assign <account> <person>",
		Short: "Link an account to the employee who uses it",
		Long: `Link an account to a person. Until it is linked the account belongs to nobody:
it never shows up in what that person holds, is never released when they
leave, and keeps costing money.`,
		Example: `  adaa workspaces accounts assign kari.n@firma.no kari@firma.no`,
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.wsAccountID(ctx, args[0], workspace)
			if err != nil {
				return err
			}
			pid, err := a.Resolve(ctx, kindPerson, wsArgAt(args, 1))
			if err != nil {
				return err
			}
			resp, acc, err := a.Send(ctx, http.MethodPost, "/workspace-accounts/"+id+"/assign", map[string]any{"person_id": pid})
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Linked %s to %s", acc.Str("user_principal_name"), pid)
			return nil
		},
	}
	addWSWorkspaceFlag(a, cmd, &workspace)
	return cmd
}

func wsArgAt(args []string, n int) string {
	if len(args) > n {
		return args[n]
	}
	return ""
}

func newWSAccountsUnassignCmd(a *App) *cobra.Command {
	var workspace string
	cmd := &cobra.Command{
		Use:     "unassign <account>",
		Short:   "Unlink an account from its person, leaving the account alone",
		Example: `  adaa workspaces accounts unassign scanner@firma.no`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.wsAccountID(cmd.Context(), args[0], workspace)
			if err != nil {
				return err
			}
			resp, acc, err := a.Send(cmd.Context(), http.MethodPost, "/workspace-accounts/"+id+"/unassign", nil)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Unlinked %s", acc.Str("user_principal_name"))
			return nil
		},
	}
	addWSWorkspaceFlag(a, cmd, &workspace)
	return cmd
}

// ---- Groups --------------------------------------------------------------

func newWSGroupsListCmd(a *App) *cobra.Command {
	var kind string
	cmd := &cobra.Command{
		Use:     "list [workspace]",
		Aliases: []string{"ls"},
		Short:   "List groups and how many are in each",
		Example: `  adaa workspaces groups list firma.no
  adaa workspaces groups list firma.no --kind shared_mailbox`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindWorkspace),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := oneOf("kind", kind, workspaceGroupKinds...); err != nil {
				return err
			}
			id, err := a.Resolve(cmd.Context(), kindWorkspace, arg(args))
			if err != nil {
				return err
			}
			q := url.Values{}
			setQuery(q, cmd, "kind", "kind", kind)
			l, err := a.ListItems(cmd.Context(), "/workspaces/"+id+"/groups", q)
			if err != nil {
				return err
			}
			return a.PrintListing(l, "No groups.", []string{"Name", "Kind", "Address", "Members", "ID"},
				func(g obj.Obj) []string {
					return []string{g.Str("name"), strings.ReplaceAll(g.Str("kind"), "_", " "), g.Str("address"),
						g.Str("member_count"), a.dim(g.Str("id"))}
				})
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", "Only groups of this kind")
	enumFlag(cmd, "kind", workspaceGroupKinds...)
	return cmd
}

func newWSGroupsAddCmd(a *App) *cobra.Command {
	var kind, name, address, description string
	cmd := &cobra.Command{
		Use:   "add [workspace]",
		Short: "Create a group",
		Example: `  adaa workspaces groups add firma.no --kind distribution_list --name Salg --address salg@firma.no
  adaa workspaces groups add firma.no --kind security_group --name "VPN users"`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindWorkspace),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := oneOf("kind", kind, workspaceGroupKinds...); err != nil {
				return err
			}
			id, err := a.Resolve(cmd.Context(), kindWorkspace, arg(args))
			if err != nil {
				return err
			}
			if kind == "" {
				opts := make([]ui.Option, len(workspaceGroupKinds))
				for n, k := range workspaceGroupKinds {
					opts[n] = ui.Option{Label: strings.ReplaceAll(k, "_", " "), Value: k}
				}
				if kind, err = a.IO.Select("What kind of group?", "--kind", opts); err != nil {
					return err
				}
			}
			if err := a.need(&name, "Name", "--name", nil); err != nil {
				return err
			}
			body := fields{"kind": kind, "name": name}
			body.str(cmd, "address", "address", address)
			body.str(cmd, "description", "description", description)
			resp, g, err := a.Send(cmd.Context(), http.MethodPost, "/workspaces/"+id+"/groups", body)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Creating %s %s", g.Str("name"), a.IO.E().Dim(g.Str("id")))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&kind, "kind", "", "distribution_list, shared_mailbox, security_group or team")
	f.StringVar(&name, "name", "", "Group name")
	f.StringVar(&address, "address", "", "Email address, for distribution lists and shared mailboxes")
	f.StringVar(&description, "description", "", "What the group is for")
	enumFlag(cmd, "kind", workspaceGroupKinds...)
	return cmd
}

// wsGroupID accepts a group id, or a name or address looked up in the given
// workspace (or all of them). It also returns the group's workspace, so the
// member can be looked up in the same place.
func (a *App) wsGroupID(ctx context.Context, input, workspace string) (id, wsID string, err error) {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, "wgr_") {
		if workspace != "" {
			wsID, err = a.Resolve(ctx, kindWorkspace, workspace)
		}
		return input, wsID, err
	}
	wsIDs, err := a.wsIDs(ctx, workspace)
	if err != nil {
		return "", "", err
	}
	var hits []obj.Obj
	for _, ws := range wsIDs {
		l, err := a.ListItems(ctx, "/workspaces/"+ws+"/groups", nil)
		if err != nil {
			return "", "", err
		}
		for _, g := range l.Items {
			if strings.EqualFold(g.Str("name"), input) || strings.EqualFold(g.Str("address"), input) {
				hits = append(hits, g)
			}
		}
	}
	switch len(hits) {
	case 0:
		return "", "", &notFoundError{kind: Kind{Name: "group", Plural: "workspaces groups"}, input: input}
	case 1:
		return hits[0].Str("id"), hits[0].Str("workspace_id"), nil
	}
	return "", "", usagef("%q matches %d groups; pass --workspace or the group id", input, len(hits))
}

func newWSGroupsMemberCmd(a *App, add bool) *cobra.Command {
	var workspace, role string
	use, short, example := "remove-member <group> <account>", "Take an account out of a group",
		`  adaa workspaces groups remove-member salg@firma.no ola@firma.no`
	if add {
		use, short, example = "add-member <group> <account>", "Put an account in a group",
			`  adaa workspaces groups add-member salg@firma.no kari@firma.no
  adaa workspaces groups add-member "VPN users" kari@firma.no --role owner`
	}
	cmd := &cobra.Command{
		Use:     use,
		Short:   short,
		Example: example,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := oneOf("role", role, "member", "owner"); err != nil {
				return err
			}
			ctx := cmd.Context()
			gid, wsID, err := a.wsGroupID(ctx, args[0], workspace)
			if err != nil {
				return err
			}
			accWS := workspace
			if accWS == "" {
				accWS = wsID
			}
			acc, err := a.wsAccountID(ctx, args[1], accWS)
			if err != nil {
				return err
			}
			var resp []byte
			var g obj.Obj
			if add {
				body := map[string]any{"account_id": acc}
				if role != "" {
					body["role"] = role
				}
				r, o, err := a.Send(ctx, http.MethodPost, "/workspace-groups/"+gid+"/members", body)
				if err != nil {
					return err
				}
				resp, g = r.Body, o
			} else {
				if err := a.Confirm("Remove " + args[1] + " from " + args[0] + "?"); err != nil {
					return err
				}
				r, o, err := a.Send(ctx, http.MethodDelete, "/workspace-groups/"+gid+"/members/"+acc, nil)
				if err != nil {
					return err
				}
				resp, g = r.Body, o
			}
			if a.JSON {
				return a.PrintJSON(resp)
			}
			verb := "Removing"
			if add {
				verb = "Adding"
			}
			a.IO.Successf("%s %s %s %s", verb, args[1], map[bool]string{true: "to", false: "from"}[add], orNone(g.Str("name"), args[0]))
			return nil
		},
	}
	if add {
		cmd.Flags().StringVar(&role, "role", "", "member (default) or owner")
		enumFlag(cmd, "role", "member", "owner")
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace the group is in (domain or id)")
	_ = cmd.RegisterFlagCompletionFunc("workspace", a.complete(kindWorkspace))
	return cmd
}
