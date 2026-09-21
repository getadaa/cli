package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/getadaa/cli/internal/obj"
	"github.com/spf13/cobra"
)

func init() { register(newActivityCmd) }

var (
	activityCategories = []string{"change", "execution", "access", "detection", "communication"}
	actorKinds         = []string{"person", "agent", "connector", "system"}
	activityOutcomes   = []string{"succeeded", "failed", "in_progress"}
	entityTypes        = []string{"organization", "person", "entitlement", "resource", "device", "credential", "license",
		"domain", "dns_record", "domain_transfer", "workspace", "workspace_account", "workspace_group", "mailbox",
		"mail_domain", "server", "backup_job", "finding", "task", "request", "service", "subscription", "attachment", "token"}
	// entityTypeByPrefix lets --entity take any id without also naming its type.
	entityTypeByPrefix = map[string]string{
		"org_": "organization", "per_": "person", "ent_": "entitlement", "res_": "resource", "dev_": "device",
		"crd_": "credential", "lic_": "license", "dom_": "domain", "dns_": "dns_record", "dtr_": "domain_transfer",
		"wsp_": "workspace", "wac_": "workspace_account", "wgr_": "workspace_group", "mbx_": "mailbox",
		"srv_": "server", "bkp_": "backup_job", "fnd_": "finding", "tsk_": "task", "req_": "request",
		"svc_": "service", "sub_": "subscription", "att_": "attachment", "tok_": "token",
	}
)

func newActivityCmd(a *App) *cobra.Command {
	var lo ListOpts
	var entity, entityType, category, actor, outcome, since, until string
	var follow bool
	cmd := &cobra.Command{
		Use:     "activity",
		Aliases: []string{"log"},
		Short:   "Everything that happened, newest first",
		Long: `One log of everything that happened: every change, every piece of work, every
sign-in, every backup, every problem opened or closed — whoever did it, person
or machine.

--since and --until take a duration back from now (30m, 24h, 7d, 2w), a date
(2026-09-01) or a full RFC 3339 time. --follow keeps printing new events as
they happen until you press Ctrl-C; with --json it prints one event per line.`,
		Example: `  adaa activity
  adaa activity --since 24h --actor agent
  adaa activity --entity per_01JATX3M4K7Q2YV8N0RCBEZ5HS
  adaa activity --outcome failed --since 7d --json
  adaa activity --follow`,
		GroupID: groupCore,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := firstErr(oneOf("category", category, activityCategories...), oneOf("actor", actor, actorKinds...),
				oneOf("outcome", outcome, activityOutcomes...), oneOf("entity-type", entityType, entityTypes...)); err != nil {
				return err
			}
			ctx := cmd.Context()
			org, err := a.OrgID(ctx)
			if err != nil {
				return err
			}
			q := url.Values{"organization_id": {org}}
			now := time.Now()
			for _, t := range []struct{ flag, key, val string }{{"since", "since", since}, {"until", "until", until}} {
				if t.val == "" {
					continue
				}
				v, err := parseSince(t.val, now)
				if err != nil {
					return usagef("--%s: %v", t.flag, err)
				}
				q.Set(t.key, v)
			}
			if entity != "" {
				q.Set("entity_id", entity)
				if entityType == "" && len(entity) > 4 {
					entityType = entityTypeByPrefix[entity[:4]]
				}
			}
			setQuery(q, cmd, "category", "category", category)
			setQuery(q, cmd, "actor", "actor_kind", actor)
			setQuery(q, cmd, "outcome", "outcome", outcome)
			if entityType != "" {
				q.Set("entity_type", entityType)
			}

			l, err := a.List(ctx, "/activity", q, lo)
			if err != nil {
				return err
			}
			if !follow {
				return a.PrintListing(l, "Nothing has happened in that window.", activityHeaders, a.activityRow)
			}
			return a.followActivity(cmd, q, l)
		},
	}
	addListFlags(cmd, &lo)
	f := cmd.Flags()
	f.StringVar(&entity, "entity", "", "Only events about this record (any id)")
	f.StringVar(&entityType, "entity-type", "", "Only events about this kind of record")
	f.StringVar(&category, "category", "", "change, execution, access, detection or communication")
	f.StringVar(&actor, "actor", "", "Only events by a person, agent, connector or system")
	f.StringVar(&outcome, "outcome", "", "succeeded, failed or in_progress")
	f.StringVar(&since, "since", "", "Only events after this (24h, 7d, 2026-09-01, …)")
	f.StringVar(&until, "until", "", "Only events before this")
	f.BoolVarP(&follow, "follow", "f", false, "Keep printing new events until interrupted")
	enumFlag(cmd, "category", activityCategories...)
	enumFlag(cmd, "actor", actorKinds...)
	enumFlag(cmd, "outcome", activityOutcomes...)
	enumFlag(cmd, "entity-type", entityTypes...)
	return cmd
}

var activityHeaders = []string{"When", "Who", "Summary", "Outcome", "Entity"}

func (a *App) activityRow(o obj.Obj) []string {
	who := o.Str("actor_label")
	if who == "" {
		who = o.Str("actor_email")
	}
	if who == "" {
		who = o.Str("actor_kind")
	}
	out := o.Str("outcome")
	if out == "succeeded" {
		out = ""
	}
	return []string{a.IO.When(o.Str("at")), who, o.Str("summary"), a.status(out), o.Str("entity_id")}
}

// followActivity prints what is there in time order, then polls for events
// newer than the last one seen. Events are deduplicated by id because since
// is inclusive at the boundary on most servers.
func (a *App) followActivity(cmd *cobra.Command, q url.Values, l *Listing) error {
	ctx := cmd.Context()
	seen := map[string]bool{}
	last := ""
	emit := func(items []obj.Obj, raw []json.RawMessage) {
		// The API lists newest first; a follow reads top to bottom.
		for n := len(items) - 1; n >= 0; n-- {
			it := items[n]
			if seen[it.Str("id")] {
				continue
			}
			seen[it.Str("id")] = true
			if at := it.Str("at"); at > last {
				last = at
			}
			if a.JSON {
				b, _ := json.Marshal(raw[n])
				fmt.Fprintln(a.IO.Out, string(b))
				continue
			}
			row := a.activityRow(it)
			fmt.Fprintln(a.IO.Out, strings.Join(slices.DeleteFunc(row, func(s string) bool { return s == "" }), "  "))
		}
	}
	emit(l.Items, l.Raw)
	if !a.JSON {
		a.IO.Infof("%s", a.dim("Following new activity. Press Ctrl-C to stop."))
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
		pq := url.Values{}
		for k, v := range q {
			pq[k] = v
		}
		pq.Del("until")
		if last != "" {
			pq.Set("since", last)
		}
		nl, err := a.List(ctx, "/activity", pq, ListOpts{Limit: 200})
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		emit(nl.Items, nl.Raw)
	}
}

// parseSince turns "24h", "7d", "2w", a date or an RFC 3339 time into an
// RFC 3339 instant in UTC.
func parseSince(s string, now time.Time) (string, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, now.Location()); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	if len(s) > 1 {
		unit := s[len(s)-1]
		if n, err := strconv.Atoi(s[:len(s)-1]); err == nil && n >= 0 {
			var d time.Duration
			switch unit {
			case 'd':
				d = time.Duration(n) * 24 * time.Hour
			case 'w':
				d = time.Duration(n) * 7 * 24 * time.Hour
			}
			if d > 0 || (n == 0 && (unit == 'd' || unit == 'w')) {
				return now.Add(-d).UTC().Format(time.RFC3339), nil
			}
		}
	}
	if d, err := time.ParseDuration(s); err == nil {
		return now.Add(-d).UTC().Format(time.RFC3339), nil
	}
	return "", fmt.Errorf("%q is not a duration (24h, 7d), a date (2026-09-01) or an RFC 3339 time", s)
}
