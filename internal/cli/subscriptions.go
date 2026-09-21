package cli

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/getadaa/cli/internal/obj"
	"github.com/spf13/cobra"
)

func init() { register(newSubscriptionsCmd) }

var subscriptionStatuses = []string{"pending", "active", "suspended", "cancelled"}

func newSubscriptionsCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "subscriptions",
		Aliases: []string{"subscription", "subs"},
		Short:   "What you pay for, and who holds each seat",
		Long: `Subscriptions are what adaa bills. Each shows what you pay for, what should
exist, and what actually exists, so a gap between them is visible at a glance.`,
		GroupID: groupCore,
	}
	cmd.AddCommand(newSubscriptionsListCmd(a), newSubscriptionsViewCmd(a), newSubscriptionsAssignmentsCmd(a),
		newSubscriptionsAssignCmd(a), newSubscriptionsReleaseCmd(a))
	return cmd
}

func newSubscriptionsListCmd(a *App) *cobra.Command {
	var lo ListOpts
	var status string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List subscriptions",
		Example: `  adaa subscriptions list
  adaa subscriptions list --status pending`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := oneOf("status", status, subscriptionStatuses...); err != nil {
				return err
			}
			path, err := a.OrgPath(ctx, "/subscriptions")
			if err != nil {
				return err
			}
			q := url.Values{}
			setQuery(q, cmd, "status", "status", status)
			l, err := a.List(ctx, path, q, lo)
			if err != nil {
				return err
			}
			return a.PrintListing(l, "No subscriptions.", []string{"Service", "Status", "Paid", "Exists", "Unit price", "ID"},
				func(s obj.Obj) []string {
					return []string{s.Str("service_name"), a.status(s.Str("status")), s.Str("quantity"),
						a.observedCell(s), money(s, "unit_price_minor"), a.dim(s.Str("id"))}
				})
		},
	}
	addListFlags(cmd, &lo)
	cmd.Flags().StringVar(&status, "status", "", "Only this status: pending, active, suspended or cancelled")
	enumFlag(cmd, "status", subscriptionStatuses...)
	return cmd
}

// observedCell highlights a subscription where what exists differs from what
// is paid for, which is exactly the drift a customer wants to spot.
func (a *App) observedCell(s obj.Obj) string {
	q, _ := s.Int("quantity")
	o, ok := s.Int("observed_count")
	if !ok {
		return ""
	}
	txt := fmt.Sprint(o)
	if o != q {
		return a.IO.S().Yellow(txt)
	}
	return txt
}

func newSubscriptionsViewCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "view [subscription]",
		Short:             "Show a subscription",
		Example:           "  adaa subscriptions view \"Microsoft 365 Business Standard\"",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindSubscription),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindSubscription, arg(args))
			if err != nil {
				return err
			}
			s, raw, err := a.Get(ctx, "/subscriptions/"+id, nil)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, s, func(s obj.Obj) {
				d := a.IO.NewDetail(s.Str("service_name"), s.Str("id"))
				d.Field("Status", a.status(s.Str("status")))
				d.Field("Service", s.Str("service_code"))
				d.Field("Billed", s.Str("billing_period"))
				d.Field("Unit price", orNone(money(s, "unit_price_minor"), "not quoted"))
				d.Field("Starts", s.Str("starts_on"))
				d.Field("Ends", s.Str("ends_on"))
				d.Section("Seats")
				d.Field("Paid for", s.Str("quantity"))
				d.Field("Should exist", s.Str("entitled_count"))
				d.Field("Assigned", s.Str("assigned_count"))
				d.Field("Exist", a.observedCell(s))
				if n := s.Str("notes"); n != "" {
					d.Section("Notes")
					d.Line(n)
				}
				d.Render()
				q, _ := s.Int("quantity")
				if o, ok := s.Int("observed_count"); ok && o != q {
					a.IO.Warnf("%d paid for, but %d exist.", q, o)
				}
			})
		},
	}
}

func newSubscriptionsAssignmentsCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "assignments [subscription]",
		Short:             "List who holds a seat",
		Example:           "  adaa subscriptions assignments \"Microsoft 365 Business Standard\"",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindSubscription),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindSubscription, arg(args))
			if err != nil {
				return err
			}
			resp, err := a.Do(ctx, http.MethodGet, "/subscriptions/"+id+"/assignments", nil, nil)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			o, err := obj.Parse(resp.Body)
			if err != nil {
				return err
			}
			l := &Listing{Items: o.List("items")}
			if err := a.PrintListing(l, "No seats are assigned to anyone.", []string{"Person", "Email", "Since", "Note", "Person ID"},
				func(s obj.Obj) []string {
					return []string{s.Str("person_name"), s.Str("person_email"), a.IO.When(s.Str("assigned_at")),
						s.Str("note"), a.dim(s.Str("person_id"))}
				}); err != nil {
				return err
			}
			a.IO.Infof("%s", a.dim(fmt.Sprintf("%s of %s seats assigned", o.Str("assigned_count"), o.Str("quantity"))))
			return nil
		},
	}
}

func newSubscriptionsAssignCmd(a *App) *cobra.Command {
	var note string
	cmd := &cobra.Command{
		Use:   "assign <subscription> <person>",
		Short: "Give a person a seat",
		Long: `Give a person a seat on a subscription. When every seat is taken this is
refused: buying one more is a decision, not a side effect.`,
		Example: "  adaa subscriptions assign \"Microsoft 365 Business Standard\" kari@firma.no",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindSubscription, args[0])
			if err != nil {
				return err
			}
			person, err := a.Resolve(ctx, kindPerson, args[1])
			if err != nil {
				return err
			}
			body := map[string]any{"person_id": person}
			if note != "" {
				body["note"] = note
			}
			resp, o, err := a.Send(ctx, http.MethodPost, "/subscriptions/"+id+"/assignments", body)
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
			a.IO.Successf("Assigned a seat to %s.", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "Why, for later")
	return cmd
}

func newSubscriptionsReleaseCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "release <subscription> <person>",
		Short: "Free a person's seat",
		Long: `Free a seat. This does not lower the bill: the number of seats paid for is
changed separately.`,
		Example: "  adaa subscriptions release \"Microsoft 365 Business Standard\" ola@firma.no",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindSubscription, args[0])
			if err != nil {
				return err
			}
			person, err := a.Resolve(ctx, kindPerson, args[1])
			if err != nil {
				return err
			}
			if err := a.Confirm("Free this person's seat? The bill stays the same."); err != nil {
				return err
			}
			if _, err := a.Do(ctx, http.MethodDelete, "/subscriptions/"+id+"/assignments/"+person, nil, nil); err != nil {
				return err
			}
			a.IO.Successf("Seat released.")
			return nil
		},
	}
}
