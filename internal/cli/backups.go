package cli

import (
	"fmt"
	"net/http"
	"time"

	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() { register(newBackupsCmd) }

// restoreTestStaleAfter is how old a passed restore test may get before it
// stops being reassuring.
const restoreTestStaleAfter = 90 * 24 * time.Hour

func newBackupsCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "backups",
		Aliases: []string{"backup"},
		Short:   "Backup jobs, restore tests and restores",
		Long: `Backup jobs, and whether they can actually be restored. A backup that has never
been restore-tested reports "unknown" rather than ok.`,
		GroupID: groupProducts,
	}
	cmd.AddCommand(newBackupsListCmd(a), newBackupsViewCmd(a), newBackupsTestRestoreCmd(a), newBackupsRestoreCmd(a))
	return cmd
}

func newBackupsListCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List backup jobs",
		Example: "  adaa backups list",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			path, err := a.OrgPath(ctx, "/backups")
			if err != nil {
				return err
			}
			l, err := a.ListItems(ctx, path, nil)
			if err != nil {
				return err
			}
			return a.PrintListing(l, "No backup jobs.", []string{"Name", "Status", "Last success", "Restore test", "Schedule", "ID"},
				func(b obj.Obj) []string {
					last := a.IO.When(b.Str("last_success_at"))
					if last == "" {
						last = a.IO.S().Red("never")
					}
					return []string{b.Str("name"), a.status(b.Str("status")), last, a.restoreTestShort(b),
						b.Str("schedule_description"), a.dim(b.Str("id"))}
				})
		},
	}
}

func (a *App) restoreTestShort(b obj.Obj) string {
	at := b.Str("last_restore_test_at")
	if at == "" {
		return a.IO.S().Yellow("never tested")
	}
	s := a.status(b.Str("last_restore_test_status")) + " " + a.IO.When(at)
	if t, err := time.Parse(time.RFC3339, at); err == nil && time.Since(t) > restoreTestStaleAfter {
		s += " " + a.IO.S().Yellow("(stale)")
	}
	return s
}

func newBackupsViewCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "view [backup]",
		Short:             "Show a backup job",
		Example:           "  adaa backups view \"File server\"\n  adaa backups view bkp_01JATX3M4K7Q2YV8N0RCBEZ5HS --json",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindBackup),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindBackup, arg(args))
			if err != nil {
				return err
			}
			b, raw, err := a.Get(ctx, "/backups/"+id, nil)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, b, func(b obj.Obj) {
				d := a.IO.NewDetail(b.Str("name"), b.Str("id"))
				d.Field("Status", a.status(b.Str("status")))
				d.Field("Summary", b.Str("summary"))
				d.Field("Schedule", b.Str("schedule_description"))
				d.Field("Destination", b.Str("destination"))
				if b.Bool("append_only") {
					d.Field("Append-only", a.IO.S().Green("yes")+a.dim(" — the machine cannot delete its own backups"))
				} else {
					d.Field("Append-only", a.IO.S().Yellow("no")+a.dim(" — ransomware on the machine could reach its backups"))
				}
				if n, ok := b.Int("retention_days"); ok {
					d.Field("Kept for", fmt.Sprintf("%d days", n))
				}
				d.Field("Target", join(" ", b.Str("target_kind"), a.dim(b.Str("target_id"))))
				d.Section("Runs")
				d.Field("Last run", a.IO.When(b.Str("last_run_at")))
				d.Field("Last success", orNone(a.IO.When(b.Str("last_success_at")), a.IO.S().Red("never")))
				if n, ok := b.Int("last_size_bytes"); ok {
					d.Field("Size", ui.Bytes(n))
				}
				d.Field("Next run", a.IO.When(b.Str("next_run_at")))
				d.Section("Restore test")
				d.Field("Result", a.restoreTestShort(b))
				d.Render()
				if b.Str("last_restore_test_at") == "" || b.Str("last_restore_test_status") == "failed" {
					a.IO.Hint("adaa backups test-restore %s", b.Str("id"))
				}
			})
		},
	}
}

func newBackupsTestRestoreCmd(a *App) *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:   "test-restore [backup]",
		Short: "Prove a backup can be restored, without touching live data",
		Long: `Restore the latest backup into a scratch location and verify it. Nothing live
is touched. Tests also run on a schedule; this runs one now.`,
		Example:           "  adaa backups test-restore \"File server\" --wait",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindBackup),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindBackup, arg(args))
			if err != nil {
				return err
			}
			if err := a.Confirm("Run a restore test now? It restores into a scratch location and touches nothing live."); err != nil {
				return err
			}
			resp, o, err := a.Send(ctx, http.MethodPost, "/backups/"+id+"/restore-test", map[string]any{})
			if err != nil {
				return err
			}
			return a.Accepted(ctx, resp, o, wait)
		},
	}
	addWaitFlag(cmd, &wait)
	return cmd
}

func newBackupsRestoreCmd(a *App) *cobra.Command {
	var at, reason string
	var paths []string
	var dryRun, wait bool
	cmd := &cobra.Command{
		Use:   "restore [backup]",
		Short: "Restore live data from a backup",
		Long: `Write data from a backup back over live data. This overwrites what is there
now, so it always waits for approval, whoever asks. Use --dry-run to see
exactly what would be written back and from which point in time.`,
		Example: `  adaa backups restore "File server" --at 2026-09-20T22:00:00Z --path /srv/shares/finance --dry-run
  adaa backups restore bkp_01JATX3M4K7Q2YV8N0RCBEZ5HS --at 2026-09-20T22:00:00Z --reason "Deleted by mistake" --yes`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindBackup),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := a.need(&at, "Restore to which point in time? (RFC 3339, e.g. 2026-09-20T22:00:00Z)", "--at", backupPointInTime); err != nil {
				return err
			}
			t, _ := time.Parse(time.RFC3339, at)
			id, err := a.Resolve(ctx, kindBackup, arg(args))
			if err != nil {
				return err
			}
			body := map[string]any{"point_in_time": t.UTC().Format(time.RFC3339)}
			if len(paths) > 0 {
				body["paths"] = paths
			}
			if reason != "" {
				body["reason"] = reason
			}
			what := "everything in this backup"
			if len(paths) > 0 {
				what = fmt.Sprintf("%d path(s)", len(paths))
			}
			resp, o, err := a.Apply(ctx, Change{Method: http.MethodPost, Path: "/backups/" + id + "/restore", Body: body,
				Question: fmt.Sprintf("Overwrite live data with %s as it was %s?", what, t.Local().Format("2 Jan 2006 15:04")),
				DryRun:   dryRun})
			if err != nil || dryRun {
				return err
			}
			return a.Accepted(ctx, resp, o, wait)
		},
	}
	f := cmd.Flags()
	f.StringVar(&at, "at", "", "Point in time to restore, as an RFC 3339 time")
	f.StringArrayVar(&paths, "path", nil, "Restore only this path (repeatable; default: everything)")
	f.StringVar(&reason, "reason", "", "Why, for the approver and the activity log")
	addDryRunFlag(cmd, &dryRun)
	addWaitFlag(cmd, &wait)
	return cmd
}

func backupPointInTime(s string) error {
	if _, err := time.Parse(time.RFC3339, s); err != nil {
		return fmt.Errorf("want an RFC 3339 time such as 2026-09-20T22:00:00Z")
	}
	return nil
}
