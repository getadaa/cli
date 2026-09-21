package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/getadaa/cli/internal/build"
	"github.com/getadaa/cli/internal/config"
	"github.com/getadaa/cli/internal/ui"
	"github.com/getadaa/cli/internal/update"
	"github.com/spf13/cobra"
)

func init() { register(newUpdateCmd) }

func newUpdateCmd(a *App) *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:     "update",
		Aliases: []string{"upgrade"},
		Short:   "Update adaa to the latest version",
		Long: `Update adaa to the latest release.

adaa updates itself the same way it was installed: with brew for Homebrew,
scoop for Scoop, and by replacing its own binary (after checking the release
checksum) when it was downloaded directly.`,
		Example: `  adaa update
  adaa update --check`,
		GroupID: groupSetup,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			rel, err := ui.Spin(a.IO, "Checking for a newer version…", func() (*update.Release, error) {
				return update.Latest(ctx)
			})
			if err != nil {
				return fmt.Errorf("could not check for updates: %w", err)
			}
			if !update.Newer(rel.Version(), build.Version) && build.IsRelease() {
				a.IO.Successf("adaa %s is the latest version.", build.Version)
				return nil
			}
			method, exe := update.Detect()
			if check {
				a.IO.Infof("adaa %s is available (you have %s).", rel.Version(), build.Version)
				a.IO.Hint("adaa update")
				return nil
			}
			return a.upgrade(ctx, rel, method, exe)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "Only report whether an update is available")
	return cmd
}

func (a *App) upgrade(ctx context.Context, rel *update.Release, method update.Method, exe string) error {
	switch method {
	case update.Homebrew, update.Scoop, update.GoInstall:
		argv := update.Command(method)
		a.IO.Infof("Updating with %s…", strings.Join(argv, " "))
		if err := update.Run(ctx, method, a.IO.Err, a.IO.Err); err != nil {
			return fmt.Errorf("%s failed: %w", argv[0], err)
		}
	case update.Package:
		return fmt.Errorf("adaa was installed by the system package manager at %s; download the new package from %s", exe, rel.HTMLURL)
	default:
		if _, err := ui.Spin(a.IO, "Downloading adaa "+rel.Version()+"…", func() (struct{}, error) {
			return struct{}{}, update.SelfReplace(ctx, rel, exe)
		}); err != nil {
			return err
		}
	}
	a.IO.Successf("Updated to adaa %s.", rel.Version())
	a.IO.Hint("adaa doctor --fix   (brings the agent skill up to date too)")
	return nil
}

// scheduleUpdateNotice checks for a new release at most once a day, in the
// background, and mentions it after the command has finished. It never delays
// a command and never prints into output a script is reading.
func (a *App) scheduleUpdateNotice(cmd *cobra.Command) {
	switch cmd.Name() {
	case "update", "doctor", "completion", "__complete", "__completeNoDesc", "version":
		return
	}
	if !a.IO.ErrTTY || !a.IO.OutTTY || a.JSON || os.Getenv("ADAA_NO_UPDATE_NOTIFIER") != "" || os.Getenv("CI") != "" || !build.IsRelease() {
		return
	}
	state := config.LoadState()
	done := make(chan struct{})
	if time.Since(state.UpdateCheckedAt) > 24*time.Hour {
		go func() {
			defer close(done)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			rel, err := update.Latest(ctx)
			state.UpdateCheckedAt = time.Now()
			if err == nil {
				state.LatestVersion = rel.Version()
			}
			_ = state.Save()
		}()
	} else {
		close(done)
	}
	a.afterRun = append(a.afterRun, func() {
		select {
		case <-done:
		case <-time.After(200 * time.Millisecond):
			return
		}
		if state.LatestVersion != "" && update.Newer(state.LatestVersion, build.Version) {
			fmt.Fprintln(a.IO.Err)
			fmt.Fprintf(a.IO.Err, "%s adaa %s is available (you have %s). Run %s.\n",
				a.IO.E().Yellow("↑"), state.LatestVersion, build.Version, a.IO.E().Bold("adaa update"))
		}
	})
}
