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
	"slices"
	"strings"
	"time"

	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() { register(newConnectionsCmd) }

// consentPollInterval is how often a pending consent is checked. Tests shorten it.
var consentPollInterval = 3 * time.Second

var kindConnection = Kind{Name: "connection", Plural: "connections", Prefix: "pcn_", List: "{org}/provider-connections",
	Match: []string{"name", "account"},
	Label: func(o obj.Obj) string { return join(" · ", o.Str("name"), o.Str("provider"), o.Str("status")) }}

func newConnectionsCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "connections",
		Aliases: []string{"connection", "conn"},
		Short:   "Accounts you hold at suppliers, and what adaa may do through them",
		Long: `Supplier accounts connected to adaa: a Microsoft 365 or Google Workspace
tenant, or a DNS host such as Bunny. A key is sealed into the vault as it
arrives; consent is granted by an administrator at the supplier.

'adaa connections providers' lists who can be connected and how.`,
		GroupID: groupSetup,
	}
	cmd.AddCommand(newConnProvidersCmd(a), newConnListCmd(a), newConnViewCmd(a), newConnAddCmd(a),
		newConnRenewCmd(a), newConnRemoveCmd(a))
	return cmd
}

func (a *App) providers(ctx context.Context) ([]obj.Obj, error) {
	path, err := a.OrgPath(ctx, "/providers")
	if err != nil {
		return nil, err
	}
	l, err := a.ListItems(ctx, path, nil)
	if err != nil {
		return nil, err
	}
	return l.Items, nil
}

func newConnProvidersCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:     "providers",
		Short:   "List the suppliers adaa can work through",
		Example: `  adaa connections providers`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := a.OrgPath(cmd.Context(), "/providers")
			if err != nil {
				return err
			}
			l, err := a.ListItems(cmd.Context(), path, nil)
			if err != nil {
				return err
			}
			return a.PrintListing(l, "No suppliers are available.",
				[]string{"Provider", "Slug", "Used for", "Connect with", "Summary"},
				func(p obj.Obj) []string {
					how := strings.NewReplacer("api_key", "key", "oauth", "consent").Replace(strings.Join(p.Strings("methods"), ", "))
					if !p.Bool("connectable") {
						how = a.dim("adaa's own")
					}
					return []string{p.Str("name"), p.Str("slug"), strings.Join(p.Strings("services"), ", "), how, p.Str("summary")}
				})
		},
	}
}

func newConnListCmd(a *App) *cobra.Command {
	var provider string
	var lo ListOpts
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List connections, including abandoned and disconnected ones",
		Example: `  adaa connections list
  adaa connections list --provider bunny`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			q := url.Values{}
			setQuery(q, cmd, "provider", "provider", provider)
			path, err := a.OrgPath(cmd.Context(), "/provider-connections")
			if err != nil {
				return err
			}
			l, err := a.List(cmd.Context(), path, q, lo)
			if err != nil {
				return err
			}
			return a.PrintListing(l, "Nothing connected yet. See `adaa connections providers`.",
				[]string{"Name", "Provider", "Account", "Status", "Last worked", "ID"},
				func(c obj.Obj) []string {
					return []string{c.Str("name"), c.Str("provider"), c.Str("account"), a.status(c.Str("status")),
						a.IO.When(c.Str("verified_at")), a.dim(c.Str("id"))}
				})
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "Only connections to this supplier (a slug from `adaa connections providers`)")
	addListFlags(cmd, &lo)
	return cmd
}

func newConnViewCmd(a *App) *cobra.Command {
	var callbackURL, code, state string
	cmd := &cobra.Command{
		Use:   "view [connection]",
		Short: "Show a connection, or finish its consent with the supplier's redirect",
		Long: `Show where a connection got to. If the supplier redirected the administrator
somewhere that did not finish the connection, paste the address they landed on
with --callback-url (or pass --code and --state) to finish it here.`,
		Example: `  adaa connections view pcn_01JATY03B4C5D6E7F8G9H0J1K2
  adaa connections view pcn_01J… --callback-url 'https://…/callback?code=…&state=…'`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindConnection),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			if (code == "") != (state == "") {
				return usagef("pass both --code and --state")
			}
			id, err := a.Resolve(cmd.Context(), kindConnection, arg(args))
			if err != nil {
				return err
			}
			if code != "" {
				return a.completeConsent(cmd.Context(), id, code, state)
			}
			c, raw, err := a.Get(cmd.Context(), "/provider-connections/"+id, nil)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, c, a.renderConnection)
		},
	}
	f := cmd.Flags()
	f.StringVar(&callbackURL, "callback-url", "", "The address the supplier redirected to, with code and state")
	f.StringVar(&code, "code", "", "The code from the supplier's redirect")
	f.StringVar(&state, "state", "", "The state from the supplier's redirect")
	cmd.MarkFlagsMutuallyExclusive("callback-url", "code")
	return cmd
}

func (a *App) renderConnection(c obj.Obj) {
	s := a.IO.S()
	v := a.IO.NewDetail(c.Str("name"), c.Str("id"))
	v.Field("Provider", c.Str("provider"))
	v.Field("Account", c.Str("account"))
	v.Field("Status", a.status(c.Str("status")))
	v.Field("Method", strings.ReplaceAll(c.Str("method"), "_", " "))
	v.Field("Last worked", a.IO.When(c.Str("verified_at")))
	if f := c.Str("failure"); f != "" {
		v.Field("Failure", s.Red(f)+" "+s.Dim(a.IO.When(c.Str("failed_at"))))
	}
	v.Field("Refused because", c.Str("denied_reason"))
	v.Field("Granted", a.IO.When(c.Str("granted_at")))
	v.Field("Disconnected", a.IO.When(c.Str("revoked_at")))
	v.Field("Workspace", c.Str("workspace_id"))
	v.Field("Key", c.Str("credential_id"))
	if c.Str("status") == "pending" {
		v.Field("Link", c.Str("consent_url"))
		v.Field("Link expires", a.IO.When(c.Str("consent_until")))
	}
	if gs := c.List("grants"); len(gs) > 0 {
		v.Section("Lets adaa")
		for _, g := range gs {
			mode := "read "
			if g.Bool("write") {
				mode = "write"
			}
			v.Line(mode + "  " + g.Str("summary"))
		}
	}
	v.Render()
	switch c.Str("status") {
	case "unauthorized":
		if c.Str("method") == "oauth" {
			a.IO.Hint("adaa connections renew %s", c.Str("id"))
		} else if k := c.Str("credential_id"); k != "" {
			a.IO.Hint("adaa credentials rotate %s", k)
		}
	case "pending":
		a.IO.Hint("adaa connections view %s --callback-url '…'", c.Str("id"))
	}
}

func newConnAddCmd(a *App) *cobra.Command {
	var name, account, method, username string
	var secretStdin bool
	var o consentOpts
	cmd := &cobra.Command{
		Use:   "add [provider]",
		Short: "Connect an account you hold at a supplier",
		Long: `Connect a supplier account, with a key or with consent, whichever the supplier
takes. A key is typed at a hidden prompt or read from stdin with
--secret-stdin, and sealed into the vault on arrival.

For consent, an administrator of the account signs in at the supplier and
grants it there; adaa never sees their password. The link opens in the
browser, and by default this command listens on this computer for the
redirect and finishes the connection itself.`,
		Example: `  adaa connections add bunny --name "Bunny DNS" --secret-stdin < key.txt
  adaa connections add microsoft_365 --account firma.no`,
		Args: cobra.MaximumNArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			ps, err := a.providers(cmd.Context())
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveError
			}
			var out []cobra.Completion
			for _, p := range ps {
				if p.Bool("connectable") {
					out = append(out, cobra.CompletionWithDesc(p.Str("slug"), p.Str("name")))
				}
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := oneOf("method", method, "api_key", "oauth"); err != nil {
				return err
			}
			ctx := cmd.Context()
			p, err := a.pickProvider(ctx, arg(args), "")
			if err != nil {
				return err
			}
			return a.connect(ctx, p, connectInput{name: name, account: account, method: method, username: username, secretStdin: secretStdin}, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "What to call it (default: the account, or the supplier's name)")
	f.StringVar(&account, "account", "", "The account at the supplier, where it asks for one (a workspace's primary domain)")
	f.StringVar(&method, "method", "", "api_key or oauth, where the supplier takes both")
	f.StringVar(&username, "username", "", "The name or id the key is paired with, where the supplier issues one")
	f.BoolVar(&secretStdin, "secret-stdin", false, "Read the key from stdin")
	enumFlag(cmd, "method", "api_key", "oauth")
	addConsentFlags(cmd, &o)
	return cmd
}

// pickProvider finds a connectable supplier by slug or name, or asks for one.
// service narrows the choice to suppliers usable for it, e.g. "workspace".
func (a *App) pickProvider(ctx context.Context, input, service string) (obj.Obj, error) {
	all, err := a.providers(ctx)
	if err != nil {
		return nil, err
	}
	var ps []obj.Obj
	for _, p := range all {
		if p.Bool("connectable") && (service == "" || slices.Contains(p.Strings("services"), service)) {
			ps = append(ps, p)
		}
	}
	if input == "" {
		if len(ps) == 0 {
			return nil, errors.New("no supplier can be connected for this company")
		}
		opts := make([]ui.Option, len(ps))
		for n, p := range ps {
			opts[n] = ui.Option{Label: p.Str("name"), Value: p.Str("slug")}
		}
		slug, err := a.IO.Select("Which supplier?", "the provider as an argument", opts)
		if err != nil {
			return nil, err
		}
		input = slug
	}
	var slugs []string
	for _, p := range ps {
		if strings.EqualFold(p.Str("slug"), input) || strings.EqualFold(p.Str("name"), input) {
			return p, nil
		}
		slugs = append(slugs, p.Str("slug"))
	}
	return nil, usagef("%q is not a supplier that can be connected; one of: %s", input, strings.Join(slugs, ", "))
}

type connectInput struct {
	name, account, method, username string
	secretStdin                     bool
}

func (a *App) connect(ctx context.Context, p obj.Obj, in connectInput, o consentOpts) error {
	methods := p.Strings("methods")
	switch {
	case in.method != "" && !slices.Contains(methods, in.method):
		return usagef("%s is not connected with %s; use %s", p.Str("name"), in.method, strings.Join(methods, " or "))
	case in.method == "" && len(methods) == 1:
		in.method = methods[0]
	case in.method == "":
		m, err := a.IO.Select("How do you want to connect "+p.Str("name")+"?", "--method", []ui.Option{
			{Label: "With an API key", Value: "api_key"}, {Label: "With consent at " + p.Str("name"), Value: "oauth"}})
		if err != nil {
			return err
		}
		in.method = m
	}
	if acct := p.Obj("account"); acct != nil && acct.Bool("required") {
		if err := a.need(&in.account, acct.Str("label"), "--account", nil); err != nil {
			return err
		}
	}
	if in.name == "" {
		in.name = orNone(in.account, p.Str("name"))
	}
	body := map[string]any{"provider": p.Str("slug"), "method": in.method, "name": in.name}
	if in.account != "" {
		body["account"] = in.account
	}
	path, err := a.OrgPath(ctx, "/provider-connections")
	if err != nil {
		return err
	}

	if in.method == "oauth" {
		return a.consent(ctx, o, func(redirect string) (obj.Obj, []byte, error) {
			if redirect != "" {
				body["redirect_uri"] = redirect
			}
			resp, c, err := a.Send(ctx, http.MethodPost, path, body)
			if err != nil {
				return nil, nil, err
			}
			return c, resp.Body, nil
		})
	}

	title := "API key"
	if sec := p.Obj("secret"); sec != nil {
		title = sec.Str("label")
		if w := sec.Str("where"); w != "" && !in.secretStdin && a.IO.Interactive() {
			a.IO.Infof("%s", a.IO.E().Dim(w))
		}
		if sec.Bool("username") {
			if err := a.need(&in.username, "Username or id that goes with it", "--username", nil); err != nil {
				return err
			}
		}
	}
	if in.username != "" {
		body["username"] = in.username
	}
	secret, err := a.readSecret(in.secretStdin, title)
	if err != nil {
		return err
	}
	body["secret"] = secret
	resp, c, err := a.Send(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	if a.JSON {
		return a.PrintJSON(resp.Body)
	}
	a.IO.Successf("Connected %s %s. It reads unknown until something uses the key.", c.Str("name"), a.IO.E().Dim(c.Str("id")))
	return nil
}

func newConnRenewCmd(a *App) *cobra.Command {
	var o consentOpts
	cmd := &cobra.Command{
		Use:   "renew [connection]",
		Short: "Ask for consent again after access lapsed or was withdrawn",
		Long: `Issue a new consent link for a connection whose status is unauthorized. Consent
is withdrawn at the supplier, not here, so the company can always cut adaa off
without asking. The previous link stops working.

A key connection has nothing to renew: rotate its key with
'adaa credentials rotate' instead.`,
		Example:           `  adaa connections renew firma.no`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindConnection),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.Resolve(cmd.Context(), kindConnection, arg(args))
			if err != nil {
				return err
			}
			return a.renew(cmd.Context(), id, o)
		},
	}
	addConsentFlags(cmd, &o)
	return cmd
}

func (a *App) renew(ctx context.Context, id string, o consentOpts) error {
	return a.consent(ctx, o, func(redirect string) (obj.Obj, []byte, error) {
		body := map[string]any{}
		if redirect != "" {
			body["redirect_uri"] = redirect
		}
		resp, c, err := a.Send(ctx, http.MethodPost, "/provider-connections/"+id+"/renew", body)
		if err != nil {
			return nil, nil, err
		}
		return c, resp.Body, nil
	})
}

func newConnRemoveCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:     "remove [connection]",
		Aliases: []string{"rm", "disconnect"},
		Short:   "Disconnect a supplier account and delete its key",
		Long: `Stop adaa acting through the account, and delete its key. This is final:
connecting the same supplier again makes a new connection.

A connection a domain publishes DNS through is refused; move the domain first.`,
		Example:           `  adaa connections remove "Bunny DNS"`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindConnection),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindConnection, arg(args))
			if err != nil {
				return err
			}
			if err := a.Confirm("Disconnect " + id + " and delete its key? This cannot be undone."); err != nil {
				return err
			}
			resp, c, err := a.Send(ctx, http.MethodDelete, "/provider-connections/"+id, nil)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Disconnected %s", orNone(c.Str("name"), id))
			return nil
		},
	}
}

// ---- Consent -------------------------------------------------------------

type consentOpts struct {
	redirectURI string
	noBrowser   bool
	noWait      bool
	timeout     time.Duration
}

func addConsentFlags(cmd *cobra.Command, o *consentOpts) {
	f := cmd.Flags()
	f.StringVar(&o.redirectURI, "redirect-uri", "", "Where the supplier sends the administrator back (default: a listener on this computer)")
	f.BoolVar(&o.noBrowser, "no-browser", false, "Print the consent link instead of opening it")
	f.BoolVar(&o.noWait, "no-wait", false, "Print the consent link and return without waiting")
	f.DurationVar(&o.timeout, "timeout", 20*time.Minute, "How long to wait for consent")
}

type consentCallback struct {
	code, state, err string
}

// consent runs a consent flow: start it, show what is being granted, send the
// administrator to the supplier, then finish it from the redirect or wait for
// it to be finished elsewhere.
func (a *App) consent(ctx context.Context, o consentOpts, start func(redirect string) (obj.Obj, []byte, error)) error {
	redirect := o.redirectURI
	var callbacks chan consentCallback
	if redirect == "" && !o.noWait {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("could not listen for the supplier's redirect: %w (pass --redirect-uri)", err)
		}
		callbacks = make(chan consentCallback, 1)
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			cb := consentCallback{code: q.Get("code"), state: q.Get("state"), err: join(": ", q.Get("error"), q.Get("error_description"))}
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

	c, raw, err := start(redirect)
	if err != nil {
		return err
	}
	id := c.Str("id")
	if o.noWait {
		if a.JSON {
			return a.PrintJSON(raw)
		}
		fmt.Fprintln(a.IO.Out, c.Str("consent_url"))
		a.IO.Hint("adaa connections view %s", id)
		return nil
	}

	e := a.IO.E()
	if grants := c.List("grants"); len(grants) > 0 {
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
	link := c.Str("consent_url")
	a.IO.Infof("An administrator of the account must open this link:")
	fmt.Fprintln(a.IO.Err, "  "+link)
	if a.IO.Interactive() && !o.noBrowser {
		if err := openBrowser(link); err == nil {
			a.IO.Infof("%s", e.Dim("Opened it in your browser."))
		}
	}

	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	sp := a.IO.StartSpinner("Waiting for consent…")
	defer sp.Stop()
	for {
		select {
		case <-ctx.Done():
			sp.Stop()
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				a.IO.Hint("adaa connections view %s", id)
				return fmt.Errorf("consent was not given within %s", o.timeout)
			}
			return ctx.Err()
		case cb := <-callbacks:
			sp.Stop()
			if cb.err != "" {
				return fmt.Errorf("consent was not granted: %s", cb.err)
			}
			// A redirect carrying somebody else's state did not come from this
			// flow; the API would refuse it too, but this says why.
			if want := c.Str("state"); want != "" && cb.state != want {
				return errors.New("the redirect's state does not match the consent this command started; not completing it")
			}
			return a.completeConsent(ctx, id, cb.code, cb.state)
		case <-time.After(consentPollInterval):
			cur, curRaw, err := a.Get(ctx, "/provider-connections/"+id, nil)
			if err != nil {
				return err
			}
			switch cur.Str("status") {
			case "pending":
			case "connected", "unknown":
				sp.Stop()
				if a.JSON {
					return a.PrintJSON(curRaw)
				}
				a.connected(cur)
				return nil
			case "denied":
				sp.Stop()
				return fmt.Errorf("consent was refused: %s", orNone(cur.Str("denied_reason"), "no reason given"))
			case "expired":
				sp.Stop()
				return errors.New("the consent link expired; start again")
			default:
				sp.Stop()
				return fmt.Errorf("the connection is %s", cur.Str("status"))
			}
		}
	}
}

func (a *App) completeConsent(ctx context.Context, id, code, state string) error {
	resp, c, err := a.Send(ctx, http.MethodPost, "/provider-connections/"+id+"/complete",
		map[string]any{"code": code, "state": state})
	if err != nil {
		return err
	}
	if a.JSON {
		return a.PrintJSON(resp.Body)
	}
	a.connected(c)
	return nil
}

func (a *App) connected(c obj.Obj) {
	name := orNone(c.Str("name"), c.Str("id"))
	if ws := c.Str("workspace_id"); ws != "" {
		a.IO.Successf("Connected %s %s. Accounts, groups and security are being read now.", name, a.IO.E().Dim(ws))
		a.IO.Hint("adaa workspaces security %s", ws)
		return
	}
	a.IO.Successf("Connected %s %s", name, a.IO.E().Dim(c.Str("id")))
}

func openBrowser(u string) error {
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
