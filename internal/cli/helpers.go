package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/getadaa/cli/internal/api"
	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

// ---- Reading -------------------------------------------------------------

// ListOpts are the --limit and --all flags every list command has.
type ListOpts struct {
	Limit int
	All   bool
}

func addListFlags(cmd *cobra.Command, lo *ListOpts) {
	cmd.Flags().IntVarP(&lo.Limit, "limit", "L", 50, "Maximum number of items to fetch")
	cmd.Flags().BoolVar(&lo.All, "all", false, "Fetch every item, following pages")
}

// Listing is one fetched list, kept raw so --json can pass it through.
type Listing struct {
	Items []obj.Obj
	Raw   []json.RawMessage
	Next  string
}

// List fetches a paginated list endpoint.
func (a *App) List(ctx context.Context, path string, q url.Values, lo ListOpts) (*Listing, error) {
	c, err := a.Client()
	if err != nil {
		return nil, err
	}
	limit := lo.Limit
	if lo.All {
		limit = 0
	}
	raw, next, err := c.List(ctx, path, q, limit)
	if err != nil {
		return nil, err
	}
	items, err := obj.ParseAll(raw)
	if err != nil {
		return nil, err
	}
	return &Listing{Items: items, Raw: raw, Next: next}, nil
}

// ListItems fetches an endpoint that returns {"items": [...]} without paging,
// or a bare array.
func (a *App) ListItems(ctx context.Context, path string, q url.Values) (*Listing, error) {
	resp, err := a.Do(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		return nil, err
	}
	var raw []json.RawMessage
	var page api.Page
	if json.Unmarshal(resp.Body, &page) == nil && page.Items != nil {
		raw = page.Items
	} else if err := json.Unmarshal(resp.Body, &raw); err != nil {
		return nil, fmt.Errorf("unexpected response from %s", path)
	}
	items, err := obj.ParseAll(raw)
	if err != nil {
		return nil, err
	}
	return &Listing{Items: items, Raw: raw}, nil
}

// Get fetches one record.
func (a *App) Get(ctx context.Context, path string, q url.Values) (obj.Obj, []byte, error) {
	resp, err := a.Do(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		return nil, nil, err
	}
	o, err := obj.Parse(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("unexpected response from %s: %w", path, err)
	}
	return o, resp.Body, nil
}

// Do sends any request with the logged-in credential.
func (a *App) Do(ctx context.Context, method, path string, q url.Values, body any) (*api.Response, error) {
	c, err := a.Client()
	if err != nil {
		return nil, err
	}
	return c.Do(ctx, api.Request{Method: method, Path: path, Query: q, Body: body})
}

// Send is Do for mutations that answer with a record (or nothing).
func (a *App) Send(ctx context.Context, method, path string, body any) (*api.Response, obj.Obj, error) {
	resp, err := a.Do(ctx, method, path, nil, body)
	if err != nil {
		return nil, nil, err
	}
	if len(resp.Body) == 0 {
		return resp, obj.Obj{}, nil
	}
	o, err := obj.Parse(resp.Body)
	if err != nil {
		return resp, nil, fmt.Errorf("unexpected response from %s: %w", path, err)
	}
	return resp, o, nil
}

// ---- Printing ------------------------------------------------------------

// PrintJSON writes raw API JSON to stdout, indented.
func (a *App) PrintJSON(raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		_, err = a.IO.Out.Write(raw)
		return err
	}
	enc := json.NewEncoder(a.IO.Out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// PrintListing prints a list as a table, or as the API's page shape with --json.
//
// empty is said when there is nothing, because a blank screen reads as broken.
func (a *App) PrintListing(l *Listing, empty string, headers []string, row func(obj.Obj) []string) error {
	if a.JSON {
		var next any
		if l.Next != "" {
			next = l.Next
		}
		items := l.Raw
		if items == nil {
			items = []json.RawMessage{}
		}
		b, err := json.Marshal(map[string]any{"items": items, "next_cursor": next})
		if err != nil {
			return err
		}
		return a.PrintJSON(b)
	}
	if len(l.Items) == 0 {
		if empty != "" {
			a.IO.Infof("%s", empty)
		}
		return nil
	}
	t := a.IO.NewTable(headers...).Flex(flexColumn(headers))
	for _, it := range l.Items {
		t.Row(row(it)...)
	}
	t.Render()
	if l.Next != "" && a.IO.OutTTY {
		a.IO.Hint("showing the first %d; pass --all for everything", len(l.Items))
	}
	return nil
}

// flexColumn picks the column to squeeze on a narrow terminal: the one that
// holds prose, by its header.
func flexColumn(headers []string) int {
	for n, h := range headers {
		switch strings.ToLower(h) {
		case "summary", "title", "name", "description", "detail", "headline":
			return n
		}
	}
	return len(headers) - 1
}

// PrintObj prints one record with view, or raw with --json.
func (a *App) PrintObj(raw []byte, o obj.Obj, view func(obj.Obj)) error {
	if a.JSON {
		return a.PrintJSON(raw)
	}
	view(o)
	return nil
}

// Short helpers for table cells.

func (a *App) status(s string) string { return a.IO.S().Status(s) }
func (a *App) dim(s string) string    { return a.IO.S().Dim(s) }

// money formats a minor-unit field with the record's currency.
func money(o obj.Obj, field string) string {
	n, ok := o.Int(field)
	if !ok {
		return ""
	}
	return ui.Money(n, o.Str("currency"))
}

// ---- Deciding ------------------------------------------------------------

// Confirm asks before a change. --yes answers for the person; without a
// terminal and without --yes the command fails rather than guessing.
func (a *App) Confirm(question string) error {
	if a.Yes {
		return nil
	}
	ok, err := a.IO.Confirm(question, "--yes", false)
	if err != nil {
		return err
	}
	if !ok {
		return ui.ErrCancelled
	}
	return nil
}

// need returns value, or asks for it, or fails naming the flag.
func (a *App) need(value *string, title, flag string, validate func(string) error) error {
	if strings.TrimSpace(*value) != "" {
		if validate != nil {
			if err := validate(*value); err != nil {
				return usagef("%s: %v", flag, err)
			}
		}
		return nil
	}
	return a.IO.Input(title, flag, value, validate)
}

// ---- Changing ------------------------------------------------------------

// Change is a mutation whose endpoint takes `dry_run`. Those are the changes
// that cost money or raise work, so they get a preview before they happen.
type Change struct {
	Method string
	Path   string
	Body   map[string]any
	// Question is asked after the preview, e.g. "Onboard Kari Nordmann?".
	Question string
	// DryRun is the --dry-run flag: show the preview and stop.
	DryRun bool
}

func addDryRunFlag(cmd *cobra.Command, v *bool) {
	cmd.Flags().BoolVar(v, "dry-run", false, "Show what would happen and what it would cost, without changing anything")
}

// Apply previews, confirms and then makes a change.
//
// With a terminal the preview is always shown first, since "what will this
// cost" is the question every change raises. Without one, --yes is required,
// and --dry-run is how an agent gets the same preview.
func (a *App) Apply(ctx context.Context, c Change) (*api.Response, obj.Obj, error) {
	if c.Body == nil {
		c.Body = map[string]any{}
	}
	if c.DryRun || (!a.Yes && a.IO.Interactive()) {
		c.Body["dry_run"] = true
		preview, err := ui.Spin(a.IO, "Working out what this would do…", func() (*api.Response, error) {
			return a.Do(ctx, c.Method, c.Path, nil, c.Body)
		})
		if err != nil {
			return nil, nil, err
		}
		p, err := obj.Parse(preview.Body)
		if err != nil {
			return nil, nil, err
		}
		if c.DryRun {
			if a.JSON {
				return preview, p, a.PrintJSON(preview.Body)
			}
			a.renderPreview(p)
			return preview, p, nil
		}
		a.renderPreview(p)
		if len(p.List("blocked")) > 0 {
			a.IO.Warnf("Some of this cannot be done; the rest will go ahead.")
		}
		if err := a.Confirm(c.Question); err != nil {
			return nil, nil, err
		}
	} else if !a.Yes {
		return nil, nil, &ui.NoInputError{What: "confirmation", Flag: "--yes (or --dry-run to preview)"}
	}
	delete(c.Body, "dry_run")
	return a.Send(ctx, c.Method, c.Path, c.Body)
}

// renderPreview draws a ChangePreview to stderr, where the confirm prompt is.
func (a *App) renderPreview(p obj.Obj) {
	io := a.IO
	e := io.E()
	fmt.Fprintln(io.Err)
	fmt.Fprintln(io.Err, e.Bold(p.Str("summary")))
	for _, ch := range p.List("changes") {
		sign := map[string]string{"create": e.Green("+"), "assign": e.Green("+"), "update": e.Yellow("~"),
			"revoke": e.Red("−"), "release": e.Red("−"), "none": e.Dim("·")}[ch.Str("action")]
		if sign == "" {
			sign = "·"
		}
		line := "  " + sign + " " + ch.Str("summary")
		var notes []string
		if ch.Bool("requires_approval") {
			notes = append(notes, "needs approval")
		}
		if ch.Str("execution") == "person" {
			notes = append(notes, "done by a person")
		}
		if len(notes) > 0 {
			line += "  " + e.Dim("("+strings.Join(notes, ", ")+")")
		}
		fmt.Fprintln(io.Err, line)
	}
	a.renderCost(p.Obj("cost_delta"))
	for _, b := range p.List("blocked") {
		reason := b.Str("reason")
		if reason == "" {
			reason = b.Str("detail")
		}
		fmt.Fprintln(io.Err, "  "+e.Red("✗")+" "+b.Str("summary")+" "+e.Dim(reason))
	}
	fmt.Fprintln(io.Err)
}

func (a *App) renderCost(cd obj.Obj) {
	if cd == nil {
		return
	}
	e := a.IO.E()
	cur := cd.Str("currency")
	change, _ := cd.Int("monthly_change_minor")
	before, _ := cd.Int("monthly_before_minor")
	after, _ := cd.Int("monthly_after_minor")
	line := "  Cost: "
	switch {
	case change == 0:
		line += "no change to the monthly bill"
	case change < 0:
		line += e.Green(ui.SignedMoney(change, cur)+"/month") + e.Dim(fmt.Sprintf(" (%s → %s)", ui.Money(before, cur), ui.Money(after, cur)))
	default:
		line += e.Yellow(ui.SignedMoney(change, cur)+"/month") + e.Dim(fmt.Sprintf(" (%s → %s)", ui.Money(before, cur), ui.Money(after, cur)))
	}
	if once, ok := cd.Int("one_time_minor"); ok && once > 0 {
		line += ", plus " + ui.Money(once, cur) + " once"
	}
	if n, _ := cd.Int("lines_awaiting_price"); n > 0 {
		line += e.Dim(fmt.Sprintf(" — an estimate, %d line(s) not priced yet", n))
	}
	fmt.Fprintln(a.IO.Err, line)
}

// ReportResult narrates a ChangeResult (or a record that embeds one as `plan`).
func (a *App) ReportResult(o obj.Obj) {
	plan := o
	if o.Has("plan") {
		plan = o.Obj("plan")
	}
	if plan == nil {
		return
	}
	if s := plan.Str("summary"); s != "" {
		a.IO.Successf("%s", s)
	}
	for _, t := range plan.List("tasks") {
		line := "  " + t.Str("summary") + " " + a.IO.E().Dim(t.Str("id"))
		if t.Bool("requires_approval") && t.Str("status") == "awaiting_approval" {
			line += " " + a.IO.E().Yellow("awaiting approval")
		}
		fmt.Fprintln(a.IO.Err, line)
	}
	if plan.Has("cost_delta") {
		a.renderCost(plan.Obj("cost_delta"))
	}
	for _, t := range plan.List("tasks") {
		if t.Str("status") == "awaiting_approval" {
			a.IO.Hint("adaa tasks approve %s", t.Str("id"))
			break
		}
	}
}

// ---- Waiting -------------------------------------------------------------

func addWaitFlag(cmd *cobra.Command, v *bool) {
	cmd.Flags().BoolVarP(v, "wait", "w", false, "Wait for the work to finish instead of returning once it is queued")
}

func taskFinished(status string) bool {
	switch status {
	case "done", "failed", "cancelled":
		return true
	}
	return false
}

// Accepted handles a 202: the API queued work as a Task. With wait it follows
// the task to the end; otherwise it says how to follow it later.
func (a *App) Accepted(ctx context.Context, resp *api.Response, task obj.Obj, wait bool) error {
	if !strings.HasPrefix(task.Str("id"), "tsk_") {
		plan := task
		if task.Has("plan") {
			plan = task.Obj("plan")
		}
		if plan == nil || !plan.Has("tasks") {
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			return nil
		}
		return a.followPlan(ctx, resp, task, plan, wait)
	}
	if wait {
		final, raw, err := a.WaitTask(ctx, task.Str("id"))
		if err != nil {
			return err
		}
		if a.JSON {
			return a.PrintJSON(raw)
		}
		return a.taskOutcome(final)
	}
	if a.JSON {
		return a.PrintJSON(resp.Body)
	}
	a.IO.Successf("Queued: %s %s", task.Str("summary"), a.IO.E().Dim(task.Str("id")))
	if task.Str("status") == "awaiting_approval" {
		a.IO.Hint("adaa tasks approve %s", task.Str("id"))
	} else {
		a.IO.Hint("adaa tasks wait %s", task.Str("id"))
	}
	return nil
}

// followPlan reports a ChangeResult and, with wait, follows each task it
// raised. Tasks waiting for approval are pointed out rather than waited on,
// since nothing will happen until a person approves them.
func (a *App) followPlan(ctx context.Context, resp *api.Response, whole, plan obj.Obj, wait bool) error {
	if !a.JSON {
		a.ReportResult(whole)
	}
	if !wait {
		if a.JSON {
			return a.PrintJSON(resp.Body)
		}
		return nil
	}
	var failed bool
	var finals []json.RawMessage
	for _, t := range plan.List("tasks") {
		if t.Str("status") == "awaiting_approval" || taskFinished(t.Str("status")) {
			continue
		}
		final, raw, err := a.WaitTask(ctx, t.Str("id"))
		if errors.Is(err, errSilent) {
			continue
		}
		if err != nil {
			return err
		}
		finals = append(finals, raw)
		if !a.JSON && a.taskOutcome(final) != nil {
			failed = true
		}
	}
	if a.JSON {
		b, err := json.Marshal(map[string]any{"result": json.RawMessage(resp.Body), "tasks": finals})
		if err != nil {
			return err
		}
		return a.PrintJSON(b)
	}
	if failed {
		return errSilent
	}
	return nil
}

// WaitTask polls a task until it finishes, showing the latest step.
func (a *App) WaitTask(ctx context.Context, id string) (obj.Obj, []byte, error) {
	sp := a.IO.StartSpinner("Waiting for " + id)
	defer sp.Stop()
	delay := time.Second
	for {
		t, raw, err := a.Get(ctx, "/tasks/"+id, nil)
		if err != nil {
			return nil, nil, err
		}
		status := t.Str("status")
		if taskFinished(status) {
			return t, raw, nil
		}
		if status == "awaiting_approval" {
			sp.Stop()
			a.IO.Warnf("%s is waiting for approval.", id)
			a.IO.Hint("adaa tasks approve %s", id)
			return t, raw, errSilent
		}
		title := t.Str("summary") + " — " + strings.ReplaceAll(status, "_", " ")
		if steps := t.List("steps"); len(steps) > 0 {
			last := steps[len(steps)-1]
			title += ": " + last.Str("summary")
			if p, ok := last.Int("progress_percent"); ok {
				title += " (" + strconv.FormatInt(p, 10) + "%)"
			}
		}
		sp.Update(title)
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(delay):
		}
		delay = min(delay*3/2, 10*time.Second)
	}
}

func (a *App) taskOutcome(t obj.Obj) error {
	switch t.Str("status") {
	case "done":
		a.IO.Successf("%s", t.Str("summary"))
		return nil
	case "cancelled":
		a.IO.Warnf("Cancelled: %s", t.Str("summary"))
		return errSilent
	default:
		a.IO.Failf("Failed: %s", t.Str("summary"))
		if e := t.Str("error"); e != "" {
			fmt.Fprintln(a.IO.Err, "  "+e)
		}
		a.IO.Hint("adaa tasks view %s", t.Str("id"))
		return errSilent
	}
}

// ---- Arguments -----------------------------------------------------------

// fields collects flags that were actually set into a request body, so an
// update never sends a field the person did not mention.
type fields map[string]any

func (f fields) str(cmd *cobra.Command, flag, key string, v string) {
	if cmd.Flags().Changed(flag) {
		if v == "" {
			f[key] = nil
		} else {
			f[key] = v
		}
	}
}

func (f fields) boolean(cmd *cobra.Command, flag, key string, v bool) {
	if cmd.Flags().Changed(flag) {
		f[key] = v
	}
}

func (f fields) integer(cmd *cobra.Command, flag, key string, v int) {
	if cmd.Flags().Changed(flag) {
		f[key] = v
	}
}

func (f fields) strings(cmd *cobra.Command, flag, key string, v []string) {
	if cmd.Flags().Changed(flag) {
		f[key] = v
	}
}

// setQuery adds a query parameter when the flag was set.
func setQuery(q url.Values, cmd *cobra.Command, flag, key, v string) {
	if cmd.Flags().Changed(flag) && v != "" {
		q.Set(key, v)
	}
}

// oneOf validates an enum flag value with a helpful message.
func oneOf(flag, v string, allowed ...string) error {
	if v == "" {
		return nil
	}
	for _, x := range allowed {
		if v == x {
			return nil
		}
	}
	return usagef("--%s must be one of: %s", flag, strings.Join(allowed, ", "))
}

// enumFlag registers shell completion for an enum flag.
func enumFlag(cmd *cobra.Command, flag string, values ...string) {
	_ = cmd.RegisterFlagCompletionFunc(flag, cobra.FixedCompletions(values, cobra.ShellCompDirectiveNoFileComp))
}
