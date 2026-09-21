package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() { register(newRequestsCmd) }

var (
	requestStatuses = []string{"open", "in_progress", "waiting_on_customer", "resolved", "closed"}
	requestKinds    = []string{"change", "question", "order"}
)

func newRequestsCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "requests",
		Aliases: []string{"request", "req"},
		Short:   "Ask adaa for something, and talk it through",
		Long: `A request is for wanting something or asking something: a change, a question,
an order. Something being broken is not a request — use 'adaa report'.`,
		GroupID: groupCore,
	}
	cmd.AddCommand(newRequestsListCmd(a), newRequestsViewCmd(a), newRequestsNewCmd(a),
		newRequestsReplyCmd(a), newRequestsCloseCmd(a), newRequestsReopenCmd(a), newRequestsAttachCmd(a))
	return cmd
}

func newRequestsListCmd(a *App) *cobra.Command {
	var lo ListOpts
	var status string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List requests",
		Example: `  adaa requests list
  adaa requests list --status waiting_on_customer`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := oneOf("status", status, requestStatuses...); err != nil {
				return err
			}
			ctx := cmd.Context()
			path, err := a.OrgPath(ctx, "/requests")
			if err != nil {
				return err
			}
			q := url.Values{}
			setQuery(q, cmd, "status", "status", status)
			l, err := a.List(ctx, path, q, lo)
			if err != nil {
				return err
			}
			return a.PrintListing(l, "No requests. Ask for something with `adaa requests new`.",
				[]string{"Status", "Title", "Kind", "Messages", "Updated", "ID"},
				func(o obj.Obj) []string {
					return []string{a.status(o.Str("status")), o.Str("title"), o.Str("kind"), o.Str("message_count"),
						a.IO.When(o.Str("updated_at")), o.Str("id")}
				})
		},
	}
	addListFlags(cmd, &lo)
	cmd.Flags().StringVar(&status, "status", "", "Only requests in this status")
	enumFlag(cmd, "status", requestStatuses...)
	return cmd
}

func newRequestsViewCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:     "view [request]",
		Aliases: []string{"show"},
		Short:   "Show a request and its conversation",
		Long: `Show a request with its whole conversation and attachments.

With --json the output is {"request": …, "messages": […], "attachments": […]},
each as the API returns it.`,
		Example:           `  adaa requests view req_01JATX3M4K7Q2YV8N0RCBEZ5HS`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindRequest),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindRequest, arg(args))
			if err != nil {
				return err
			}
			r, raw, err := a.Get(ctx, "/requests/"+id, nil)
			if err != nil {
				return err
			}
			msgs, err := a.ListItems(ctx, "/requests/"+id+"/messages", nil)
			if err != nil {
				return err
			}
			atts, err := a.ListItems(ctx, "/requests/"+id+"/attachments", nil)
			if err != nil {
				atts = &Listing{}
			}
			if a.JSON {
				b, err := json.Marshal(map[string]any{"request": json.RawMessage(raw),
					"messages": nonNilRaw(msgs.Raw), "attachments": nonNilRaw(atts.Raw)})
				if err != nil {
					return err
				}
				return a.PrintJSON(b)
			}
			a.renderRequest(ctx, r, msgs.Items, atts.Items)
			return nil
		},
	}
}

func nonNilRaw(r []json.RawMessage) []json.RawMessage {
	if r == nil {
		return []json.RawMessage{}
	}
	return r
}

func (a *App) renderRequest(ctx context.Context, r obj.Obj, msgs, atts []obj.Obj) {
	s := a.IO.S()
	w := a.IO.Out
	fmt.Fprintf(w, "%s  %s\n", s.Bold(r.Str("title")), s.Dim(r.Str("id")))
	fmt.Fprintf(w, "  %s\n", join(s.Dim(" · "), a.status(r.Str("status")), r.Str("kind"),
		r.Str("severity"), "opened "+a.IO.When(r.Str("created_at"))))
	if f := r.Str("finding_id"); f != "" {
		fmt.Fprintf(w, "  %s %s\n", s.Dim("About finding"), f)
	}
	if tasks := r.Strings("task_ids"); len(tasks) > 0 {
		fmt.Fprintf(w, "  %s %s\n", s.Dim("Work raised"), strings.Join(tasks, ", "))
	}

	me := ""
	if id, err := a.Identity(ctx); err == nil {
		me = id.Str("person.id")
	}
	for _, m := range msgs {
		author := m.Str("author_name")
		if author == "" {
			author = m.Str("author_person_id")
		}
		if me != "" && m.Str("author_person_id") == me {
			author = s.Cyan(author + " (you)")
		} else {
			author = s.Bold(author)
		}
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%s  %s\n", author, s.Dim(a.IO.When(m.Str("created_at"))))
		fmt.Fprintln(w, "  "+strings.ReplaceAll(strings.TrimSpace(m.Str("body")), "\n", "\n  "))
	}
	if len(atts) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, s.Bold("Attachments"))
		for _, at := range atts {
			fmt.Fprintln(w, "  "+attachmentLine(a, at))
		}
	}
	switch r.Str("status") {
	case "resolved", "closed":
	default:
		a.IO.Hint("adaa requests reply %s", r.Str("id"))
	}
}

func newRequestsNewCmd(a *App) *cobra.Command {
	var kind, title, body, bodyFile, severity string
	cmd := &cobra.Command{
		Use:     "new",
		Aliases: []string{"create"},
		Short:   "Ask for a change, an order or an answer",
		Example: `  adaa requests new
  adaa requests new --kind order --title "Two new monitors for the meeting room" --body "27 inch, USB-C"
  adaa requests new --kind question --title "Can we get a guest Wi-Fi?" --body-file notes.md`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := firstErr(oneOf("kind", kind, requestKinds...), oneOf("severity", severity, findingSeverities...)); err != nil {
				return err
			}
			ctx := cmd.Context()
			if bodyFile != "" {
				b, err := readBodyFile(a, bodyFile)
				if err != nil {
					return err
				}
				body = b
			}
			if title == "" || body == "" {
				if !a.IO.Interactive() {
					missing := "--title"
					if title != "" {
						missing = "--body or --body-file"
					}
					return &ui.NoInputError{What: "the request", Flag: missing}
				}
				var fields []huh.Field
				if !cmd.Flags().Changed("kind") {
					fields = append(fields, huh.NewSelect[string]().Title("What kind of request?").Options(
						huh.NewOption("A question", "question"),
						huh.NewOption("A change to something you have", "change"),
						huh.NewOption("An order for something new", "order"),
					).Value(&kind))
				}
				fields = append(fields,
					huh.NewInput().Title("Title").Value(&title).Validate(nonEmpty),
					huh.NewText().Title("Details").Value(&body).Validate(nonEmpty))
				if err := a.IO.Run(huh.NewGroup(fields...)); err != nil {
					return err
				}
			}
			req := map[string]any{"title": title, "body": body}
			if kind != "" {
				req["kind"] = kind
			}
			if severity != "" {
				req["severity"] = severity
			}
			path, err := a.OrgPath(ctx, "/requests")
			if err != nil {
				return err
			}
			resp, r, err := a.Send(ctx, http.MethodPost, path, req)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Sent: %s %s", r.Str("title"), a.IO.E().Dim(r.Str("id")))
			a.IO.Hint("adaa requests view %s", r.Str("id"))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&kind, "kind", "", "question, change or order")
	f.StringVar(&title, "title", "", "One line saying what you want")
	f.StringVar(&body, "body", "", "The details")
	f.StringVar(&bodyFile, "body-file", "", "Read the details from a file, or - for stdin")
	f.StringVar(&severity, "severity", "", "How urgent it is: info, warning or critical")
	enumFlag(cmd, "kind", requestKinds...)
	enumFlag(cmd, "severity", findingSeverities...)
	cmd.MarkFlagsMutuallyExclusive("body", "body-file")
	return cmd
}

func readBodyFile(a *App, name string) (string, error) {
	var r io.Reader = a.IO.In
	if name != "-" {
		f, err := os.Open(name)
		if err != nil {
			return "", err
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(b)) == "" {
		return "", usagef("%s is empty", name)
	}
	return string(b), nil
}

func newRequestsReplyCmd(a *App) *cobra.Command {
	var bodyFile string
	cmd := &cobra.Command{
		Use:   "reply [request] [message]",
		Short: "Add a message to a request's conversation",
		Example: `  adaa requests reply req_01JATX3M4K7Q2YV8N0RCBEZ5HS "Tuesday works for us"
  adaa requests reply req_01JATX3M4K7Q2YV8N0RCBEZ5HS --body-file answer.md`,
		Args:              cobra.MaximumNArgs(2),
		ValidArgsFunction: a.complete(kindRequest),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindRequest, arg(args))
			if err != nil {
				return err
			}
			var body string
			switch {
			case len(args) == 2:
				body = args[1]
			case bodyFile != "":
				if body, err = readBodyFile(a, bodyFile); err != nil {
					return err
				}
			default:
				if !a.IO.Interactive() {
					return &ui.NoInputError{What: "the message", Flag: "the message as an argument, or --body-file"}
				}
				if err := a.IO.Run(huh.NewGroup(huh.NewText().Title("Your reply").Value(&body).Validate(nonEmpty))); err != nil {
					return err
				}
			}
			resp, _, err := a.Send(ctx, http.MethodPost, "/requests/"+id+"/messages", map[string]any{"body": body})
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Replied.")
			return nil
		},
	}
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "Read the message from a file, or - for stdin")
	return cmd
}

func newRequestsCloseCmd(a *App) *cobra.Command {
	var resolved bool
	cmd := &cobra.Command{
		Use:   "close [request]",
		Short: "Close a request you no longer need",
		Long: `Close a request. Use --resolved to say it was answered or done, rather than
just no longer needed. 'adaa requests reopen' brings it back.`,
		Example: `  adaa requests close req_01JATX3M4K7Q2YV8N0RCBEZ5HS
  adaa requests close req_01JATX3M4K7Q2YV8N0RCBEZ5HS --resolved`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindRequest),
		RunE: func(cmd *cobra.Command, args []string) error {
			status := "closed"
			if resolved {
				status = "resolved"
			}
			return a.setRequestStatus(cmd, arg(args), status)
		},
	}
	cmd.Flags().BoolVar(&resolved, "resolved", false, "Mark it resolved instead of closed")
	return cmd
}

func newRequestsReopenCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "reopen [request]",
		Short:             "Reopen a resolved or closed request",
		Example:           `  adaa requests reopen req_01JATX3M4K7Q2YV8N0RCBEZ5HS`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindRequest),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.setRequestStatus(cmd, arg(args), "open")
		},
	}
}

func (a *App) setRequestStatus(cmd *cobra.Command, input, status string) error {
	ctx := cmd.Context()
	id, err := a.Resolve(ctx, kindRequest, input)
	if err != nil {
		return err
	}
	resp, r, err := a.Send(ctx, http.MethodPatch, "/requests/"+id, map[string]any{"status": status})
	if err != nil {
		return err
	}
	if a.JSON {
		return a.PrintJSON(resp.Body)
	}
	a.IO.Successf("%s is now %s.", r.Str("title"), strings.ReplaceAll(r.Str("status"), "_", " "))
	return nil
}

func newRequestsAttachCmd(a *App) *cobra.Command {
	var caption string
	cmd := &cobra.Command{
		Use:     "attach <request> <file>...",
		Short:   "Attach files to a request",
		Example: `  adaa requests attach req_01JATX3M4K7Q2YV8N0RCBEZ5HS quote.pdf --caption "The quote we got"`,
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindRequest, args[0])
			if err != nil {
				return err
			}
			raw, err := a.attachFiles(ctx, "/requests/"+id+"/attachments", args[1:], caption)
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
