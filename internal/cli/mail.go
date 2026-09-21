package cli

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/getadaa/cli/internal/api"
	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() { register(newMailCmd) }

const mailGiB = 1 << 30

var migrationStatuses = []string{"pending", "running", "paused", "syncing", "cutover_pending", "verifying", "completed", "failed", "cancelled"}

func newMailCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mail",
		Short: "adaa's email service: mailboxes, migrations and deliverability",
		Long: `adaa's own email service. Email and only email: no calendar, shared files or
chat. A company on Microsoft 365 or Google Workspace gets its mail there
instead; see 'adaa workspaces'.`,
		GroupID: groupProducts,
	}
	boxes := &cobra.Command{Use: "mailboxes", Aliases: []string{"mailbox"}, Short: "Personal and shared mailboxes"}
	boxes.AddCommand(newMailboxesListCmd(a), newMailboxesViewCmd(a), newMailboxesAddCmd(a),
		newMailboxesEditCmd(a), newMailboxesArchiveCmd(a))
	cmd.AddCommand(newMailViewCmd(a), newMailSetupCmd(a), boxes, newMailMigrationsCmd(a),
		newMailMigrationCmd(a), newMailDeliverabilityCmd(a))
	return cmd
}

func newMailViewCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:     "view",
		Short:   "Show the mail service and how well its mail is delivered",
		Example: `  adaa mail view`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			path, err := a.OrgPath(ctx, "/mail")
			if err != nil {
				return err
			}
			m, raw, err := a.Get(ctx, path, nil)
			if err != nil {
				var p *api.Problem
				if errors.As(err, &p) && p.Status == http.StatusNotFound {
					a.IO.Infof("There is no mail service yet.")
					a.IO.Hint("adaa mail setup --domain <your domain>")
					return nil
				}
				return err
			}
			if a.JSON {
				return a.PrintJSON(raw)
			}
			v := a.IO.NewDetail("Mail", "")
			v.Field("Status", a.status(m.Str("status")))
			v.Field("Mailboxes", m.Str("mailbox_count"))
			if used, ok := m.Int("storage_used_bytes"); ok {
				s := ui.Bytes(used)
				if q, ok := m.Int("storage_quota_bytes"); ok && q > 0 {
					s += " of " + ui.Bytes(q)
				}
				v.Field("Storage", s)
			}
			v.Field("Hosted in", m.Str("hosting_region"))
			v.Field("Includes", strings.Join(m.Strings("capabilities"), ", "))
			v.Field("Not included", strings.Join(m.Strings("not_included"), "; "))
			if mt := m.Obj("maintenance"); mt != nil {
				state := a.IO.S().Green("up to date")
				if !mt.Bool("up_to_date") {
					state = a.IO.S().Yellow(mt.Str("updates_pending") + " updates pending")
				}
				if mt.Bool("reboot_required") {
					state += ", restart needed"
				}
				v.Field("Software", state)
				v.Field("Next maintenance", a.IO.When(mt.Str("next_maintenance_at")))
			}
			if dpath, err := a.OrgPath(ctx, "/mail/deliverability"); err == nil {
				if d, _, err := a.Get(ctx, dpath, nil); err == nil {
					v.Section("Deliverability")
					v.Field("Status", a.status(d.Str("status")))
					v.Field("Summary", d.Str("summary"))
				}
			}
			v.Render()
			a.IO.Hint("adaa mail mailboxes list")
			return nil
		},
	}
}

func newMailSetupCmd(a *App) *cobra.Command {
	var domain string
	var quotaGB int
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Set up the mail service",
		Long: `Build the mail service for a domain. You see what it will do and cost first.
The domain's DNS records are handled in 'adaa domains'.`,
		Example: `  adaa mail setup --domain firma.no
  adaa mail setup --domain firma.no --default-quota-gb 50 --dry-run`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.need(&domain, "Primary mail domain", "--domain", nil); err != nil {
				return err
			}
			body := map[string]any{"primary_domain": domain}
			if cmd.Flags().Changed("default-quota-gb") {
				body["default_quota_bytes"] = int64(quotaGB) * mailGiB
			}
			path, err := a.OrgPath(cmd.Context(), "/mail")
			if err != nil {
				return err
			}
			resp, res, err := a.Apply(cmd.Context(), Change{Method: http.MethodPost, Path: path, Body: body,
				Question: "Set up mail for " + domain + "?", DryRun: dryRun})
			if err != nil || dryRun {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.ReportResult(res)
			a.IO.Hint("adaa domains check %s   (once the records are published)", domain)
			return nil
		},
	}
	cmd.Flags().StringVar(&domain, "domain", "", "The primary domain mail is received for")
	cmd.Flags().IntVar(&quotaGB, "default-quota-gb", 0, "Default mailbox size, in GiB")
	addDryRunFlag(cmd, &dryRun)
	return cmd
}

func newMailboxesListCmd(a *App) *cobra.Command {
	var lo ListOpts
	var kind string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List mailboxes",
		Example: `  adaa mail mailboxes list
  adaa mail mailboxes list --kind shared`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := oneOf("kind", kind, "personal", "shared"); err != nil {
				return err
			}
			path, err := a.OrgPath(cmd.Context(), "/mail/mailboxes")
			if err != nil {
				return err
			}
			q := url.Values{}
			setQuery(q, cmd, "kind", "kind", kind)
			l, err := a.List(cmd.Context(), path, q, lo)
			if err != nil {
				return err
			}
			return a.PrintListing(l, "No mailboxes yet. Create one with `adaa mail mailboxes add`.",
				[]string{"Address", "Kind", "Name", "Status", "Used", "ID"},
				func(m obj.Obj) []string {
					return []string{m.Str("address"), m.Str("kind"), m.Str("display_name"), a.status(m.Str("status")),
						mailUsage(m), a.dim(m.Str("id"))}
				})
		},
	}
	addListFlags(cmd, &lo)
	cmd.Flags().StringVar(&kind, "kind", "", "Only personal or shared mailboxes")
	enumFlag(cmd, "kind", "personal", "shared")
	return cmd
}

func mailUsage(m obj.Obj) string {
	used, ok := m.Int("used_bytes")
	if !ok {
		return ""
	}
	s := ui.Bytes(used)
	if q, ok := m.Int("quota_bytes"); ok && q > 0 {
		s += fmt.Sprintf(" of %s (%d%%)", ui.Bytes(q), used*100/q)
	}
	return s
}

func newMailboxesViewCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "view [mailbox]",
		Short:             "Show a mailbox",
		Example:           `  adaa mail mailboxes view post@firma.no`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindMailbox),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.Resolve(cmd.Context(), kindMailbox, arg(args))
			if err != nil {
				return err
			}
			m, raw, err := a.Get(cmd.Context(), "/mail/mailboxes/"+id, nil)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, m, func(m obj.Obj) {
				v := a.IO.NewDetail(m.Str("address"), m.Str("id"))
				v.Field("Name", m.Str("display_name"))
				v.Field("Kind", m.Str("kind"))
				v.Field("Status", a.status(m.Str("status")))
				v.Field("Owner", m.Str("person_id"))
				v.Field("Delegates", strings.Join(m.Strings("delegate_person_ids"), ", "))
				v.Field("Aliases", strings.Join(m.Strings("aliases"), ", "))
				if fw := m.Obj("forwarding"); fw != nil && len(fw.Strings("to")) > 0 {
					s := strings.Join(fw.Strings("to"), ", ")
					if fw.Has("keep_copy") && !fw.Bool("keep_copy") {
						s += " (no copy kept)"
					}
					v.Field("Forwards to", s)
				}
				v.Field("Used", mailUsage(m))
				v.Field("Migration", a.status(m.Str("migration_status")))
				v.Field("Created", a.IO.When(m.Str("created_at")))
				v.Render()
				if m.Str("migration_status") != "" {
					a.IO.Hint("adaa mail migration %s", m.Str("address"))
				}
			})
		},
	}
}

func newMailboxesAddCmd(a *App) *cobra.Command {
	var address, kind, name, person string
	var delegates []string
	var quotaGB int
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "add [address]",
		Short: "Create a mailbox",
		Long: `Create a mailbox. A personal mailbox belongs to one person; a shared one, like
post@firma.no, belongs to nobody and is opened by the people delegated to it.`,
		Example: `  adaa mail mailboxes add kari@firma.no --kind personal --person kari@firma.no
  adaa mail mailboxes add post@firma.no --kind shared --delegate kari@firma.no --delegate ola@firma.no`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if address == "" {
				address = arg(args)
			}
			if err := oneOf("kind", kind, "personal", "shared"); err != nil {
				return err
			}
			if err := a.need(&address, "Address", "the address as an argument", validEmail); err != nil {
				return err
			}
			if kind == "" {
				k, err := a.IO.Select("What kind of mailbox?", "--kind", []ui.Option{
					{Label: "Personal — belongs to one person", Value: "personal"},
					{Label: "Shared — like post@, opened by delegates", Value: "shared"},
				})
				if err != nil {
					return err
				}
				kind = k
			}
			body := map[string]any{"address": address, "kind": kind}
			if name != "" {
				body["display_name"] = name
			}
			if kind == "personal" {
				if person == "" && !a.IO.Interactive() {
					return usagef("a personal mailbox needs --person")
				}
				pid, err := a.Resolve(ctx, kindPerson, person)
				if err != nil {
					return err
				}
				body["person_id"] = pid
			} else if person != "" {
				return usagef("a shared mailbox belongs to nobody; use --delegate instead of --person")
			}
			if len(delegates) > 0 {
				ids := make([]string, 0, len(delegates))
				for _, d := range delegates {
					id, err := a.Resolve(ctx, kindPerson, d)
					if err != nil {
						return err
					}
					ids = append(ids, id)
				}
				body["delegate_person_ids"] = ids
			}
			if cmd.Flags().Changed("quota-gb") {
				body["quota_bytes"] = int64(quotaGB) * mailGiB
			}
			path, err := a.OrgPath(ctx, "/mail/mailboxes")
			if err != nil {
				return err
			}
			resp, m, err := a.Apply(ctx, Change{Method: http.MethodPost, Path: path, Body: body,
				Question: "Create " + address + "?", DryRun: dryRun})
			if err != nil || dryRun {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Created %s %s", m.Str("address"), a.IO.E().Dim(m.Str("id")))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&address, "address", "", "Email address")
	f.StringVar(&kind, "kind", "", "personal or shared")
	f.StringVar(&name, "name", "", "Display name")
	f.StringVar(&person, "person", "", "Owner of a personal mailbox (email, name or id)")
	f.StringArrayVar(&delegates, "delegate", nil, "Person who can open a shared mailbox (repeatable)")
	f.IntVar(&quotaGB, "quota-gb", 0, "Size limit, in GiB")
	addDryRunFlag(cmd, &dryRun)
	enumFlag(cmd, "kind", "personal", "shared")
	_ = cmd.RegisterFlagCompletionFunc("person", a.complete(kindPerson))
	_ = cmd.RegisterFlagCompletionFunc("delegate", a.complete(kindPerson))
	return cmd
}

func newMailboxesEditCmd(a *App) *cobra.Command {
	var name string
	var addAlias, removeAlias, addDelegate, removeDelegate, forwardTo []string
	var keepCopy, noForward bool
	var quotaGB int
	cmd := &cobra.Command{
		Use:   "edit [mailbox]",
		Short: "Change a mailbox's name, aliases, delegates, forwarding or size",
		Example: `  adaa mail mailboxes edit post@firma.no --add-delegate ola@firma.no
  adaa mail mailboxes edit kari@firma.no --add-alias k.nordmann@firma.no
  adaa mail mailboxes edit kari@firma.no --forward-to kari@newjob.no --keep-copy=false
  adaa mail mailboxes edit kari@firma.no --no-forward`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindMailbox),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindMailbox, arg(args))
			if err != nil {
				return err
			}
			body := fields{}
			body.str(cmd, "name", "display_name", name)
			if cmd.Flags().Changed("quota-gb") {
				body["quota_bytes"] = int64(quotaGB) * mailGiB
			}
			needCurrent := len(addAlias)+len(removeAlias)+len(addDelegate)+len(removeDelegate) > 0
			var cur obj.Obj
			if needCurrent {
				if cur, _, err = a.Get(ctx, "/mail/mailboxes/"+id, nil); err != nil {
					return err
				}
			}
			if len(addAlias)+len(removeAlias) > 0 {
				body["aliases"] = mailEditSet(cur.Strings("aliases"), addAlias, removeAlias)
			}
			if len(addDelegate)+len(removeDelegate) > 0 {
				resolve := func(in []string) ([]string, error) {
					out := make([]string, 0, len(in))
					for _, p := range in {
						pid, err := a.Resolve(ctx, kindPerson, p)
						if err != nil {
							return nil, err
						}
						out = append(out, pid)
					}
					return out, nil
				}
				add, err := resolve(addDelegate)
				if err != nil {
					return err
				}
				rm, err := resolve(removeDelegate)
				if err != nil {
					return err
				}
				body["delegate_person_ids"] = mailEditSet(cur.Strings("delegate_person_ids"), add, rm)
			}
			switch {
			case noForward:
				body["forwarding"] = nil
			case len(forwardTo) > 0:
				fw := map[string]any{"to": forwardTo}
				if cmd.Flags().Changed("keep-copy") {
					fw["keep_copy"] = keepCopy
				}
				body["forwarding"] = fw
			}
			if len(body) == 0 {
				return usagef("nothing to change; pass at least one flag (see --help)")
			}
			resp, m, err := a.Send(ctx, http.MethodPatch, "/mail/mailboxes/"+id, body)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Updated %s", m.Str("address"))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "Display name")
	f.StringArrayVar(&addAlias, "add-alias", nil, "Add an alias address (repeatable)")
	f.StringArrayVar(&removeAlias, "remove-alias", nil, "Remove an alias address (repeatable)")
	f.StringArrayVar(&addDelegate, "add-delegate", nil, "Let a person open this shared mailbox (repeatable)")
	f.StringArrayVar(&removeDelegate, "remove-delegate", nil, "Stop a person opening this shared mailbox (repeatable)")
	f.StringArrayVar(&forwardTo, "forward-to", nil, "Forward mail to this address (repeatable; replaces forwarding)")
	f.BoolVar(&keepCopy, "keep-copy", true, "Keep a copy of forwarded mail in the mailbox")
	f.BoolVar(&noForward, "no-forward", false, "Stop forwarding")
	f.IntVar(&quotaGB, "quota-gb", 0, "Size limit, in GiB")
	cmd.MarkFlagsMutuallyExclusive("forward-to", "no-forward")
	return cmd
}

// editSet applies additions and removals to a list, keeping its order.
func mailEditSet(cur, add, remove []string) []string {
	out := []string{}
	seen := map[string]bool{}
	drop := map[string]bool{}
	for _, r := range remove {
		drop[strings.ToLower(r)] = true
	}
	for _, v := range append(append([]string{}, cur...), add...) {
		k := strings.ToLower(v)
		if drop[k] || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, v)
	}
	return out
}

func newMailboxesArchiveCmd(a *App) *cobra.Command {
	var retainDays int
	var wait bool
	cmd := &cobra.Command{
		Use:   "archive [mailbox]",
		Short: "Archive a mailbox, keeping its mail for a while first",
		Long: `Archive a mailbox. This raises a task that waits for approval, and the mail is
kept for --retain-days before anything is deleted: somebody almost always
still needs it.`,
		Example: `  adaa mail mailboxes archive ola@firma.no
  adaa mail mailboxes archive ola@firma.no --retain-days 365 --yes`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindMailbox),
		RunE: func(cmd *cobra.Command, args []string) error {
			if retainDays < 0 || retainDays > 3650 {
				return usagef("--retain-days must be between 0 and 3650")
			}
			id, err := a.Resolve(cmd.Context(), kindMailbox, arg(args))
			if err != nil {
				return err
			}
			if err := a.Confirm(fmt.Sprintf("Archive this mailbox, keeping its mail for %d days?", retainDays)); err != nil {
				return err
			}
			resp, task, err := func() (*api.Response, obj.Obj, error) {
				r, err := a.Do(cmd.Context(), http.MethodDelete, "/mail/mailboxes/"+id,
					url.Values{"retain_days": {strconv.Itoa(retainDays)}}, nil)
				if err != nil {
					return nil, nil, err
				}
				o, err := obj.Parse(r.Body)
				return r, o, err
			}()
			if err != nil {
				return err
			}
			return a.Accepted(cmd.Context(), resp, task, wait)
		},
	}
	cmd.Flags().IntVar(&retainDays, "retain-days", 90, "Days to keep the mail before deleting it")
	addWaitFlag(cmd, &wait)
	return cmd
}

func newMailMigrationsCmd(a *App) *cobra.Command {
	var status string
	cmd := &cobra.Command{
		Use:   "migrations",
		Short: "How far along moving mail from the old host is",
		Example: `  adaa mail migrations
  adaa mail migrations --status failed`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := oneOf("status", status, migrationStatuses...); err != nil {
				return err
			}
			path, err := a.OrgPath(cmd.Context(), "/mail/migrations")
			if err != nil {
				return err
			}
			q := url.Values{}
			setQuery(q, cmd, "status", "status", status)
			o, raw, err := a.Get(cmd.Context(), path, q)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(raw)
			}
			s := a.IO.S()
			v := a.IO.NewDetail("Mail migration", "")
			v.Field("Status", a.status(o.Str("status")))
			v.Field("Summary", o.Str("summary"))
			boxes := fmt.Sprintf("%s of %s done", orNone(o.Str("mailboxes_completed"), "0"), o.Str("mailboxes_total"))
			if n, _ := o.Int("mailboxes_running"); n > 0 {
				boxes += fmt.Sprintf(", %d running", n)
			}
			if n, _ := o.Int("mailboxes_failed"); n > 0 {
				boxes += ", " + s.Red(fmt.Sprintf("%d failed", n))
			}
			v.Field("Mailboxes", boxes)
			v.Field("Messages", migrationCounts(s, o))
			v.Field("Done", migrationPercent(o))
			v.Field("Expected done", a.IO.When(o.Str("estimated_completion_at")))
			v.Render()
			items := o.List("items")
			if len(items) == 0 {
				return nil
			}
			fmt.Fprintln(a.IO.Out)
			t := a.IO.NewTable("Address", "Status", "Progress", "Messages", "Last report")
			for _, m := range items {
				t.Row(m.Str("address"), a.status(m.Str("status")), migrationPercent(m), migrationCounts(s, m), a.IO.When(m.Str("last_progress_at")))
			}
			t.Render()
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "Only migrations in this state")
	enumFlag(cmd, "status", migrationStatuses...)
	return cmd
}

// migrationCounts always shows failures next to the totals, so a migration
// that "finished" with messages it could not copy never reads as complete.
func migrationCounts(s ui.Palette, m obj.Obj) string {
	if !m.Has("messages_total") && !m.Has("messages_copied") {
		return ""
	}
	out := fmt.Sprintf("%s of %s copied", orNone(m.Str("messages_copied"), "0"), orNone(m.Str("messages_total"), "?"))
	if n, _ := m.Int("messages_failed"); n > 0 {
		out += ", " + s.Red(fmt.Sprintf("%d failed", n))
	}
	return out
}

func migrationPercent(m obj.Obj) string {
	p := m.Str("percent_complete")
	if p == "" {
		return ""
	}
	if f, err := strconv.ParseFloat(p, 64); err == nil {
		return fmt.Sprintf("%.0f%%", f)
	}
	return p + "%"
}

func newMailMigrationCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "migration [mailbox]",
		Short:             "Show one mailbox's migration in detail",
		Example:           `  adaa mail migration kari@firma.no`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindMailbox),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.Resolve(cmd.Context(), kindMailbox, arg(args))
			if err != nil {
				return err
			}
			m, raw, err := a.Get(cmd.Context(), "/mail/mailboxes/"+id+"/migration", nil)
			if err != nil {
				var p *api.Problem
				if errors.As(err, &p) && p.Status == http.StatusNotFound {
					a.IO.Infof("No migration is recorded for this mailbox.")
					return nil
				}
				return err
			}
			return a.PrintObj(raw, m, func(m obj.Obj) {
				s := a.IO.S()
				v := a.IO.NewDetail(m.Str("address"), m.Str("id"))
				v.Field("Status", a.status(m.Str("status")))
				v.Field("Summary", m.Str("summary"))
				v.Field("From", join(" ", m.Str("source"), m.Str("source_host"), m.Str("source_username")))
				v.Field("Progress", migrationPercent(m))
				v.Field("Messages", migrationCounts(s, m))
				if n, _ := m.Int("messages_skipped"); n > 0 {
					v.Field("Skipped", fmt.Sprintf("%d (already there, or excluded)", n))
				}
				if bt, ok := m.Int("bytes_total"); ok {
					bc, _ := m.Int("bytes_copied")
					v.Field("Data", ui.Bytes(bc)+" of "+ui.Bytes(bt))
				}
				if ft := m.Str("folders_total"); ft != "" {
					v.Field("Folders", orNone(m.Str("folders_completed"), "0")+" of "+ft)
				}
				v.Field("Working on", m.Str("current_folder"))
				v.Field("Speed", join(" ", m.Str("messages_per_second"), "messages/s"))
				v.Field("Last report", a.IO.When(m.Str("last_progress_at")))
				v.Field("Expected done", a.IO.When(m.Str("estimated_completion_at")))
				v.Field("Cut over", a.IO.When(m.Str("cutover_at")))
				v.Field("Reporter", m.Str("reporter"))
				v.Field("Error", s.Red(join(": ", m.Str("error_code"), m.Str("error"))))
				if ws := m.Strings("warnings"); len(ws) > 0 {
					v.Section("Warnings")
					for _, w := range ws {
						v.Line(s.Yellow("! ") + w)
					}
				}
				v.Render()
				if folders := m.List("folders"); len(folders) > 0 {
					fmt.Fprintln(a.IO.Out)
					t := a.IO.NewTable("Folder", "Status", "Messages")
					for _, f := range folders {
						t.Row(f.Str("name"), a.status(f.Str("status")), migrationCounts(s, f))
					}
					t.Render()
				}
			})
		},
	}
}

func newMailDeliverabilityCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "deliverability",
		Short: "Whether mail from the company reaches inboxes",
		Long: `Whether the company's mail lands in inboxes: reverse DNS, public blocklists,
and how much of it passes DMARC at the receiving end.`,
		Example: `  adaa mail deliverability`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := a.OrgPath(cmd.Context(), "/mail/deliverability")
			if err != nil {
				return err
			}
			d, raw, err := a.Get(cmd.Context(), path, nil)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, d, func(d obj.Obj) {
				s := a.IO.S()
				v := a.IO.NewDetail("Deliverability", "")
				v.Field("Status", a.status(d.Str("status")))
				v.Field("Summary", d.Str("summary"))
				v.Field("Detail", d.Str("detail"))
				v.Field("Sending IP", d.Str("sending_ip"))
				switch {
				case !d.Has("reverse_dns_ok"):
					v.Field("Reverse DNS", s.Dim("not checked"))
				case d.Bool("reverse_dns_ok"):
					v.Field("Reverse DNS", s.Green("ok"))
				default:
					v.Field("Reverse DNS", s.Red("wrong"))
				}
				if r := d.Str("dmarc_pass_rate_percent"); r != "" {
					v.Field("DMARC pass rate", r+"%"+s.Dim(" of "+orNone(d.Str("dmarc_volume"), "?")+" messages"))
				}
				v.Field("Last checked", a.IO.When(d.Str("last_checked_at")))
				if bl := d.List("blocklists"); len(bl) > 0 {
					v.Section("Blocklists")
					for _, b := range bl {
						state := s.Green("not listed")
						if b.Bool("listed") {
							state = s.Red("LISTED")
						}
						v.Field(b.Str("name"), state)
					}
				}
				v.Render()
			})
		},
	}
}
