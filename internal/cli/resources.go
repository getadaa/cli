package cli

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/getadaa/cli/internal/obj"
	"github.com/spf13/cobra"
)

func init() {
	register(newResourcesCmd)
	register(newSignalsCmd)
}

var (
	resourceKinds  = []string{"mailbox", "distribution_list", "license_seat", "cloud_account", "device", "virtual_machine", "physical_server", "backup_job", "file_share", "domain", "network_device", "other"}
	resourceStates = []string{"active", "suspended", "provisioning", "error", "archived", "unknown"}
	signalStatuses = []string{"ok", "warning", "critical", "unknown"}
	signalKinds    = []string{"backup_last_success", "backup_size_bytes", "host_reachable", "disk_free_percent", "memory_used_percent", "agent_last_seen", "patch_age_days", "encryption_enabled", "license_expires_on", "certificate_expires_on", "mailbox_quota_percent", "migration_progress_percent"}
)

func newResourcesCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "resources",
		Aliases: []string{"resource"},
		Short:   "Everything that actually exists: mailboxes, seats, machines, backups",
		Long: `A resource is one thing that actually exists in your company's IT — a mailbox,
a license seat, a laptop, a virtual machine, a backup job — in one uniform
list, whatever product it belongs to.`,
		GroupID: groupCore,
	}
	cmd.AddCommand(newResourcesListCmd(a), newResourcesViewCmd(a), newResourcesAssignCmd(a))
	return cmd
}

func newResourcesListCmd(a *App) *cobra.Command {
	var lo ListOpts
	var kind, state, person string
	var managed, unassigned bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List resources",
		Example: `  adaa resources list
  adaa resources list --unassigned        # where license waste hides
  adaa resources list --person kari@firma.no --kind mailbox`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := firstErr(oneOf("kind", kind, resourceKinds...), oneOf("state", state, resourceStates...)); err != nil {
				return err
			}
			ctx := cmd.Context()
			path, err := a.OrgPath(ctx, "/resources")
			if err != nil {
				return err
			}
			q := url.Values{}
			setQuery(q, cmd, "kind", "kind", kind)
			setQuery(q, cmd, "state", "state", state)
			if person != "" {
				id, err := a.Resolve(ctx, kindPerson, person)
				if err != nil {
					return err
				}
				q.Set("person_id", id)
			}
			if cmd.Flags().Changed("managed") {
				q.Set("managed", boolString(managed))
			}
			if unassigned {
				q.Set("unassigned", "true")
			}
			l, err := a.List(ctx, path, q, lo)
			if err != nil {
				return err
			}
			names := map[string]string{}
			if !a.JSON {
				names = a.personNames(ctx, l.Items)
			}
			return a.PrintListing(l, "No resources match.", []string{"Name", "Kind", "State", "Holder", "Managed", "ID"},
				func(o obj.Obj) []string {
					holder := a.dim("nobody")
					if p := o.Str("person_id"); p != "" {
						holder = names[p]
						if holder == "" {
							holder = p
						}
					}
					return []string{o.Str("display_name"), strings.ReplaceAll(o.Str("kind"), "_", " "),
						a.status(o.Str("state")), holder, o.Str("managed"), o.Str("id")}
				})
		},
	}
	addListFlags(cmd, &lo)
	f := cmd.Flags()
	f.StringVar(&kind, "kind", "", "Only resources of this kind")
	f.StringVar(&state, "state", "", "Only resources in this state")
	f.StringVar(&person, "person", "", "Only what this person holds (email, name or id)")
	f.BoolVar(&managed, "managed", false, "Only what adaa manages (--managed=false for only what it can see)")
	f.BoolVar(&unassigned, "unassigned", false, "Only what nobody holds")
	enumFlag(cmd, "kind", resourceKinds...)
	enumFlag(cmd, "state", resourceStates...)
	_ = cmd.RegisterFlagCompletionFunc("person", a.complete(kindPerson))
	return cmd
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// personNames maps the person ids in items to names with one list call. A
// failure only costs the names, so it is not an error.
func (a *App) personNames(ctx context.Context, items []obj.Obj) map[string]string {
	return a.personNamesBy(ctx, items, "person_id")
}

// personNamesBy is personNames for records that name the person in another field.
func (a *App) personNamesBy(ctx context.Context, items []obj.Obj, field string) map[string]string {
	names := map[string]string{}
	if !slices.ContainsFunc(items, func(o obj.Obj) bool { return o.Str(field) != "" }) {
		return names
	}
	path, err := a.OrgPath(ctx, "/people")
	if err != nil {
		return names
	}
	l, err := a.List(ctx, path, nil, ListOpts{Limit: 1000})
	if err != nil {
		return names
	}
	for _, p := range l.Items {
		names[p.Str("id")] = p.Str("full_name")
	}
	return names
}

func newResourcesViewCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "view [resource]",
		Aliases:           []string{"show"},
		Short:             "Show a resource and its signals",
		Example:           `  adaa resources view res_01JATX3M4K7Q2YV8N0RCBEZ5HS`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindResource),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindResource, arg(args))
			if err != nil {
				return err
			}
			r, raw, err := a.Get(ctx, "/resources/"+id, nil)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(raw)
			}
			var signals []obj.Obj
			if l, err := a.ListItems(ctx, "/resources/"+id+"/signals", nil); err == nil {
				signals = l.Items
			}
			holder := ""
			if p := r.Str("person_id"); p != "" {
				holder = a.personNames(ctx, []obj.Obj{r})[p]
				holder = join(" ", holder, a.dim(p))
			}
			d := a.IO.NewDetail(r.Str("display_name"), r.Str("id"))
			d.Field("Kind", strings.ReplaceAll(r.Str("kind"), "_", " "))
			d.Field("State", a.status(r.Str("state")))
			d.Field("Held by", holder)
			if r.Str("person_id") == "" {
				d.Field("Held by", a.dim("nobody"))
			}
			d.Field("Managed", map[bool]string{true: "yes, adaa operates it", false: "no, adaa can only see it"}[r.Bool("managed")])
			d.Field("Source", r.Str("source"))
			d.Field("Provider", r.Str("provider"))
			d.Field("Service", r.Str("service_code"))
			d.Field("Subscription", r.Str("subscription_id"))
			d.Field("External id", r.Str("external_id"))
			d.Field("Last seen", a.IO.When(r.Str("observed_at")))
			d.Field("Notes", r.Str("notes"))
			if attrs := r.Obj("attributes"); len(attrs) > 0 {
				d.Section("Details")
				keys := make([]string, 0, len(attrs))
				for k := range attrs {
					keys = append(keys, k)
				}
				slices.Sort(keys)
				for _, k := range keys {
					d.Field(strings.ReplaceAll(k, "_", " "), obj.String(attrs[k]))
				}
			}
			if len(signals) > 0 {
				d.Section("Signals")
				for _, s := range signals {
					d.Line(join("  ", severityDot(a.IO.S(), signalTone(s.Str("status")))+" "+s.Str("summary"),
						signalValue(s), a.dim(a.IO.When(s.Str("observed_at")))))
				}
			}
			d.Render()
			return nil
		},
	}
}

// signalTone maps a signal status onto the severity dot colours.
func signalTone(status string) string {
	switch status {
	case "ok":
		return ""
	case "unknown":
		return "unknown"
	}
	return status
}

func signalValue(s obj.Obj) string {
	if v := s.Str("value_text"); v != "" {
		return v
	}
	if v := s.Str("value_numeric"); v != "" {
		unit := s.Str("unit")
		if unit == "percent" || unit == "%" {
			return v + "%"
		}
		return join(" ", v, unit)
	}
	return ""
}

func newResourcesAssignCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "assign [resource] [person|none]",
		Short: "Record who holds a resource",
		Long: `Record who holds a resource — "that laptop is Kari's". Use none to give it
back to nobody.`,
		Example: `  adaa resources assign res_01JATX3M4K7Q2YV8N0RCBEZ5HS kari@firma.no
  adaa resources assign res_01JATX3M4K7Q2YV8N0RCBEZ5HS none`,
		Args:              cobra.MaximumNArgs(2),
		ValidArgsFunction: a.complete(kindResource),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindResource, arg(args))
			if err != nil {
				return err
			}
			var person any
			who := ""
			if len(args) > 1 {
				who = args[1]
			}
			switch strings.ToLower(who) {
			case "none", "nobody", "-":
			default:
				pid, err := a.Resolve(ctx, kindPerson, who)
				if err != nil {
					return err
				}
				person = pid
			}
			resp, r, err := a.Send(ctx, http.MethodPatch, "/resources/"+id, map[string]any{"person_id": person})
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			if person == nil {
				a.IO.Successf("%s is now held by nobody.", r.Str("display_name"))
			} else {
				a.IO.Successf("%s is now held by %s.", r.Str("display_name"), a.personNames(ctx, []obj.Obj{r})[r.Str("person_id")])
			}
			return nil
		},
	}
	return cmd
}

func newSignalsCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "signals",
		Aliases: []string{"signal"},
		Short:   "Measured facts: backups ran, servers are up, disks are not full",
		Long: `A signal is one measured fact with its freshness attached. A signal that has
gone stale reports unknown rather than its last value, so silence looks
broken rather than healthy.`,
		GroupID: groupCore,
	}
	var lo ListOpts
	var status, kind string
	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List signals",
		Example: `  adaa signals list
  adaa signals list --status critical
  adaa signals list --kind backup_last_success --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := firstErr(oneOf("status", status, signalStatuses...), oneOf("kind", kind, signalKinds...)); err != nil {
				return err
			}
			ctx := cmd.Context()
			path, err := a.OrgPath(ctx, "/signals")
			if err != nil {
				return err
			}
			q := url.Values{}
			setQuery(q, cmd, "status", "status", status)
			setQuery(q, cmd, "kind", "kind", kind)
			l, err := a.List(ctx, path, q, lo)
			if err != nil {
				return err
			}
			return a.PrintListing(l, "No signals match.", []string{"Status", "Summary", "Value", "Observed", "Resource"},
				func(o obj.Obj) []string {
					return []string{a.status(o.Str("status")), o.Str("summary"), signalValue(o),
						a.IO.When(o.Str("observed_at")), o.Str("resource_id")}
				})
		},
	}
	addListFlags(list, &lo)
	list.Flags().StringVar(&status, "status", "", "Only signals with this status")
	list.Flags().StringVar(&kind, "kind", "", "Only signals of this kind")
	enumFlag(list, "status", signalStatuses...)
	enumFlag(list, "kind", signalKinds...)
	cmd.AddCommand(list)
	return cmd
}
