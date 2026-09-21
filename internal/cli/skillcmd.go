package cli

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"strings"

	"github.com/getadaa/cli/internal/skill"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() { register(newSkillCmd) }

func newSkillCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Install the adaa skill for AI agents",
		Long: `The adaa skill teaches AI agents (Claude Code, Codex and others that read
Agent Skills) how to use this CLI safely: what needs --yes, how to preview a
change, when to run 'adaa doctor'.

There is one skill, published in the getadaa/plugins marketplace. You can
install it from there:

  Claude Code:  /plugin marketplace add getadaa/plugins
                /plugin install adaa@adaa
  Codex:        codex plugin marketplace add getadaa/plugins

or let this command download the same file into each agent's skills folder.
'adaa doctor' checks every copy against the published one.`,
		GroupID: groupSetup,
	}
	cmd.AddCommand(newSkillInstallCmd(a), newSkillStatusCmd(a), newSkillUninstallCmd(a), newSkillShowCmd(a))
	return cmd
}

func addSkillTargetFlags(cmd *cobra.Command, clients *[]string, scope *string) {
	cmd.Flags().StringSliceVar(clients, "client", nil, "Agents to install for: "+strings.Join(skill.ClientIDs(), ", ")+", or all")
	cmd.Flags().StringVar(scope, "scope", "user", "user (for you, everywhere) or project (this repository, to commit)")
	enumFlag(cmd, "client", append(skill.ClientIDs(), "all")...)
	enumFlag(cmd, "scope", "user", "project")
}

// skillClients turns --client into clients, asking when it was not given.
func (a *App) skillClients(ids []string, verb string) ([]skill.Client, error) {
	var out []skill.Client
	for _, id := range ids {
		if id == "all" {
			return skill.Clients, nil
		}
		c, ok := skill.ClientByID(id)
		if !ok {
			return nil, usagef("unknown client %q (known: %s)", id, strings.Join(skill.ClientIDs(), ", "))
		}
		out = append(out, c)
	}
	if len(out) > 0 {
		return out, nil
	}
	var present []string
	for _, c := range skill.Clients {
		if c.Present() {
			present = append(present, c.ID)
		}
	}
	if len(present) == 0 {
		present = []string{"claude"}
	}
	if a.IO.Interactive() {
		opts := make([]ui.Option, len(skill.Clients))
		for n, c := range skill.Clients {
			opts[n] = ui.Option{Label: c.Name, Value: c.ID}
		}
		picked, err := a.IO.MultiSelect("Which agents should "+verb+" the adaa skill?", "--client", opts, present)
		if err != nil {
			return nil, err
		}
		if len(picked) == 0 {
			return nil, ui.ErrCancelled
		}
		present = picked
	}
	for _, id := range present {
		c, _ := skill.ClientByID(id)
		out = append(out, c)
	}
	return out, nil
}

func newSkillInstallCmd(a *App) *cobra.Command {
	var clients []string
	var scope string
	var force bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Download the skill into your agents' skills folders",
		Long: `Download the published adaa skill and install it for the agents you pick.

Without --client, agents found on this computer are chosen (and offered in a
picker when there is a terminal). Run it again at any time to update; copies
you edited by hand are left alone unless you pass --force.`,
		Example: `  adaa skill install
  adaa skill install --client claude,codex
  adaa skill install --client claude --scope project`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := oneOf("scope", scope, "user", "project"); err != nil {
				return err
			}
			targets, err := a.skillClients(clients, "get")
			if err != nil {
				return err
			}
			content, err := ui.Spin(a.IO, "Downloading the skill from "+skill.Marketplace+"…", func() ([]byte, error) {
				return skill.Fetch(cmd.Context())
			})
			if err != nil {
				return err
			}
			root, _ := os.Getwd()
			installs := skill.Find(root)
			failed := false
			for _, c := range targets {
				if !force && hasMarketplaceCopy(installs, c.ID) && skill.Scope(scope) == skill.ScopeUser {
					a.IO.Infof("%s already has the skill from the marketplace; skipping so it is not loaded twice.", c.Name)
					continue
				}
				path, err := skill.Write(c, skill.Scope(scope), root, content, force)
				if err != nil {
					failed = true
					a.IO.Failf("%s: %v", c.Name, err)
					continue
				}
				a.IO.Successf("%s: %s", c.Name, tildePath(path))
			}
			if failed {
				return errSilent
			}
			a.IO.Hint("start a new agent session to load it; `adaa doctor` keeps it current")
			return nil
		},
	}
	addSkillTargetFlags(cmd, &clients, &scope)
	cmd.Flags().BoolVar(&force, "force", false, "Replace copies that were edited by hand")
	return cmd
}

func hasMarketplaceCopy(installs []skill.Install, client string) bool {
	for _, in := range installs {
		if in.Client == client && in.Origin == skill.OriginMarketplace {
			return true
		}
	}
	return false
}

func newSkillUninstallCmd(a *App) *cobra.Command {
	var clients []string
	var scope string
	cmd := &cobra.Command{
		Use:     "uninstall",
		Short:   "Remove copies of the skill that adaa installed",
		Example: "  adaa skill uninstall --client codex",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			targets, err := a.skillClients(clients, "lose")
			if err != nil {
				return err
			}
			root, _ := os.Getwd()
			for _, c := range targets {
				path, err := skill.Remove(c, skill.Scope(scope), root)
				switch {
				case errors.Is(err, fs.ErrNotExist):
					a.IO.Infof("%s: not installed", c.Name)
				case err != nil:
					a.IO.Failf("%s: %v", c.Name, err)
				default:
					a.IO.Successf("%s: removed %s", c.Name, tildePath(path))
				}
			}
			if hasMarketplaceCopy(skill.Find(root), "claude") {
				a.IO.Hint("the marketplace copy is managed by Claude Code: /plugin uninstall adaa@adaa")
			}
			return nil
		},
	}
	addSkillTargetFlags(cmd, &clients, &scope)
	return cmd
}

func newSkillStatusCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show every copy of the skill on this computer and whether it is current",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, _ := os.Getwd()
			installs := skill.Find(root)
			latest, fetchErr := skill.Fetch(cmd.Context())
			latestSum := ""
			if fetchErr == nil {
				latestSum = skill.Checksum(latest)
			}
			if a.JSON {
				type row struct {
					skill.Install
					State string `json:"state"`
				}
				rows := make([]row, len(installs))
				for n, in := range installs {
					rows[n] = row{in, skillState(in, latestSum)}
				}
				return a.printValue(map[string]any{"published_sha256": latestSum, "installs": rows})
			}
			if fetchErr != nil {
				a.IO.Warnf("Could not check against the published skill: %v", fetchErr)
			}
			if len(installs) == 0 {
				a.IO.Infof("The adaa skill is not installed anywhere on this computer.")
				a.IO.Hint("adaa skill install")
				return nil
			}
			t := a.IO.NewTable("Agent", "Scope", "From", "State", "Path")
			for _, in := range installs {
				name := in.Client
				if c, ok := skill.ClientByID(in.Client); ok {
					name = strings.SplitN(c.Name, " (", 2)[0]
				}
				t.Row(name, string(in.Scope), string(in.Origin), a.status(skillState(in, latestSum)), tildePath(in.Path))
			}
			t.Render()
			return nil
		},
	}
}

func skillState(in skill.Install, latest string) string {
	switch {
	case in.Modified:
		return "edited"
	case latest == "":
		return "unknown"
	case in.SHA256 == latest:
		return "current"
	}
	return "outdated"
}

func newSkillShowCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the published skill",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := skill.Fetch(cmd.Context())
			if err != nil {
				return err
			}
			_, err = a.IO.Out.Write(b)
			return err
		},
	}
}

// updateClaudePlugin refreshes the marketplace and updates the plugin through
// Claude Code's own CLI, which owns that copy.
func updateClaudePlugin(ctx context.Context) error {
	if _, err := exec.LookPath("claude"); err != nil {
		return errors.New("the claude command is not on PATH; run /plugin marketplace update adaa inside Claude Code")
	}
	for _, argv := range [][]string{
		{"claude", "plugin", "marketplace", "update", "adaa"},
		{"claude", "plugin", "update", skill.PluginID},
	} {
		c := exec.CommandContext(ctx, argv[0], argv[1:]...)
		if out, err := c.CombinedOutput(); err != nil {
			return errors.New(strings.Join(argv, " ") + ": " + strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
