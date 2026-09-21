package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() { register(newFindingsCmd) }

var (
	findingStatuses   = []string{"open", "triaged", "in_progress", "closed", "resolved"}
	findingSeverities = []string{"info", "warning", "critical"}
	findingKinds      = []string{"reported_problem", "credential_rotation_due", "warranty_expiring", "unused_license_seat",
		"mfa_missing", "excess_admin_rights", "oversharing", "dns_record_missing", "dns_conflict", "spf_lookup_limit",
		"domain_expiring", "transfer_stalled", "missing_resource", "orphan_resource", "unmanaged_resource",
		"billing_mismatch", "signal_critical", "coverage_gap", "stale_observation", "departed_person_access"}
	ignoreOutcomes = []string{"not_a_problem", "resolved_itself", "accepted_risk", "wont_fix"}
)

func newFindingsCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "findings",
		Aliases: []string{"finding", "problems"},
		Short:   "What is wrong: drift adaa detected and problems people reported",
		Long: `A finding is a difference between what your company should have and what
actually exists — a missing mailbox, an unused license, a backup that stopped —
or a problem somebody reported with 'adaa report'. Both live in one list.`,
		GroupID: groupCore,
	}
	cmd.AddCommand(newFindingsListCmd(a), newFindingsViewCmd(a), newFindingsResolveCmd(a),
		newFindingsIgnoreCmd(a), newFindingsAttachCmd(a))
	return cmd
}

func newFindingsListCmd(a *App) *cobra.Command {
	var lo ListOpts
	var status, severity, kind string
	var closed bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List findings, most recent first",
		Long: `List findings. Closed and resolved ones are hidden unless you ask for them
with --closed or a --status.`,
		Example: `  adaa findings list
  adaa findings list --severity critical
  adaa findings list --kind unused_license_seat --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := firstErr(oneOf("status", status, findingStatuses...), oneOf("severity", severity, findingSeverities...),
				oneOf("kind", kind, findingKinds...)); err != nil {
				return err
			}
			ctx := cmd.Context()
			path, err := a.OrgPath(ctx, "/findings")
			if err != nil {
				return err
			}
			q := url.Values{}
			setQuery(q, cmd, "status", "status", status)
			setQuery(q, cmd, "severity", "severity", severity)
			setQuery(q, cmd, "kind", "kind", kind)
			l, err := a.List(ctx, path, q, lo)
			if err != nil {
				return err
			}
			if status == "" && !closed {
				l = filterListing(l, func(o obj.Obj) bool {
					s := o.Str("status")
					return s != "closed" && s != "resolved"
				})
			}
			return a.PrintListing(l, "No open findings. Everything adaa can see looks right.",
				[]string{"Severity", "Summary", "Status", "Seen", "ID"},
				func(o obj.Obj) []string {
					summary := o.Str("summary")
					if o.Bool("blocking_work") {
						summary = a.IO.S().Red("[blocking] ") + summary
					}
					return []string{severityDot(a.IO.S(), o.Str("severity")) + " " + o.Str("severity"), summary,
						a.status(o.Str("status")), a.IO.When(o.Str("last_seen_at")), o.Str("id")}
				})
		},
	}
	addListFlags(cmd, &lo)
	cmd.Flags().StringVar(&status, "status", "", "Only findings in this status")
	cmd.Flags().StringVar(&severity, "severity", "", "Only findings of this severity")
	cmd.Flags().StringVar(&kind, "kind", "", "Only findings of this kind")
	cmd.Flags().BoolVar(&closed, "closed", false, "Include closed and resolved findings")
	enumFlag(cmd, "status", findingStatuses...)
	enumFlag(cmd, "severity", findingSeverities...)
	enumFlag(cmd, "kind", findingKinds...)
	return cmd
}

func newFindingsViewCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "view [finding]",
		Aliases:           []string{"show"},
		Short:             "Show one finding: what is wrong, what should be true, and what fixing it does",
		Example:           `  adaa findings view fnd_01JATX3M4K7Q2YV8N0RCBEZ5HS`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindFinding),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindFinding, arg(args))
			if err != nil {
				return err
			}
			f, raw, err := a.Get(ctx, "/findings/"+id, nil)
			if err != nil {
				return err
			}
			var attachments []obj.Obj
			if n, _ := f.Int("attachment_count"); n > 0 && !a.JSON {
				if l, err := a.ListItems(ctx, "/findings/"+id+"/attachments", nil); err == nil {
					attachments = l.Items
				}
			}
			return a.PrintObj(raw, f, func(f obj.Obj) { a.renderFinding(f, attachments) })
		},
	}
}

func (a *App) renderFinding(f obj.Obj, attachments []obj.Obj) {
	s := a.IO.S()
	d := a.IO.NewDetail(f.Str("summary"), f.Str("id"))
	d.Field("Severity", severityDot(s, f.Str("severity"))+" "+f.Str("severity"))
	d.Field("Status", a.status(f.Str("status")))
	d.Field("Kind", strings.ReplaceAll(f.Str("kind"), "_", " ")+s.Dim(" ("+f.Str("source")+")"))
	if f.Bool("blocking_work") {
		d.Field("Blocking", s.Red("somebody cannot work because of this"))
	}
	d.Field("Should be", f.Str("desired"))
	d.Field("Actually is", f.Str("observed"))
	d.Field("Fix", f.Str("suggested_action"))
	if n, ok := f.Int("cost_impact_monthly_minor"); ok && n != 0 {
		c := ui.SignedMoney(n, "") + "/month"
		if n < 0 {
			c = s.Green(c + " (a saving)")
		}
		d.Field("Cost impact", c)
	}
	if f.Has("auto_resolvable") && !f.Bool("auto_resolvable") {
		d.Field("Needs", "a person to fix it")
	}
	d.Field("Person", f.Str("person_id"))
	d.Field("Resource", f.Str("resource_id"))
	d.Field("Service", f.Str("service_code"))
	d.Field("Conversation", f.Str("request_id"))
	d.Field("Duplicate of", f.Str("duplicate_of_finding_id"))
	d.Field("First seen", a.IO.When(f.Str("first_seen_at")))
	d.Field("Last seen", a.IO.When(f.Str("last_seen_at")))
	d.Field("Resolved", a.IO.When(f.Str("resolved_at")))
	if o := f.Str("closed_outcome"); o != "" {
		d.Field("Outcome", strings.ReplaceAll(o, "_", " "))
	}
	d.Field("Ignored because", f.Str("ignored_reason"))
	if detail := f.Str("detail"); detail != "" {
		d.Section("Detail")
		d.Line(detail)
	}
	if len(attachments) > 0 {
		d.Section("Attachments")
		for _, at := range attachments {
			d.Line(attachmentLine(a, at))
		}
	}
	d.Render()

	switch f.Str("status") {
	case "open", "triaged":
		a.IO.Hint("adaa findings resolve %s", f.Str("id"))
	}
}

func attachmentLine(a *App, at obj.Obj) string {
	line := at.Str("filename")
	if n, ok := at.Int("size_bytes"); ok {
		line += a.dim(" " + ui.Bytes(n))
	}
	if c := at.Str("caption"); c != "" {
		line += " — " + c
	}
	return line + a.dim("  "+at.Str("id"))
}

func newFindingsResolveCmd(a *App) *cobra.Command {
	var direction, note string
	var dryRun, wait bool
	cmd := &cobra.Command{
		Use:   "resolve [finding]",
		Short: "Raise the work that fixes a finding",
		Long: `Raise the work that closes a finding. Anything destructive still waits for
approval with 'adaa tasks approve'.

--direction decides who wins the disagreement: enforce (the default) changes
reality to match what you should have; adopt changes what you should have to
match reality — "this is how it is meant to be".`,
		Example: `  adaa findings resolve fnd_01JATX3M4K7Q2YV8N0RCBEZ5HS
  adaa findings resolve fnd_01JATX3M4K7Q2YV8N0RCBEZ5HS --direction adopt --yes
  adaa findings resolve fnd_01JATX3M4K7Q2YV8N0RCBEZ5HS --dry-run --json`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindFinding),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := oneOf("direction", direction, "enforce", "adopt"); err != nil {
				return err
			}
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindFinding, arg(args))
			if err != nil {
				return err
			}
			body := map[string]any{"direction": direction}
			if note != "" {
				body["note"] = note
			}
			question := "Fix this?"
			if direction == "adopt" {
				question = "Accept how things are now as how they should be?"
			}
			resp, result, err := a.Apply(ctx, Change{Method: http.MethodPost, Path: "/findings/" + id + "/resolve",
				Body: body, Question: question, DryRun: dryRun})
			if err != nil || dryRun {
				return err
			}
			return a.followResult(ctx, resp.Body, result, wait)
		},
	}
	cmd.Flags().StringVar(&direction, "direction", "enforce", "enforce (change reality) or adopt (change intent)")
	cmd.Flags().StringVar(&note, "note", "", "A note for whoever does the work")
	enumFlag(cmd, "direction", "enforce", "adopt")
	addDryRunFlag(cmd, &dryRun)
	addWaitFlag(cmd, &wait)
	return cmd
}

// followResult narrates a ChangeResult and, with wait, follows each task it
// raised to the end. Tasks waiting for approval are pointed out, not waited on.
func (a *App) followResult(ctx context.Context, raw []byte, result obj.Obj, wait bool) error {
	if !wait {
		if a.JSON {
			return a.PrintJSON(raw)
		}
		a.ReportResult(result)
		if tasks := result.List("tasks"); len(tasks) > 0 && tasks[0].Str("status") != "awaiting_approval" {
			a.IO.Hint("adaa tasks wait %s", tasks[0].Str("id"))
		}
		return nil
	}
	if !a.JSON {
		a.ReportResult(result)
	}
	var finals []json.RawMessage
	var failed bool
	for _, t := range result.List("tasks") {
		if taskFinished(t.Str("status")) || t.Str("status") == "awaiting_approval" {
			b, _ := json.Marshal(t)
			finals = append(finals, b)
			continue
		}
		final, fraw, err := a.WaitTask(ctx, t.Str("id"))
		if err != nil {
			return err
		}
		finals = append(finals, fraw)
		if a.JSON {
			continue
		}
		if a.taskOutcome(final) != nil {
			failed = true
		}
	}
	if a.JSON {
		b, _ := json.Marshal(map[string]any{"items": finals, "next_cursor": nil})
		return a.PrintJSON(b)
	}
	if failed {
		return errSilent
	}
	return nil
}

func newFindingsIgnoreCmd(a *App) *cobra.Command {
	var outcome, reason, until string
	cmd := &cobra.Command{
		Use:     "ignore [finding]",
		Aliases: []string{"dismiss"},
		Short:   "Dismiss a finding, with the reason why",
		Long: `Dismiss a finding. A reason is always required: a list closed without reasons
turns into noise nobody reads.

--outcome says what kind of dismissal this is:
  not_a_problem    it was a false alarm
  resolved_itself  it went away on its own
  accepted_risk    it is real, and you accept it
  wont_fix         it is real, and it will not be fixed

A detected finding comes back if the facts behind it change. --until snoozes
it until a date instead of dismissing it for good.`,
		Example: `  adaa findings ignore fnd_01JATX3M4K7Q2YV8N0RCBEZ5HS --outcome accepted_risk --reason "Shared kiosk, no MFA possible"
  adaa findings ignore fnd_01JATX3M4K7Q2YV8N0RCBEZ5HS --outcome wont_fix --reason "Replacing it in Q1" --until 2027-01-15`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindFinding),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := oneOf("outcome", outcome, ignoreOutcomes...); err != nil {
				return err
			}
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindFinding, arg(args))
			if err != nil {
				return err
			}
			if outcome == "" {
				opts := []ui.Option{
					{Label: "It was a false alarm", Value: "not_a_problem"},
					{Label: "It went away on its own", Value: "resolved_itself"},
					{Label: "It is real, and we accept the risk", Value: "accepted_risk"},
					{Label: "It is real, and we will not fix it", Value: "wont_fix"},
				}
				if outcome, err = a.IO.Select("Why dismiss it?", "--outcome", opts); err != nil {
					return err
				}
			}
			if err := a.need(&reason, "In a sentence, why?", "--reason", nonEmpty); err != nil {
				return err
			}
			body := map[string]any{"outcome": outcome, "reason": reason}
			if until != "" {
				body["until"] = until
			}
			resp, f, err := a.Send(ctx, http.MethodPost, "/findings/"+id+"/ignore", body)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Dismissed: %s", f.Str("summary"))
			return nil
		},
	}
	cmd.Flags().StringVar(&outcome, "outcome", "", "not_a_problem, resolved_itself, accepted_risk or wont_fix")
	cmd.Flags().StringVar(&reason, "reason", "", "Why it is being dismissed")
	cmd.Flags().StringVar(&until, "until", "", "Snooze until this date (YYYY-MM-DD) instead of for good")
	enumFlag(cmd, "outcome", ignoreOutcomes...)
	return cmd
}

func nonEmpty(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("cannot be empty")
	}
	return nil
}

func newFindingsAttachCmd(a *App) *cobra.Command {
	var caption string
	cmd := &cobra.Command{
		Use:   "attach <finding> <file>...",
		Short: "Attach screenshots, photos or logs to a finding",
		Example: `  adaa findings attach fnd_01JATX3M4K7Q2YV8N0RCBEZ5HS error.png
  adaa findings attach fnd_01JATX3M4K7Q2YV8N0RCBEZ5HS printer.jpg --caption "The display after the jam"`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindFinding, args[0])
			if err != nil {
				return err
			}
			raw, err := a.attachFiles(ctx, "/findings/"+id+"/attachments", args[1:], caption)
			if err != nil {
				return err
			}
			if a.JSON {
				b, _ := json.Marshal(map[string]any{"items": raw, "next_cursor": nil})
				return a.PrintJSON(b)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&caption, "caption", "", "A caption for the attached files")
	return cmd
}

const maxAttachmentBytes = 25 << 20

// attachFiles uploads files one at a time to an attachments endpoint. Every
// file is checked before the first upload, so a typo in the last name does not
// leave half the files attached.
func (a *App) attachFiles(ctx context.Context, path string, files []string, caption string) ([]json.RawMessage, error) {
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil {
			return nil, usagef("cannot attach %s: %v", f, err)
		}
		if st.IsDir() {
			return nil, usagef("cannot attach %s: it is a directory", f)
		}
		if st.Size() > maxAttachmentBytes {
			return nil, usagef("cannot attach %s: %s is over the 25 MiB limit", f, ui.Bytes(st.Size()))
		}
	}
	c, err := a.Client()
	if err != nil {
		return nil, err
	}
	var out []json.RawMessage
	for _, f := range files {
		resp, err := ui.Spin(a.IO, "Uploading "+filepath.Base(f)+"…", func() ([]byte, error) {
			r, err := c.Upload(ctx, path, f, caption)
			if err != nil {
				return nil, err
			}
			return r.Body, nil
		})
		if err != nil {
			return out, fmt.Errorf("uploading %s: %w", f, err)
		}
		out = append(out, resp)
		if !a.JSON {
			a.IO.Successf("Attached %s", filepath.Base(f))
		}
	}
	return out, nil
}

// filterListing keeps the items (and their raw JSON) that keep returns true for.
func filterListing(l *Listing, keep func(obj.Obj) bool) *Listing {
	out := &Listing{Next: l.Next}
	for n, it := range l.Items {
		if keep(it) {
			out.Items = append(out.Items, it)
			out.Raw = append(out.Raw, l.Raw[n])
		}
	}
	return out
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
