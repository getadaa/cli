package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/getadaa/cli/internal/api"
	"github.com/getadaa/cli/internal/auth"
	"github.com/getadaa/cli/internal/build"
	"github.com/getadaa/cli/internal/config"
	"github.com/getadaa/cli/internal/skill"
	"github.com/getadaa/cli/internal/update"
	"github.com/spf13/cobra"
)

func init() { register(newDoctorCmd) }

type checkStatus string

const (
	checkOK   checkStatus = "ok"
	checkWarn checkStatus = "warn"
	checkFail checkStatus = "fail"
	checkSkip checkStatus = "skip"
)

// check is one thing doctor looked at. A check with a fix can be repaired by
// --fix; one with only a hint needs the person.
type check struct {
	ID       string      `json:"id"`
	Status   checkStatus `json:"status"`
	Summary  string      `json:"summary"`
	Hint     string      `json:"hint,omitempty"`
	Fixable  bool        `json:"fixable"`
	Fixed    bool        `json:"fixed,omitempty"`
	FixError string      `json:"fix_error,omitempty"`

	fix func(context.Context) error
}

func (c *check) withFix(fn func(context.Context) error) *check {
	c.fix = fn
	c.Fixable = true
	return c
}

func newDoctorCmd(a *App) *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check that adaa works on this computer, and fix what it can",
		Long: `Check everything adaa depends on, report every problem at once, and with
--fix repair what can be repaired:

  version      is this the latest adaa?                    fix: update it
  config       is the config readable and private?         fix: repair it
  api          can this computer reach the adaa API?       (clock checked too)
  keychain     is the session stored safely?               fix: move it to the keychain
  login        is there a working session?                 fix: sign in (needs a terminal)
  skill        is the agent skill installed, and is every  fix: install or update it
               copy the same as the published one?

Exits 1 when a check fails. Warnings do not fail.`,
		Example: `  adaa doctor
  adaa doctor --fix
  adaa doctor --json`,
		GroupID: groupSetup,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			checks := a.runChecks(ctx)
			if fix {
				for _, c := range checks {
					if c.fix == nil || (c.Status != checkWarn && c.Status != checkFail) {
						continue
					}
					if err := c.fix(ctx); err != nil {
						c.FixError = err.Error()
					} else {
						c.Fixed = true
					}
				}
			}
			failed := false
			for _, c := range checks {
				if c.Status == checkFail && !c.Fixed {
					failed = true
				}
			}
			if a.JSON {
				if err := a.printValue(map[string]any{"ok": !failed, "checks": checks}); err != nil {
					return err
				}
			} else {
				a.renderChecks(checks, fix)
			}
			if failed {
				return errSilent
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "Repair everything that can be repaired")
	return cmd
}

func (a *App) runChecks(ctx context.Context) []*check {
	sp := a.IO.StartSpinner("Checking…")
	defer sp.Stop()
	var checks []*check
	step := func(title string, fn func() []*check) {
		sp.Update("Checking " + title + "…")
		checks = append(checks, fn()...)
	}
	step("the version", func() []*check { return []*check{a.checkVersion(ctx)} })
	step("the config", func() []*check { return a.checkConfig() })
	step("the API", func() []*check { return a.checkAPI(ctx) })
	step("the session", func() []*check { return a.checkLogin(ctx) })
	step("the agent skill", func() []*check { return a.checkSkill(ctx) })
	return checks
}

func (a *App) renderChecks(checks []*check, fixing bool) {
	io := a.IO
	e := io.S()
	problems, fixable := 0, 0
	for _, c := range checks {
		var mark string
		switch {
		case c.Fixed:
			mark = e.Green("✓")
		case c.Status == checkOK:
			mark = e.Green("✓")
		case c.Status == checkWarn:
			mark = e.Yellow("!")
		case c.Status == checkFail:
			mark = e.Red("✗")
		default:
			mark = e.Dim("-")
		}
		line := fmt.Sprintf("%s %s", mark, c.Summary)
		if c.Status == checkSkip {
			line = e.Dim(line)
		}
		if c.Fixed {
			line += e.Green("  fixed")
		}
		fmt.Fprintln(io.Out, line)
		if c.FixError != "" {
			fmt.Fprintln(io.Out, "    "+e.Red("could not fix: ")+c.FixError)
		}
		if c.Hint != "" && !c.Fixed && (c.Status == checkWarn || c.Status == checkFail) {
			fmt.Fprintln(io.Out, "    "+e.Dim("→ "+c.Hint))
		}
		if (c.Status == checkWarn || c.Status == checkFail) && !c.Fixed {
			problems++
			if c.Fixable && c.FixError == "" {
				fixable++
			}
		}
	}
	fmt.Fprintln(io.Out)
	switch {
	case problems == 0:
		fmt.Fprintln(io.Out, e.Green("Everything looks good."))
	case fixable > 0 && !fixing:
		fmt.Fprintf(io.Out, "%d problem(s), %d fixable. Run %s.\n", problems, fixable, e.Bold("adaa doctor --fix"))
	default:
		fmt.Fprintf(io.Out, "%d problem(s) need you; see the hints above.\n", problems)
	}
}

func (a *App) checkVersion(ctx context.Context) *check {
	c := &check{ID: "version"}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	rel, err := update.Latest(ctx)
	if !build.IsRelease() {
		c.Status = checkSkip
		c.Summary = "adaa " + build.Version + " is a development build; not comparing versions"
		if err == nil {
			c.Summary += " (latest release is " + rel.Version() + ")"
		}
		return c
	}
	if err != nil {
		c.Status = checkWarn
		c.Summary = "Could not check for a newer adaa: " + err.Error()
		return c
	}
	if !update.Newer(rel.Version(), build.Version) {
		c.Status = checkOK
		c.Summary = "adaa " + build.Version + " is the latest version"
		return c
	}
	method, exe := update.Detect()
	c.Status = checkWarn
	c.Summary = fmt.Sprintf("adaa %s is available (you have %s)", rel.Version(), build.Version)
	if argv := update.Command(method); argv != nil {
		c.Hint = strings.Join(argv, " ")
	} else {
		c.Hint = "adaa update"
	}
	if method == update.Package {
		c.Hint = "download the new package from " + rel.HTMLURL
		return c
	}
	return c.withFix(func(ctx context.Context) error { return a.upgrade(ctx, rel, method, exe) })
}

func (a *App) checkConfig() []*check {
	path, err := config.Path()
	if err != nil {
		return []*check{{ID: "config", Status: checkFail, Summary: "Cannot find a config directory: " + err.Error()}}
	}
	if a.cfgErr != nil {
		c := &check{ID: "config", Status: checkFail, Summary: "The config file is damaged: " + a.cfgErr.Error(),
			Hint: "adaa doctor --fix moves it aside and starts fresh (you will need to log in again)"}
		return []*check{c.withFix(func(context.Context) error {
			broken := path + ".broken-" + time.Now().Format("20060102-150405")
			if err := os.Rename(path, broken); err != nil {
				return err
			}
			a.cfgErr = nil
			a.Cfg = &config.Config{}
			return nil
		})}
	}
	st, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return []*check{{ID: "config", Status: checkOK, Summary: "No config yet at " + tildePath(path) + " (created at login)"}}
	}
	if err != nil {
		return []*check{{ID: "config", Status: checkFail, Summary: "Cannot read " + tildePath(path) + ": " + err.Error()}}
	}
	c := &check{ID: "config", Status: checkOK, Summary: "Config at " + tildePath(path)}
	if perm := st.Mode().Perm(); perm&0o077 != 0 && !isWindows() {
		c.Status = checkWarn
		c.Summary = fmt.Sprintf("%s can be read by other users (mode %o)", tildePath(path), perm)
		c.Hint = "chmod 600 " + tildePath(path)
		c.withFix(func(context.Context) error { return os.Chmod(path, 0o600) })
	}
	return []*check{c}
}

func (a *App) checkAPI(ctx context.Context) []*check {
	base := a.Cfg.EffectiveAPIURL()
	where := base
	if os.Getenv("ADAA_API_URL") != "" {
		where += " (from ADAA_API_URL)"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	start := time.Now()
	// There is no health endpoint. Asking who we are without credentials must
	// be refused with adaa's own problem document, which proves both that the
	// server is reachable and that ADAA_API_URL points at the adaa API.
	resp, err := a.Anonymous().Do(ctx, api.Request{Method: http.MethodGet, Path: "/me"})
	elapsed := time.Since(start).Round(time.Millisecond)
	var p *api.Problem
	switch {
	case errors.As(err, &p) && p.Status == http.StatusUnauthorized && strings.HasPrefix(p.Type, "https://adaa.no/problems/"):
	case errors.As(err, &p) && p.Status >= 500:
		return []*check{{ID: "api", Status: checkFail, Summary: fmt.Sprintf("%s answered %d: %s", where, p.Status, p.Title),
			Hint: "the API is having trouble; try again shortly"}}
	case errors.As(err, &p):
		return []*check{{ID: "api", Status: checkFail, Summary: fmt.Sprintf("%s answered %d, which is not how the adaa API answers", where, p.Status),
			Hint: "check ADAA_API_URL or `adaa login --api-url`; the default is " + config.DefaultAPIURL}}
	case err != nil:
		return []*check{{ID: "api", Status: checkFail, Summary: "Cannot reach " + where + ": " + err.Error(),
			Hint: "check your network connection, VPN or proxy"}}
	}
	reach := &check{ID: "api", Status: checkOK, Summary: fmt.Sprintf("%s is reachable (%s)", where, elapsed)}
	checks := []*check{reach}
	if date, err := http.ParseTime(resp.Header.Get("Date")); err == nil {
		skew := time.Since(date) - elapsed/2
		if skew < 0 {
			skew = -skew
		}
		clock := &check{ID: "clock", Status: checkOK, Summary: "This computer's clock agrees with the API"}
		if skew > 2*time.Minute {
			clock.Status = checkWarn
			clock.Summary = fmt.Sprintf("This computer's clock is %s off", skew.Round(time.Second))
			clock.Hint = "turn on automatic time in your system settings; sign-in links and sessions depend on it"
		}
		checks = append(checks, clock)
	}
	return checks
}

func (a *App) checkLogin(ctx context.Context) []*check {
	var checks []*check
	token, src := auth.Token(a.Cfg)

	keychain := &check{ID: "keychain", Status: checkOK, Summary: "Session is stored in the system keychain"}
	switch {
	case src == auth.SourceEnv:
		keychain.Summary = "Using the token in ADAA_TOKEN"
	case src == auth.SourceFile && auth.KeyringAvailable() == nil:
		keychain.Status = checkWarn
		keychain.Summary = "Session is stored in the config file, but a keychain is available"
		keychain.withFix(func(context.Context) error { _, err := auth.Save(a.Cfg, token); return err })
	case src == auth.SourceFile:
		keychain.Status = checkSkip
		keychain.Summary = "No system keychain here; the session is kept in the config file (readable only by you)"
	case src == auth.SourceNone:
		keychain = nil
	}
	if keychain != nil {
		checks = append(checks, keychain)
	}

	login := &check{ID: "login"}
	checks = append(checks, login)
	relogin := func(ctx context.Context) error {
		if !a.IO.Interactive() {
			return errors.New("signing in needs a terminal; run `adaa login`")
		}
		if src != auth.SourceEnv {
			_ = auth.Clear(a.Cfg)
		}
		a.client, a.identity = nil, nil
		return newLoginCmd(a).RunE(&cobra.Command{}, nil)
	}
	if token == "" {
		login.Status = checkFail
		login.Summary = "Not logged in"
		login.Hint = "adaa login"
		login.withFix(relogin)
		return checks
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	me, err := a.Identity(ctx)
	if err != nil {
		var p *api.Problem
		var n *api.NetworkError
		switch {
		case errors.As(err, &p) && p.Status == 401:
			login.Status = checkFail
			login.Summary = "The stored session no longer works (expired or signed out elsewhere)"
			login.Hint = "adaa login"
			if src == auth.SourceEnv {
				login.Hint = "ADAA_TOKEN is not valid; create a new token with `adaa tokens create`"
				return checks
			}
			login.withFix(relogin)
		case errors.As(err, &n):
			login.Status = checkSkip
			login.Summary = "Could not check the session because the API is unreachable"
		default:
			login.Status = checkFail
			login.Summary = "Could not check the session: " + err.Error()
		}
		return checks
	}
	login.Status = checkOK
	login.Summary = fmt.Sprintf("Logged in as %s <%s> at %s (%s)", me.Str("person.full_name"), me.Str("person.email"),
		me.Str("organization.name"), strings.ReplaceAll(me.Str("membership.role"), "_", " "))
	if src != auth.SourceEnv {
		org := me.Str("organization.id")
		if org != "" && (org != a.Cfg.OrganizationID || me.Str("organization.name") != a.Cfg.OrganizationName) {
			a.Cfg.OrganizationID, a.Cfg.OrganizationName = org, me.Str("organization.name")
			a.Cfg.Email, a.Cfg.Name = me.Str("person.email"), me.Str("person.full_name")
			_ = a.Cfg.Save()
		}
	}
	return checks
}

func (a *App) checkSkill(ctx context.Context) []*check {
	root, _ := os.Getwd()
	installs := skill.Find(root)
	latest, err := skill.Fetch(ctx)
	if err != nil {
		c := &check{ID: "skill", Status: checkWarn, Summary: "Could not download the published skill to compare: " + err.Error()}
		if len(installs) == 0 {
			c.Summary = "The agent skill is not installed, and the published one could not be downloaded"
		}
		return []*check{c}
	}
	sum := skill.Checksum(latest)

	var checks []*check
	byClient := map[string][]skill.Install{}
	for _, in := range installs {
		byClient[in.Client] = append(byClient[in.Client], in)
	}

	for _, in := range installs {
		in := in
		name := clientName(in.Client)
		where := tildePath(in.Path)
		c := &check{ID: "skill"}
		switch {
		case in.Modified:
			c.Status = checkWarn
			c.Summary = fmt.Sprintf("Skill for %s was edited by hand (%s)", name, where)
			c.Hint = "adaa skill install --client " + in.Client + " --force   (replaces your edits)"
		case in.SHA256 == sum:
			c.Status = checkOK
			c.Summary = fmt.Sprintf("Skill for %s is current (%s)", name, where)
		case in.Origin == skill.OriginMarketplace && in.Client == "claude":
			c.Status = checkWarn
			c.Summary = fmt.Sprintf("Skill for %s from the marketplace is outdated", name)
			c.Hint = "/plugin marketplace update adaa, then /plugin update adaa@adaa"
			c.withFix(func(ctx context.Context) error { return updateClaudePlugin(ctx) })
		case in.Origin == skill.OriginMarketplace:
			c.Status = checkWarn
			c.Summary = fmt.Sprintf("Skill for %s from the marketplace is outdated (%s)", name, where)
			c.Hint = "update the adaa plugin from the getadaa/plugins marketplace in " + name
		default:
			c.Status = checkWarn
			c.Summary = fmt.Sprintf("Skill for %s is outdated (%s)", name, where)
			c.Hint = "adaa skill install --client " + in.Client
			client, _ := skill.ClientByID(in.Client)
			c.withFix(func(context.Context) error {
				_, err := skill.Write(client, in.Scope, root, latest, false)
				return err
			})
		}
		checks = append(checks, c)
	}

	// Claude Code loads skills from both places, so two copies means the
	// agent reads two, possibly different, sets of instructions.
	if list := byClient["claude"]; len(list) > 1 && hasMarketplaceCopy(list, "claude") {
		for _, in := range list {
			if in.Origin == skill.OriginCLI && in.Scope == skill.ScopeUser {
				c := &check{ID: "skill-duplicate", Status: checkWarn,
					Summary: "Claude Code has the skill both from the marketplace and from `adaa skill install`",
					Hint:    "adaa skill uninstall --client claude   (the marketplace copy stays)"}
				client, _ := skill.ClientByID("claude")
				checks = append(checks, c.withFix(func(context.Context) error {
					_, err := skill.Remove(client, skill.ScopeUser, root)
					return err
				}))
			}
		}
	}

	for _, client := range skill.Clients {
		if !client.Present() || len(byClient[client.ID]) > 0 {
			continue
		}
		client := client
		c := &check{ID: "skill", Status: checkWarn,
			Summary: fmt.Sprintf("%s is on this computer but does not have the adaa skill", clientName(client.ID)),
			Hint:    "adaa skill install --client " + client.ID}
		if client.ID == "claude" {
			c.Hint += "   (or /plugin install adaa@adaa from the getadaa/plugins marketplace)"
		}
		checks = append(checks, c.withFix(func(context.Context) error {
			_, err := skill.Write(client, skill.ScopeUser, root, latest, false)
			return err
		}))
	}

	if len(checks) == 0 {
		checks = append(checks, &check{ID: "skill", Status: checkSkip,
			Summary: "No AI agents found on this computer, so there is nowhere to install the skill",
			Hint:    "adaa skill install --client claude"})
	}
	return checks
}

func clientName(id string) string {
	if c, ok := skill.ClientByID(id); ok {
		return strings.SplitN(c.Name, " (", 2)[0]
	}
	return id
}

func isWindows() bool { return os.PathSeparator == '\\' }

// printValue prints any Go value as indented JSON on stdout.
func (a *App) printValue(v any) error {
	enc := json.NewEncoder(a.IO.Out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
