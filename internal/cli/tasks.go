package cli

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/getadaa/cli/internal/obj"
	"github.com/spf13/cobra"
)

func init() { register(newTasksCmd) }

var (
	taskStatuses   = []string{"pending", "awaiting_approval", "queued", "in_progress", "blocked", "done", "failed", "cancelled"}
	taskExecutions = []string{"connector", "agent", "person"}
)

func newTasksCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "tasks",
		Aliases: []string{"task"},
		Short:   "The work being done: automated and by people, in one list",
		Long: `A task is one piece of work that closes a finding: creating a mailbox,
revoking a license, replacing a disk. Anything that revokes, deletes or wipes
waits for your approval before it runs.`,
		GroupID: groupCore,
	}
	cmd.AddCommand(newTasksListCmd(a), newTasksViewCmd(a), newTasksApproveCmd(a), newTasksWaitCmd(a))
	return cmd
}

func newTasksListCmd(a *App) *cobra.Command {
	var lo ListOpts
	var status, execution string
	var awaiting, finished bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List tasks; finished ones are hidden unless you ask",
		Example: `  adaa tasks list
  adaa tasks list --awaiting-approval
  adaa tasks list --status failed --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := firstErr(oneOf("status", status, taskStatuses...), oneOf("execution", execution, taskExecutions...)); err != nil {
				return err
			}
			ctx := cmd.Context()
			path, err := a.OrgPath(ctx, "/tasks")
			if err != nil {
				return err
			}
			q := url.Values{}
			setQuery(q, cmd, "status", "status", status)
			setQuery(q, cmd, "execution", "execution", execution)
			if awaiting {
				q.Set("awaiting_approval", "true")
			}
			l, err := a.List(ctx, path, q, lo)
			if err != nil {
				return err
			}
			if status == "" && !finished {
				l = filterListing(l, func(o obj.Obj) bool {
					s := o.Str("status")
					return s != "done" && s != "cancelled"
				})
			}
			empty := "No work in progress."
			if awaiting {
				empty = "Nothing is waiting for your approval."
			}
			return a.PrintListing(l, empty, []string{"Status", "Summary", "By", "Updated", "ID"},
				func(o obj.Obj) []string {
					st := a.status(o.Str("status"))
					if o.Bool("destructive") {
						st += a.IO.S().Red(" !")
					}
					return []string{st, o.Str("summary"), o.Str("execution"), a.IO.When(o.Str("updated_at")), o.Str("id")}
				})
		},
	}
	addListFlags(cmd, &lo)
	cmd.Flags().StringVar(&status, "status", "", "Only tasks in this status")
	cmd.Flags().StringVar(&execution, "execution", "", "Only tasks done by a connector, an agent or a person")
	cmd.Flags().BoolVar(&awaiting, "awaiting-approval", false, "Only tasks waiting for your approval")
	cmd.Flags().BoolVar(&finished, "finished", false, "Include done and cancelled tasks")
	enumFlag(cmd, "status", taskStatuses...)
	enumFlag(cmd, "execution", taskExecutions...)
	return cmd
}

func newTasksViewCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "view [task]",
		Aliases:           []string{"show"},
		Short:             "Show a task and every step taken so far",
		Example:           `  adaa tasks view tsk_01JATX3M4K7Q2YV8N0RCBEZ5HS`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindTask),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindTask, arg(args))
			if err != nil {
				return err
			}
			t, raw, err := a.Get(ctx, "/tasks/"+id, nil)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, t, a.renderTask)
		},
	}
}

func (a *App) renderTask(t obj.Obj) {
	s := a.IO.S()
	d := a.IO.NewDetail(t.Str("summary"), t.Str("id"))
	d.Field("Status", a.status(t.Str("status")))
	d.Field("Kind", strings.ReplaceAll(t.Str("kind"), "_", " "))
	d.Field("Done by", map[string]string{"connector": "an integration", "agent": "an adaa agent", "person": "a person"}[t.Str("execution")])
	if t.Bool("destructive") {
		d.Field("Destructive", s.Red("yes — revokes, deletes, suspends or wipes something"))
	}
	switch {
	case t.Str("approved_at") != "":
		d.Field("Approved", a.IO.When(t.Str("approved_at")))
	case t.Bool("requires_approval"):
		d.Field("Approval", s.Yellow("required before it runs"))
	}
	d.Field("Scheduled", a.IO.When(t.Str("scheduled_for")))
	d.Field("Due", a.IO.When(t.Str("due_at")))
	d.Field("Where", t.Str("location"))
	d.Field("Finding", t.Str("finding_id"))
	d.Field("Person", t.Str("person_id"))
	d.Field("Resource", t.Str("resource_id"))
	d.Field("Part of", t.Str("parent_task_id"))
	d.Field("Started", a.IO.When(t.Str("started_at")))
	d.Field("Completed", a.IO.When(t.Str("completed_at")))
	if n, _ := t.Int("minutes_logged"); n > 0 {
		d.Field("Time spent", fmt.Sprintf("%d min", n))
	}
	if n, _ := t.Int("attempts"); n > 1 {
		d.Field("Attempts", fmt.Sprint(n))
	}
	if e := t.Str("error"); e != "" {
		d.Field("Error", s.Red(e))
	}
	if detail := t.Str("detail"); detail != "" {
		d.Section("Detail")
		d.Line(detail)
	}
	if steps := t.List("steps"); len(steps) > 0 {
		d.Section("Steps")
		for _, st := range steps {
			line := stepGlyph(a, st.Str("status")) + " " + st.Str("summary")
			if p, ok := st.Int("progress_percent"); ok && st.Str("status") == "in_progress" {
				line += s.Cyan(fmt.Sprintf(" %d%%", p))
			}
			if at := st.Str("at"); at != "" {
				line += "  " + s.Dim(a.IO.When(at))
			}
			for _, extra := range []string{st.Str("instruction"), st.Str("note")} {
				if extra != "" {
					line += "\n  " + s.Dim(extra)
				}
			}
			d.Line(line)
		}
	}
	d.Render()
	if t.Str("status") == "awaiting_approval" {
		a.IO.Hint("adaa tasks approve %s", t.Str("id"))
	} else if !taskFinished(t.Str("status")) {
		a.IO.Hint("adaa tasks wait %s", t.Str("id"))
	}
}

func stepGlyph(a *App, status string) string {
	s := a.IO.S()
	switch status {
	case "done":
		return s.Green("✓")
	case "in_progress":
		return s.Cyan("◐")
	case "failed":
		return s.Red("✗")
	case "skipped":
		return s.Dim("–")
	}
	return s.Dim("○")
}

func newTasksApproveCmd(a *App) *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:   "approve [task]",
		Short: "Approve a task that is waiting for you",
		Long: `Approve a task. Creating things runs on its own; anything that revokes,
deletes, suspends or wipes waits here for a person to say yes.`,
		Example: `  adaa tasks approve tsk_01JATX3M4K7Q2YV8N0RCBEZ5HS
  adaa tasks approve tsk_01JATX3M4K7Q2YV8N0RCBEZ5HS --yes --wait`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindTask),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			input := arg(args)
			var id string
			var err error
			if input == "" {
				id, err = a.pickAwaitingTask(cmd)
			} else {
				id, err = a.Resolve(ctx, kindTask, input)
			}
			if err != nil {
				return err
			}
			t, _, err := a.Get(ctx, "/tasks/"+id, nil)
			if err != nil {
				return err
			}
			if !a.JSON {
				fmt.Fprintln(a.IO.Err, a.IO.E().Bold(t.Str("summary")))
				if d := t.Str("detail"); d != "" {
					fmt.Fprintln(a.IO.Err, "  "+d)
				}
				if t.Bool("destructive") {
					a.IO.Warnf("This revokes, deletes, suspends or wipes something.")
				}
			}
			if err := a.Confirm("Approve it?"); err != nil {
				return err
			}
			resp, task, err := a.Send(ctx, http.MethodPost, "/tasks/"+id+"/approve", nil)
			if err != nil {
				return err
			}
			if !wait && !a.JSON {
				a.IO.Successf("Approved.")
			}
			return a.Accepted(ctx, resp, task, wait)
		},
	}
	addWaitFlag(cmd, &wait)
	return cmd
}

// pickAwaitingTask offers only the tasks that can be approved, since those are
// the only sensible answers to "approve which?".
func (a *App) pickAwaitingTask(cmd *cobra.Command) (string, error) {
	ctx := cmd.Context()
	path, err := a.OrgPath(ctx, "/tasks")
	if err != nil {
		return "", err
	}
	l, err := a.List(ctx, path, url.Values{"awaiting_approval": {"true"}}, ListOpts{Limit: 200})
	if err != nil {
		return "", err
	}
	if len(l.Items) == 0 {
		return "", fmt.Errorf("nothing is waiting for approval")
	}
	return a.pick(kindTask, l.Items, "Which task?")
}

func newTasksWaitCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "wait [task]",
		Short: "Wait for a task to finish, showing progress",
		Long: `Wait for a task to finish. Exits 0 when it is done and 1 when it failed, was
cancelled or is waiting for approval — so it can gate a script.`,
		Example: `  adaa tasks wait tsk_01JATX3M4K7Q2YV8N0RCBEZ5HS
  adaa tasks wait tsk_01JATX3M4K7Q2YV8N0RCBEZ5HS --json`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindTask),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindTask, arg(args))
			if err != nil {
				return err
			}
			t, raw, err := a.WaitTask(ctx, id)
			if err != nil {
				if raw != nil && a.JSON {
					_ = a.PrintJSON(raw)
				}
				return err
			}
			if a.JSON {
				if err := a.PrintJSON(raw); err != nil {
					return err
				}
				if t.Str("status") != "done" {
					return errSilent
				}
				return nil
			}
			return a.taskOutcome(t)
		},
	}
}
