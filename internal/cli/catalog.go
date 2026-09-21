package cli

import (
	"net/url"
	"strings"

	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() {
	register(newServicesCmd)
	register(newProductsCmd)
}

var serviceCategories = []string{"license", "email", "backup", "hosting", "support", "onboarding", "network", "device"}

func newServicesCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "services",
		Aliases: []string{"service", "catalog"},
		Short:   "The catalog of services adaa sells, with prices",
		GroupID: groupCore,
	}
	cmd.AddCommand(newServicesListCmd(a), newServicesViewCmd(a))
	return cmd
}

func newServicesListCmd(a *App) *cobra.Command {
	var lo ListOpts
	var category string
	var inactive bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List catalog services",
		Example: `  adaa services list
  adaa services list --category license`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := oneOf("category", category, serviceCategories...); err != nil {
				return err
			}
			q := url.Values{}
			setQuery(q, cmd, "category", "category", category)
			if !inactive {
				q.Set("active", "true")
			}
			l, err := a.List(cmd.Context(), "/services", q, lo)
			if err != nil {
				return err
			}
			return a.PrintListing(l, "No services match.", []string{"Name", "Code", "Category", "Price", "ID"},
				func(s obj.Obj) []string {
					name := s.Str("name")
					if !s.Bool("active") {
						name += " " + a.dim("(withdrawn)")
					}
					return []string{name, s.Str("code"), s.Str("category"), servicePrice(s), a.dim(s.Str("id"))}
				})
		},
	}
	addListFlags(cmd, &lo)
	cmd.Flags().StringVar(&category, "category", "", "Only this category: "+strings.Join(serviceCategories, ", "))
	cmd.Flags().BoolVar(&inactive, "include-inactive", false, "Include services that are no longer sold")
	enumFlag(cmd, "category", serviceCategories...)
	return cmd
}

// servicePrice reads like "249,00 kr per person / month". A missing price
// means not yet quoted, which is not the same as free.
func servicePrice(s obj.Obj) string {
	n, ok := s.Int("unit_price_minor")
	if !ok {
		return "not quoted"
	}
	p := ui.Money(n, s.Str("currency"))
	if u := s.Str("pricing_unit"); u != "" && u != "flat" {
		p += " per " + strings.TrimPrefix(u, "per_")
	}
	switch s.Str("billing_period") {
	case "monthly":
		p += " / month"
	case "yearly":
		p += " / year"
	case "one_time":
		p += " once"
	}
	return p
}

func newServicesViewCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "view [service]",
		Short:             "Show a catalog service",
		Example:           "  adaa services view m365-business-standard",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindService),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindService, arg(args))
			if err != nil {
				return err
			}
			s, raw, err := a.Get(ctx, "/services/"+id, nil)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, s, func(s obj.Obj) {
				d := a.IO.NewDetail(s.Str("name"), s.Str("id"))
				d.Field("Code", s.Str("code"))
				d.Field("Category", s.Str("category"))
				d.Field("Price", servicePrice(s))
				d.Field("Vendor", s.Str("vendor"))
				d.Field("Product", s.Str("product_code"))
				if !s.Bool("active") {
					d.Field("Available", a.IO.S().Yellow("no longer sold"))
				}
				if desc := s.Str("description"); desc != "" {
					d.Section("About")
					d.Line(desc)
				}
				d.Render()
			})
		},
	}
}

// productCommands maps a product to the command that configures it.
var productCommands = map[string]string{
	"mail":      "adaa mail view",
	"workspace": "adaa workspaces list",
	"server":    "adaa servers list",
	"backup":    "adaa backups list",
	"license":   "adaa licenses list",
	"device":    "adaa devices list",
}

func newProductsCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "products",
		Short: "What adaa operates for you, and where to configure each",
		Long: `Products are the things adaa actually runs, like mail, servers and backups,
as opposed to services, which are lines on a bill.`,
		Example: "  adaa products",
		GroupID: groupProducts,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			l, err := a.ListItems(cmd.Context(), "/products", nil)
			if err != nil {
				return err
			}
			return a.PrintListing(l, "No products.", []string{"Product", "Code", "Configure with", "Description"},
				func(p obj.Obj) []string {
					return []string{p.Str("name"), p.Str("code"), productCommands[p.Str("code")], p.Str("description")}
				})
		},
	}
}
