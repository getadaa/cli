// Package cli is the adaa command tree.
//
// Each command group lives in its own file and registers itself with
// register(), so groups can be added or removed without touching this file.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"strings"
	"syscall"

	"github.com/getadaa/cli/internal/api"
	"github.com/getadaa/cli/internal/auth"
	"github.com/getadaa/cli/internal/build"
	"github.com/getadaa/cli/internal/config"
	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

// Command groups, in the order `adaa --help` lists them.
const (
	groupCore     = "core"
	groupProducts = "products"
	groupSetup    = "setup"
)

// App is the state every command shares. It is built once per process.
type App struct {
	IO  *ui.IO
	Cfg *config.Config

	JSON    bool
	Yes     bool
	Debug   bool
	NoColor bool
	NoInput bool

	// cfgErr is a damaged config file. Only doctor runs with one, to repair it.
	cfgErr error

	client   *api.Client
	identity obj.Obj
	// afterRun holds work to do once the command finishes, like the update notice.
	afterRun []func()
}

var registry []func(*App) *cobra.Command

// register adds a top-level command. Called from init() in each command file.
func register(fn func(*App) *cobra.Command) {
	registry = append(registry, fn)
}

// Main runs the CLI and returns the process exit code.
func Main() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a := &App{IO: ui.System()}
	return a.Run(ctx, os.Args[1:])
}

// Run executes one invocation. Tests call it with their own IO.
func (a *App) Run(ctx context.Context, args []string) int {
	cfg, err := config.Load()
	var corrupt *config.CorruptError
	switch {
	case errors.As(err, &corrupt):
		a.cfgErr = err
		cfg = &config.Config{}
	case err != nil:
		a.printError(err)
		return exitCode(err)
	}
	a.Cfg = cfg

	root := a.NewRoot()
	root.SetArgs(args)
	root.SetIn(a.IO.In)
	root.SetOut(a.IO.Out)
	root.SetErr(a.IO.Err)

	cmd, err := root.ExecuteContextC(ctx)
	for _, fn := range a.afterRun {
		fn()
	}
	if err != nil {
		if errors.Is(err, errSilent) {
			return 1
		}
		var quiet *silentWith
		if errors.As(err, &quiet) {
			return exitCode(quiet.err)
		}
		if isUsageError(err) {
			fmt.Fprintln(a.IO.Err, a.IO.E().Red("✗")+" "+err.Error())
			if cmd != nil {
				a.IO.Hint("see `%s --help`", cmd.CommandPath())
			}
			return exitUsage
		}
		a.printError(err)
		return exitCode(err)
	}
	return 0
}

func (a *App) NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "adaa",
		Short: "Run your company's IT from the terminal",
		Long: `adaa runs IT for small companies. This is the command line for it: the people,
computers, subscriptions, mailboxes, servers and backups you have, whether they
work, and what happens next.

Start with:
  adaa login      sign in with a link sent to your email
  adaa status     see how everything is doing
  adaa doctor     check that this computer is set up correctly`,
		Version:       versionString(),
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if a.NoColor {
				a.IO.DisableColor()
			}
			if a.NoInput {
				a.IO.NoInput = true
			}
			if a.cfgErr != nil && cmd.Name() != "doctor" {
				return fmt.Errorf("%w (run `adaa doctor --fix`)", a.cfgErr)
			}
			a.scheduleUpdateNotice(cmd)
			return nil
		},
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.CompletionOptions.HiddenDefaultCmd = false

	pf := root.PersistentFlags()
	pf.BoolVar(&a.JSON, "json", false, "Print the API's JSON instead of a table")
	pf.BoolVarP(&a.Yes, "yes", "y", false, "Answer yes to confirmations (required for changes when there is no terminal)")
	pf.BoolVar(&a.NoInput, "no-input", false, "Never prompt; fail with the flag that is needed instead")
	pf.BoolVar(&a.NoColor, "no-color", false, "Disable colour (also NO_COLOR=1)")
	pf.BoolVar(&a.Debug, "debug", os.Getenv("ADAA_DEBUG") != "", "Log every API request to stderr")

	root.AddGroup(
		&cobra.Group{ID: groupCore, Title: "Your IT:"},
		&cobra.Group{ID: groupProducts, Title: "Products:"},
		&cobra.Group{ID: groupSetup, Title: "Setup and troubleshooting:"},
	)
	root.SetHelpCommandGroupID(groupSetup)
	root.SetCompletionCommandGroupID(groupSetup)

	for _, fn := range registry {
		root.AddCommand(fn(a))
	}
	sortCommands(root)
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return &usageError{err}
	})
	return root
}

func sortCommands(root *cobra.Command) {
	cmds := root.Commands()
	slices.SortFunc(cmds, func(x, y *cobra.Command) int { return strings.Compare(x.Name(), y.Name()) })
}

func versionString() string {
	s := "adaa " + build.Version
	var extra []string
	if build.Commit != "" {
		extra = append(extra, build.Commit[:min(7, len(build.Commit))])
	}
	if build.Date != "" {
		extra = append(extra, build.Date)
	}
	extra = append(extra, runtime.GOOS+"/"+runtime.GOARCH)
	return s + " (" + strings.Join(extra, ", ") + ")"
}

func userAgent() string {
	return "adaa-cli/" + build.Version + " (" + runtime.GOOS + "; " + runtime.GOARCH + ")"
}

// Client returns an authenticated API client, or errNotLoggedIn.
func (a *App) Client() (*api.Client, error) {
	if a.client != nil {
		return a.client, nil
	}
	token, _ := auth.Token(a.Cfg)
	if token == "" {
		return nil, errNotLoggedIn
	}
	a.client = a.newClient(token)
	return a.client, nil
}

// Anonymous returns a client without credentials, for sign-in and health checks.
func (a *App) Anonymous() *api.Client {
	return a.newClient("")
}

func (a *App) newClient(token string) *api.Client {
	c := api.New(a.Cfg.EffectiveAPIURL(), token, userAgent())
	if a.Debug {
		c.Debug = a.IO.Err
	}
	return c
}

// Identity is GET /me, fetched once per process.
func (a *App) Identity(ctx context.Context) (obj.Obj, error) {
	if a.identity != nil {
		return a.identity, nil
	}
	c, err := a.Client()
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(ctx, api.Request{Method: "GET", Path: "/me"})
	if err != nil {
		return nil, err
	}
	o, err := obj.Parse(resp.Body)
	if err != nil {
		return nil, err
	}
	a.identity = o
	return o, nil
}

// OrgID is the organization every org-scoped path needs. A credential belongs
// to exactly one organization, so it is remembered at login; ADAA_ORG
// overrides it and /me fills it in when it is missing.
func (a *App) OrgID(ctx context.Context) (string, error) {
	if id := os.Getenv("ADAA_ORG"); id != "" {
		return id, nil
	}
	if _, src := auth.Token(a.Cfg); src != auth.SourceEnv && a.Cfg.OrganizationID != "" {
		return a.Cfg.OrganizationID, nil
	}
	me, err := a.Identity(ctx)
	if err != nil {
		return "", err
	}
	id := me.Str("organization.id")
	if id == "" {
		id = me.Str("person.organization_id")
	}
	if id == "" {
		return "", errors.New("this credential is not tied to an organization")
	}
	return id, nil
}

// OrgPath builds /organizations/{org}<suffix>.
func (a *App) OrgPath(ctx context.Context, suffix string) (string, error) {
	id, err := a.OrgID(ctx)
	if err != nil {
		return "", err
	}
	return "/organizations/" + id + suffix, nil
}
