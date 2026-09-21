package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() { register(newServersCmd) }

func newServersCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "servers",
		Aliases: []string{"server"},
		Short:   "Servers your people use: specs, access, restarts",
		Long: `Servers here are machines your people log into or run business software on.
Machines adaa runs behind a product, like the mail host, are not listed.`,
		GroupID: groupProducts,
	}
	cmd.AddCommand(
		newServersListCmd(a),
		newServersViewCmd(a),
		newServersOrderCmd(a),
		newServersEditCmd(a),
		newServersRestartCmd(a),
		newServersAccessCmd(a),
		newServersSSHCmd(a),
	)
	return cmd
}

func newServersListCmd(a *App) *cobra.Command {
	var lo ListOpts
	var status, platform string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List servers",
		Example: `  adaa servers list
  adaa servers list --platform linux --status running`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := oneOf("status", status, serverStatuses...); err != nil {
				return err
			}
			if err := oneOf("platform", platform, "windows", "linux"); err != nil {
				return err
			}
			q := url.Values{}
			setQuery(q, cmd, "status", "status", status)
			setQuery(q, cmd, "platform", "platform_kind", platform)
			path, err := a.OrgPath(ctx, "/servers")
			if err != nil {
				return err
			}
			l, err := a.List(ctx, path, q, lo)
			if err != nil {
				return err
			}
			return a.PrintListing(l, "No servers.", []string{"Hostname", "Status", "Platform", "Size", "Updates", "Purpose", "ID"},
				func(s obj.Obj) []string {
					return []string{s.Str("hostname"), a.status(s.Str("status")), s.Str("platform_kind"),
						serverSize(s), a.maintenanceShort(s.Obj("maintenance")), s.Str("purpose"), a.dim(s.Str("id"))}
				})
		},
	}
	addListFlags(cmd, &lo)
	cmd.Flags().StringVar(&status, "status", "", "Only this status: "+strings.Join(serverStatuses, ", "))
	cmd.Flags().StringVar(&platform, "platform", "", "Only windows or linux")
	enumFlag(cmd, "status", serverStatuses...)
	enumFlag(cmd, "platform", "windows", "linux")
	return cmd
}

var serverStatuses = []string{"building", "running", "stopped", "restarting", "decommissioned"}

func serverSize(s obj.Obj) string {
	var parts []string
	if n, ok := s.Int("cpu_cores"); ok {
		parts = append(parts, fmt.Sprintf("%d vCPU", n))
	}
	if n, ok := s.Int("memory_mb"); ok {
		parts = append(parts, ui.Bytes(n<<20))
	}
	if n, ok := s.Int("storage_gb"); ok {
		parts = append(parts, fmt.Sprintf("%d GB", n))
	}
	return strings.Join(parts, " · ")
}

func (a *App) maintenanceShort(m obj.Obj) string {
	if m == nil {
		return ""
	}
	if m.Bool("up_to_date") {
		return a.status("up_to_date")
	}
	s := "updates pending"
	if n, ok := m.Int("updates_pending"); ok {
		s = fmt.Sprintf("%d pending", n)
	}
	if m.Bool("reboot_required") {
		s += ", reboot"
	}
	return a.IO.S().Yellow(s)
}

func newServersViewCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "view [server]",
		Short:             "Show a server",
		Example:           "  adaa servers view files01\n  adaa servers view srv_01JATX3M4K7Q2YV8N0RCBEZ5HS --json",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindServer),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindServer, arg(args))
			if err != nil {
				return err
			}
			s, raw, err := a.Get(ctx, "/servers/"+id, nil)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, s, a.renderServer)
		},
	}
}

func (a *App) renderServer(s obj.Obj) {
	p := a.IO.S()
	d := a.IO.NewDetail(s.Str("hostname"), s.Str("id"))
	d.Field("Status", a.status(s.Str("status")))
	d.Field("Purpose", s.Str("purpose"))
	d.Field("Platform", join(" ", s.Str("platform_kind"), s.Str("os_version")))
	if w := s.Obj("windows"); w != nil {
		d.Field("Edition", w.Str("edition"))
	}
	d.Field("IP address", s.Str("ip_address"))
	d.Field("Size", serverSize(s))
	if total, ok := s.Int("storage_gb"); ok && total > 0 {
		if used, ok := s.Int("storage_used_gb"); ok {
			pct := used * 100 / total
			txt := fmt.Sprintf("%d of %d GB (%d%%)", used, total, pct)
			switch {
			case pct >= 90:
				txt = p.Red(txt)
			case pct >= 75:
				txt = p.Yellow(txt)
			}
			d.Field("Disk used", txt)
		}
	}
	if n, ok := s.Int("access_count"); ok {
		d.Field("Access", fmt.Sprintf("%d people", n))
	}
	d.Field("Backup job", s.Str("backup_job_id"))
	d.Field("Subscription", s.Str("subscription_id"))
	if m := s.Obj("maintenance"); m != nil {
		d.Section("Maintenance")
		d.Field("Software", a.maintenanceShort(m))
		if m.Bool("reboot_required") {
			d.Field("Reboot", p.Yellow("needed to apply updates"))
		}
		d.Field("Last updated", a.IO.When(m.Str("last_updated_at")))
		d.Field("Next window", a.IO.When(m.Str("next_maintenance_at")))
	}
	d.Render()
}

func newServersOrderCmd(a *App) *cobra.Command {
	var hostname, platform, osVersion, purpose string
	var cores, memoryGB, storageGB int
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "order",
		Short: "Order a new server",
		Long: `Order a new server. You see what it costs before anything is ordered.

Say what the server is for with --purpose, even when it seems obvious.`,
		Example: `  adaa servers order
  adaa servers order --hostname files01 --platform linux --cores 2 --memory 4 --storage 200 --purpose "File shares" --dry-run
  adaa servers order --hostname erp01 --platform windows --cores 4 --memory 16 --storage 500 --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := oneOf("platform", platform, "windows", "linux"); err != nil {
				return err
			}
			missing := hostname == "" || platform == "" || cores == 0 || memoryGB == 0 || storageGB == 0
			if missing {
				if !a.IO.Interactive() {
					return usagef("--hostname, --platform, --cores, --memory and --storage are required")
				}
				if err := a.serverOrderForm(&hostname, &platform, &osVersion, &purpose, &cores, &memoryGB, &storageGB); err != nil {
					return err
				}
			}
			body := map[string]any{
				"hostname":      hostname,
				"platform_kind": platform,
				"cpu_cores":     cores,
				"memory_mb":     memoryGB * 1024,
				"storage_gb":    storageGB,
			}
			if osVersion != "" {
				body["os_version"] = osVersion
			}
			if purpose != "" {
				body["purpose"] = purpose
			}
			path, err := a.OrgPath(ctx, "/servers")
			if err != nil {
				return err
			}
			resp, o, err := a.Apply(ctx, Change{Method: http.MethodPost, Path: path, Body: body,
				Question: "Order " + hostname + "?", DryRun: dryRun})
			if err != nil || dryRun {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.ReportResult(o)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&hostname, "hostname", "", "Hostname")
	f.StringVar(&platform, "platform", "", "Platform: windows or linux")
	f.StringVar(&osVersion, "os", "", "Operating system version, e.g. \"Ubuntu 24.04\"")
	f.StringVar(&purpose, "purpose", "", "What the server is for")
	f.IntVar(&cores, "cores", 0, "Number of vCPUs")
	f.IntVar(&memoryGB, "memory", 0, "Memory in GB")
	f.IntVar(&storageGB, "storage", 0, "Disk in GB")
	addDryRunFlag(cmd, &dryRun)
	enumFlag(cmd, "platform", "windows", "linux")
	return cmd
}

func (a *App) serverOrderForm(hostname, platform, osVersion, purpose *string, cores, memoryGB, storageGB *int) error {
	if *platform == "" {
		*platform = "linux"
	}
	coresS, memS, diskS := intOr(*cores, 2), intOr(*memoryGB, 4), intOr(*storageGB, 100)
	positive := func(s string) error {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err != nil || n < 1 {
			return errors.New("enter a whole number above zero")
		}
		return nil
	}
	required := func(s string) error {
		if strings.TrimSpace(s) == "" {
			return errors.New("required")
		}
		return nil
	}
	err := a.IO.Run(
		huh.NewGroup(
			huh.NewInput().Title("Hostname").Value(hostname).Validate(required),
			huh.NewInput().Title("What is it for?").Description("Say it even if it seems obvious; in a year it will not be.").Value(purpose),
			huh.NewSelect[string]().Title("Platform").Description("Usually decided by the software it will run.").
				Options(huh.NewOption("Linux", "linux"), huh.NewOption("Windows Server", "windows")).Value(platform),
			huh.NewInput().Title("Operating system version (optional)").Value(osVersion),
		),
		huh.NewGroup(
			huh.NewInput().Title("vCPUs").Value(&coresS).Validate(positive),
			huh.NewInput().Title("Memory (GB)").Value(&memS).Validate(positive),
			huh.NewInput().Title("Disk (GB)").Value(&diskS).Validate(positive),
		),
	)
	if err != nil {
		return err
	}
	*cores, _ = strconv.Atoi(strings.TrimSpace(coresS))
	*memoryGB, _ = strconv.Atoi(strings.TrimSpace(memS))
	*storageGB, _ = strconv.Atoi(strings.TrimSpace(diskS))
	return nil
}

func intOr(v, def int) string {
	if v == 0 {
		v = def
	}
	return strconv.Itoa(v)
}

func newServersEditCmd(a *App) *cobra.Command {
	var hostname, purpose string
	var cores, memoryGB, storageGB int
	var dryRun, wait bool
	cmd := &cobra.Command{
		Use:   "edit [server]",
		Short: "Rename a server or change its size",
		Long: `Change a server's hostname, purpose or size. Resizing is work, not a field
update, so it is previewed with its cost and runs as a task. Disks can grow but
never shrink.`,
		Example: `  adaa servers edit files01 --purpose "File shares and scans"
  adaa servers edit files01 --memory 8 --storage 400 --dry-run`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindServer),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			body := fields{}
			body.str(cmd, "hostname", "hostname", hostname)
			body.str(cmd, "purpose", "purpose", purpose)
			body.integer(cmd, "cores", "cpu_cores", cores)
			if cmd.Flags().Changed("memory") {
				body["memory_mb"] = memoryGB * 1024
			}
			body.integer(cmd, "storage", "storage_gb", storageGB)
			if len(body) == 0 {
				return usagef("nothing to change; pass --hostname, --purpose, --cores, --memory or --storage")
			}
			id, err := a.Resolve(ctx, kindServer, arg(args))
			if err != nil {
				return err
			}
			resp, o, err := a.Apply(ctx, Change{Method: http.MethodPatch, Path: "/servers/" + id, Body: body,
				Question: "Apply these changes?", DryRun: dryRun})
			if err != nil || dryRun {
				return err
			}
			if wait {
				for _, t := range o.List("tasks") {
					if _, _, err := a.WaitTask(ctx, t.Str("id")); err != nil {
						return err
					}
				}
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.ReportResult(o)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&hostname, "hostname", "", "New hostname")
	f.StringVar(&purpose, "purpose", "", "What the server is for")
	f.IntVar(&cores, "cores", 0, "Number of vCPUs")
	f.IntVar(&memoryGB, "memory", 0, "Memory in GB")
	f.IntVar(&storageGB, "storage", 0, "Disk in GB (can only grow)")
	addDryRunFlag(cmd, &dryRun)
	addWaitFlag(cmd, &wait)
	return cmd
}

func newServersRestartCmd(a *App) *cobra.Command {
	var at, reason string
	var wait bool
	cmd := &cobra.Command{
		Use:   "restart [server]",
		Short: "Restart a server, now or at a set time",
		Long: `Restart a server. Everyone working on it is interrupted, so pick a time with
--at unless it has to happen now.`,
		Example: `  adaa servers restart files01 --at 2026-09-22T05:00:00Z --reason "Apply updates"
  adaa servers restart files01 --yes --wait`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindServer),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			body := map[string]any{}
			when := "now"
			if at != "" {
				t, err := time.Parse(time.RFC3339, at)
				if err != nil {
					return usagef("--at wants an RFC 3339 time such as 2026-09-22T05:00:00Z")
				}
				body["scheduled_for"] = t.UTC().Format(time.RFC3339)
				when = "at " + t.Local().Format("2 Jan 2006 15:04")
			}
			if reason != "" {
				body["reason"] = reason
			}
			id, err := a.Resolve(ctx, kindServer, arg(args))
			if err != nil {
				return err
			}
			q := "Restart the server " + when + "? Anyone working on it will be interrupted."
			if err := a.Confirm(q); err != nil {
				return err
			}
			resp, o, err := a.Send(ctx, http.MethodPost, "/servers/"+id+"/restart", body)
			if err != nil {
				return err
			}
			return a.Accepted(ctx, resp, o, wait)
		},
	}
	cmd.Flags().StringVar(&at, "at", "", "When to restart, as an RFC 3339 time (default: now)")
	cmd.Flags().StringVar(&reason, "reason", "", "Why, for the activity log")
	addWaitFlag(cmd, &wait)
	return cmd
}

func newServersAccessCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "access",
		Short: "Who can log into a server",
	}
	cmd.AddCommand(newServersAccessListCmd(a), newServersAccessGrantCmd(a), newServersAccessRevokeCmd(a))
	return cmd
}

func newServersAccessListCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "list [server]",
		Aliases:           []string{"ls"},
		Short:             "List who has access to a server",
		Example:           "  adaa servers access list files01",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindServer),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindServer, arg(args))
			if err != nil {
				return err
			}
			l, err := a.ListItems(ctx, "/servers/"+id+"/access", nil)
			if err != nil {
				return err
			}
			return a.PrintListing(l, "Nobody has access to this server.", []string{"Person", "Level", "Status", "Last login", "Person ID"},
				func(o obj.Obj) []string {
					last := a.IO.When(o.Str("last_login_at"))
					if last == "" {
						last = a.dim("never")
					}
					return []string{o.Str("person_name"), strings.ReplaceAll(o.Str("level"), "_", " "),
						a.status(o.Str("status")), last, a.dim(o.Str("person_id"))}
				})
		},
	}
}

func newServersAccessGrantCmd(a *App) *cobra.Command {
	var level string
	cmd := &cobra.Command{
		Use:   "grant <server> <person>",
		Short: "Give a person access to a server",
		Example: `  adaa servers access grant files01 kari@firma.no
  adaa servers access grant erp01 ola@firma.no --level administrator`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := oneOf("level", level, "read_only", "standard", "administrator"); err != nil {
				return err
			}
			id, err := a.Resolve(ctx, kindServer, args[0])
			if err != nil {
				return err
			}
			person, err := a.Resolve(ctx, kindPerson, args[1])
			if err != nil {
				return err
			}
			resp, o, err := a.Send(ctx, http.MethodPost, "/servers/"+id+"/access",
				map[string]any{"person_id": person, "level": level})
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			name := o.Str("person_name")
			if name == "" {
				name = person
			}
			a.IO.Successf("Granting %s %s access.", name, strings.ReplaceAll(level, "_", " "))
			return nil
		},
	}
	cmd.Flags().StringVar(&level, "level", "standard", "Access level: read_only, standard or administrator")
	enumFlag(cmd, "level", "read_only", "standard", "administrator")
	return cmd
}

func newServersAccessRevokeCmd(a *App) *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:     "revoke <server> <person>",
		Short:   "Take away a person's access to a server",
		Long:    "Revoking is destructive, so it waits for approval before it runs.",
		Example: "  adaa servers access revoke files01 ola@firma.no",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindServer, args[0])
			if err != nil {
				return err
			}
			person, err := a.Resolve(ctx, kindPerson, args[1])
			if err != nil {
				return err
			}
			if err := a.Confirm("Revoke this person's access to the server?"); err != nil {
				return err
			}
			resp, o, err := a.Send(ctx, http.MethodDelete, "/servers/"+id+"/access/"+person, nil)
			if err != nil {
				return err
			}
			return a.Accepted(ctx, resp, o, wait)
		},
	}
	addWaitFlag(cmd, &wait)
	return cmd
}

func newServersSSHCmd(a *App) *cobra.Command {
	var user string
	cmd := &cobra.Command{
		Use:   "ssh [server] [-- ssh-args...]",
		Short: "Connect to a Linux server with ssh (or get the RDP address for Windows)",
		Long: `Connect to a server using your own ssh client. adaa checks that you have been
granted access first, so a refused login is not a mystery. Windows servers are
reached with Remote Desktop instead; for those this prints the address.`,
		Example: `  adaa servers ssh files01
  adaa servers ssh files01 --user kari -- -L 8080:localhost:80`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: a.complete(kindServer),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			var extra []string
			if n := cmd.ArgsLenAtDash(); n >= 0 {
				extra = args[n:]
				args = args[:n]
			}
			if len(args) > 1 {
				return usagef("pass extra ssh arguments after --")
			}
			id, err := a.Resolve(ctx, kindServer, arg(args))
			if err != nil {
				return err
			}
			s, _, err := a.Get(ctx, "/servers/"+id, nil)
			if err != nil {
				return err
			}
			host := s.Str("ip_address")
			if host == "" {
				host = s.Str("hostname")
			}
			if s.Str("status") != "running" {
				a.IO.Warnf("%s is %s.", s.Str("hostname"), strings.ReplaceAll(s.Str("status"), "_", " "))
			}
			if err := a.checkServerAccess(ctx, id); err != nil {
				return err
			}
			if s.Str("platform_kind") == "windows" {
				a.IO.Infof("%s is a Windows server; connect with Remote Desktop to %s.", s.Str("hostname"), a.IO.E().Bold(host))
				switch runtime.GOOS {
				case "windows":
					a.IO.Hint("mstsc /v:%s", host)
				case "darwin":
					a.IO.Hint("open 'rdp://full%%20address=s:%s'", host)
				}
				return nil
			}
			sshPath, err := exec.LookPath("ssh")
			if err != nil {
				return errors.New("no ssh client found on this computer; install OpenSSH and try again")
			}
			target := host
			if user != "" {
				target = user + "@" + host
			}
			argv := append([]string{"ssh", target}, extra...)
			a.IO.Infof("%s", a.IO.E().Dim("$ "+strings.Join(argv, " ")))
			return execSSH(ctx, sshPath, argv)
		},
	}
	cmd.Flags().StringVarP(&user, "user", "l", "", "Remote user name (default: your ssh config)")
	return cmd
}

// checkServerAccess fails early when the API says the caller has no access,
// instead of leaving them at a password prompt that can never succeed.
func (a *App) checkServerAccess(ctx context.Context, serverID string) error {
	me, err := a.Identity(ctx)
	if err != nil {
		return err
	}
	l, err := a.ListItems(ctx, "/servers/"+serverID+"/access", nil)
	if err != nil {
		// Reading the list needs its own permission; not having it says
		// nothing about whether this person can log in.
		return nil
	}
	for _, it := range l.Items {
		if it.Str("person_id") == me.Str("person.id") {
			if it.Str("status") == "granting" {
				a.IO.Warnf("Your access is still being set up.")
			}
			return nil
		}
	}
	return fmt.Errorf("you have not been granted access to this server; an admin can run `adaa servers access grant %s %s`",
		serverID, me.Str("person.email"))
}
