package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() { register(newDevicesCmd) }

var (
	deviceKinds      = []string{"computer", "server", "network", "printer", "phone", "peripheral", "other"}
	deviceStatuses   = []string{"in_stock", "in_use", "in_repair", "retired", "lost", "stolen"}
	deviceOwnerships = []string{"company_owned", "leased", "personal", "adaa_owned"}
)

func newDevicesCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "devices",
		Aliases: []string{"device", "dev"},
		Short:   "Computers, printers, phones and network gear",
		Long: `Physical hardware: who holds it, what it is, and when its warranty runs out.

Hardware is never deleted. Retired, lost and stolen are statuses, because
"what happened to that laptop" has to stay answerable.`,
		GroupID: groupCore,
	}
	cmd.AddCommand(
		newDevicesListCmd(a), newDevicesViewCmd(a), newDevicesAddCmd(a), newDevicesEditCmd(a),
		newDevicesAssignCmd(a), newDevicesReturnCmd(a), newDevicesSignalsCmd(a),
		newDevicesRegisterCmd(a), newDevicesDiscoverCmd(a),
	)
	return cmd
}

func newDevicesListCmd(a *App) *cobra.Command {
	var lo ListOpts
	var kind, status, person string
	var unassigned bool
	var warranty int
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List devices",
		Example: `  adaa devices list
  adaa devices list --kind computer --unassigned
  adaa devices list --person kari@firma.no
  adaa devices list --warranty-expiring-within 90`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := oneOf("kind", kind, deviceKinds...); err != nil {
				return err
			}
			if err := oneOf("status", status, deviceStatuses...); err != nil {
				return err
			}
			q := url.Values{}
			setQuery(q, cmd, "kind", "kind", kind)
			setQuery(q, cmd, "status", "status", status)
			if unassigned {
				q.Set("unassigned", "true")
			}
			if person != "" {
				id, err := a.Resolve(ctx, kindPerson, person)
				if err != nil {
					return err
				}
				q.Set("person_id", id)
			}
			path, err := a.OrgPath(ctx, "/devices")
			if err != nil {
				return err
			}
			l, err := a.List(ctx, path, q, lo)
			if err != nil {
				return err
			}
			if warranty > 0 {
				// The API has no warranty filter, so this narrows what was fetched.
				until := time.Now().AddDate(0, 0, warranty).Format("2006-01-02")
				l = filterListing(l, func(d obj.Obj) bool {
					w := d.Str("warranty_expires_on")
					return w != "" && w <= until
				})
			}
			var names map[string]string
			if !a.JSON {
				names = a.personNamesBy(ctx, l.Items, "assigned_person_id")
			}
			return a.PrintListing(l, "No devices match.", []string{"Name", "Kind", "Status", "Holder", "Model", "Serial", "ID"}, func(d obj.Obj) []string {
				return []string{d.Str("name"), d.Str("kind"), a.status(d.Str("status")), devHolder(d, names),
					join(" ", d.Str("manufacturer"), d.Str("model")), d.Str("serial_number"), a.dim(d.Str("id"))}
			})
		},
	}
	addListFlags(cmd, &lo)
	f := cmd.Flags()
	f.StringVar(&kind, "kind", "", "Only this kind: "+strings.Join(deviceKinds, ", "))
	f.StringVar(&status, "status", "", "Only this status: "+strings.Join(deviceStatuses, ", "))
	f.StringVar(&person, "person", "", "Only devices held by this person (email, name, id or 'me')")
	f.BoolVar(&unassigned, "unassigned", false, "Only devices nobody holds")
	f.IntVar(&warranty, "warranty-expiring-within", 0, "Only devices whose warranty ends within this many days (or has ended)")
	enumFlag(cmd, "kind", deviceKinds...)
	enumFlag(cmd, "status", deviceStatuses...)
	_ = cmd.RegisterFlagCompletionFunc("person", a.complete(kindPerson))
	return cmd
}

func newDevicesViewCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "view [device]",
		Short:             "Show one device",
		Example:           "  adaa devices view kari-macbook\n  adaa devices view C02XK1ZXJGH5",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindDevice),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindDevice, arg(args))
			if err != nil {
				return err
			}
			d, raw, err := a.Get(ctx, "/devices/"+id, nil)
			if err != nil {
				return err
			}
			return a.PrintObj(raw, d, a.renderDevice)
		},
	}
}

func (a *App) renderDevice(d obj.Obj) {
	s := a.IO.S()
	v := a.IO.NewDetail(d.Str("name"), d.Str("id"))
	v.Field("Kind", d.Str("kind"))
	v.Field("Status", a.status(d.Str("status")))
	v.Field("Ownership", strings.ReplaceAll(d.Str("ownership"), "_", " "))
	if d.Bool("managed") {
		v.Field("Managed", "by adaa")
	}
	holder := devHolder(d, a.personNamesBy(context.Background(), []obj.Obj{d}, "assigned_person_id"))
	if holder != "" && d.Str("assigned_at") != "" {
		holder += s.Dim(" since " + a.IO.When(d.Str("assigned_at")))
	}
	v.Field("Held by", holder)
	v.Field("Location", d.Str("location"))
	v.Field("Model", join(" ", d.Str("manufacturer"), d.Str("model")))
	v.Field("Serial", d.Str("serial_number"))
	v.Field("Asset tag", d.Str("asset_tag"))

	v.Section("Network")
	v.Field("Hostname", d.Str("hostname"))
	v.Field("IP", d.Str("ip_address"))
	v.Field("MAC", strings.Join(d.Strings("mac_addresses"), ", "))

	v.Section("Hardware")
	hw := d.Obj("hardware")
	if hw != nil {
		v.Field("CPU", join(", ", hw.Str("cpu_model"), devPlural(hw, "cpu_cores", "core")))
		if mb, ok := hw.Int("memory_mb"); ok {
			v.Field("Memory", ui.Bytes(mb<<20))
		}
		if gb, ok := hw.Int("storage_gb"); ok {
			v.Field("Storage", join(" ", strconv.FormatInt(gb, 10)+" GB", strings.ToUpper(hw.Str("storage_type"))))
		}
	}
	if c := d.Obj("computer"); c != nil {
		v.Field("OS", c.Str("operating_system"))
		v.Field("Encryption", devOnOff(s, c, "encryption_enabled"))
		v.Field("Firewall", devOnOff(s, c, "firewall_enabled"))
		if n, ok := c.Int("patch_age_days"); ok {
			age := fmt.Sprintf("%d days behind", n)
			if n > 30 {
				age = s.Yellow(age)
			}
			v.Field("Updates", age)
		}
		v.Field("Last user", c.Str("last_user"))
	}
	if n := d.Obj("network"); n != nil {
		v.Field("Firmware", n.Str("firmware_version"))
		v.Field("Management", n.Str("management_ip"))
		v.Field("Ports", n.Str("port_count"))
	}
	if d.Bool("agent_installed") {
		seen := d.Str("agent_last_seen_at")
		if seen == "" {
			v.Field("Agent", s.Yellow("installed, never reported"))
		} else {
			v.Field("Agent", "last seen "+a.IO.When(seen))
		}
	}

	v.Section("Purchase")
	v.Field("Supplier", d.Str("supplier"))
	v.Field("Bought", d.Str("purchased_on"))
	if age, ok := d.Int("age_months"); ok {
		v.Field("Age", fmt.Sprintf("%d months", age))
	}
	v.Field("Price", money(d, "purchase_price_minor"))
	v.Field("Warranty", warrantyText(s, d.Str("warranty_expires_on"), time.Now()))
	if n, _ := d.Int("credential_count"); n > 0 {
		v.Field("Credentials", fmt.Sprintf("%d stored", n))
	}
	if notes := d.Str("notes"); notes != "" {
		v.Section("Notes")
		v.Line(notes)
	}
	v.Render()
	if n, _ := d.Int("credential_count"); n > 0 {
		a.IO.Hint("adaa credentials list --device %s", d.Str("id"))
	}
}

// devHolder names who holds a device. The API gives only the person's id, so
// names come from a lookup, falling back to the id.
func devHolder(d obj.Obj, names map[string]string) string {
	id := d.Str("assigned_person_id")
	if n := names[id]; n != "" {
		return n
	}
	return orNone(d.Str("assigned_person_name"), id)
}

func devPlural(o obj.Obj, field, unit string) string {
	n, ok := o.Int(field)
	if !ok {
		return ""
	}
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func devOnOff(s ui.Palette, o obj.Obj, field string) string {
	if !o.Has(field) {
		return ""
	}
	if o.Bool(field) {
		return s.Green("on")
	}
	return s.Red("off")
}

// warrantyText says how long is left, and shouts when it is nearly gone.
func warrantyText(s ui.Palette, date string, now time.Time) string {
	if date == "" {
		return ""
	}
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	days := int(t.Sub(now.Truncate(24*time.Hour)).Hours() / 24)
	switch {
	case days < 0:
		return s.Red("expired " + date)
	case days <= 90:
		return s.Yellow(fmt.Sprintf("until %s (%d days left)", date, days))
	}
	return "until " + date
}

// deviceFlags are the writable fields shared by add and edit.
type deviceFlags struct {
	name, hostname, status, ownership, location, manufacturer, model, serial, assetTag string
	ip, supplier, purchasedOn, price, warranty, notes                                  string
	macs                                                                               []string
	managed                                                                            bool
}

func (df *deviceFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&df.name, "name", "", "What a person would call it, e.g. \"Kari's laptop\"")
	f.StringVar(&df.hostname, "hostname", "", "Network hostname")
	f.StringVar(&df.status, "status", "", "Status: "+strings.Join(deviceStatuses, ", "))
	f.StringVar(&df.ownership, "ownership", "", "Who owns it: "+strings.Join(deviceOwnerships, ", "))
	f.BoolVar(&df.managed, "managed", false, "Whether adaa manages it")
	f.StringVar(&df.location, "location", "", "Where it physically is")
	f.StringVar(&df.manufacturer, "manufacturer", "", "Manufacturer")
	f.StringVar(&df.model, "model", "", "Model")
	f.StringVar(&df.serial, "serial", "", "Serial number")
	f.StringVar(&df.assetTag, "asset-tag", "", "Asset tag")
	f.StringArrayVar(&df.macs, "mac", nil, "MAC address (repeatable)")
	f.StringVar(&df.ip, "ip", "", "IP address")
	f.StringVar(&df.supplier, "supplier", "", "Where it was bought")
	f.StringVar(&df.purchasedOn, "purchased-on", "", "Purchase date, YYYY-MM-DD")
	f.StringVar(&df.price, "price", "", "Purchase price in kroner, e.g. 12999.00")
	f.StringVar(&df.warranty, "warranty-until", "", "Warranty end date, YYYY-MM-DD")
	f.StringVar(&df.notes, "notes", "", "Free-text notes")
	enumFlag(cmd, "status", deviceStatuses...)
	enumFlag(cmd, "ownership", deviceOwnerships...)
}

func (df *deviceFlags) body(cmd *cobra.Command) (fields, error) {
	if err := oneOf("status", df.status, deviceStatuses...); err != nil {
		return nil, err
	}
	if err := oneOf("ownership", df.ownership, deviceOwnerships...); err != nil {
		return nil, err
	}
	for flag, v := range map[string]string{"purchased-on": df.purchasedOn, "warranty-until": df.warranty} {
		if err := devValidDate(flag, v); err != nil {
			return nil, err
		}
	}
	b := fields{}
	b.str(cmd, "name", "name", df.name)
	b.str(cmd, "hostname", "hostname", df.hostname)
	b.str(cmd, "status", "status", df.status)
	b.str(cmd, "ownership", "ownership", df.ownership)
	b.boolean(cmd, "managed", "managed", df.managed)
	b.str(cmd, "location", "location", df.location)
	b.str(cmd, "manufacturer", "manufacturer", df.manufacturer)
	b.str(cmd, "model", "model", df.model)
	b.str(cmd, "serial", "serial_number", df.serial)
	b.str(cmd, "asset-tag", "asset_tag", df.assetTag)
	b.str(cmd, "ip", "ip_address", df.ip)
	b.str(cmd, "supplier", "supplier", df.supplier)
	b.str(cmd, "purchased-on", "purchased_on", df.purchasedOn)
	b.str(cmd, "warranty-until", "warranty_expires_on", df.warranty)
	b.str(cmd, "notes", "notes", df.notes)
	if cmd.Flags().Changed("mac") {
		macs := []string{}
		for _, m := range df.macs {
			for _, part := range strings.Split(m, ",") {
				if p := strings.TrimSpace(part); p != "" {
					macs = append(macs, strings.ToLower(p))
				}
			}
		}
		b["mac_addresses"] = macs
	}
	if cmd.Flags().Changed("price") {
		minor, err := devParseMinor(df.price)
		if err != nil {
			return nil, usagef("--price: %v", err)
		}
		b["purchase_price_minor"] = minor
	}
	return b, nil
}

func devValidDate(flag, v string) error {
	if v == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return usagef("--%s must be a date like 2026-10-01", flag)
	}
	return nil
}

// devParseMinor turns "12 999,50" or "12999.50" into minor units.
func devParseMinor(s string) (int64, error) {
	s = strings.NewReplacer(" ", "", " ", "", "kr", "", "NOK", "").Replace(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, ",", ".")
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 2 {
		return 0, fmt.Errorf("%q has more than two decimals", s)
	}
	for len(frac) < 2 {
		frac += "0"
	}
	n, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%q is not an amount", s)
	}
	return n, nil
}

func newDevicesAddCmd(a *App) *cobra.Command {
	var df deviceFlags
	var kind, assign string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Record a device",
		Long: `Record a device adaa should know about.

To record the computer you are sitting at, 'adaa devices register' reads the
model, serial number and specs for you. To find what else is on the office
network, use 'adaa devices discover'.`,
		Example: `  adaa devices add
  adaa devices add --kind printer --name "Office printer" --location "2nd floor"
  adaa devices add --kind computer --name "Kari's laptop" --serial C02XK1ZXJGH5 --assign kari@firma.no`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := oneOf("kind", kind, deviceKinds...); err != nil {
				return err
			}
			if kind == "" {
				opts := make([]ui.Option, len(deviceKinds))
				for n, k := range deviceKinds {
					opts[n] = ui.Option{Label: k, Value: k}
				}
				var err error
				if kind, err = a.IO.Select("What kind of device?", "--kind", opts); err != nil {
					return err
				}
			}
			if err := a.need(&df.name, "Name", "--name", nil); err != nil {
				return err
			}
			body, err := df.body(cmd)
			if err != nil {
				return err
			}
			body["kind"] = kind
			body["name"] = df.name
			if assign != "" {
				pid, err := a.Resolve(ctx, kindPerson, assign)
				if err != nil {
					return err
				}
				body["assigned_person_id"] = pid
				if _, ok := body["status"]; !ok {
					body["status"] = "in_use"
				}
			}
			path, err := a.OrgPath(ctx, "/devices")
			if err != nil {
				return err
			}
			resp, d, err := a.Send(ctx, http.MethodPost, path, map[string]any(body))
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Recorded %s %s", d.Str("name"), a.IO.E().Dim(d.Str("id")))
			return nil
		},
	}
	df.register(cmd)
	cmd.Flags().StringVar(&kind, "kind", "", "Kind: "+strings.Join(deviceKinds, ", "))
	cmd.Flags().StringVar(&assign, "assign", "", "Hand it to this person (email, name, id or 'me')")
	enumFlag(cmd, "kind", deviceKinds...)
	_ = cmd.RegisterFlagCompletionFunc("assign", a.complete(kindPerson))
	return cmd
}

func newDevicesEditCmd(a *App) *cobra.Command {
	var df deviceFlags
	var kind string
	cmd := &cobra.Command{
		Use:   "edit [device]",
		Short: "Change a device's details",
		Long: `Change a device's details. Only the flags you pass are changed; pass an empty
value (--location "") to clear a field.

Who holds a device is changed with 'adaa devices assign' and 'adaa devices return'.
A device that was lost, stolen or retired is marked with --status; those are
final.`,
		Example: `  adaa devices edit kari-macbook --location "Oslo office"
  adaa devices edit C02XK1ZXJGH5 --status in_repair --notes "Screen replaced"
  adaa devices edit C02XK1ZXJGH5 --status stolen --notes "Reported to police, case 123"`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindDevice),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			body, err := df.body(cmd)
			if err != nil {
				return err
			}
			if err := oneOf("kind", kind, deviceKinds...); err != nil {
				return err
			}
			body.str(cmd, "kind", "kind", kind)
			if len(body) == 0 {
				return usagef("nothing to change; pass at least one flag, such as --name or --status")
			}
			id, err := a.Resolve(ctx, kindDevice, arg(args))
			if err != nil {
				return err
			}
			if st := df.status; st == "lost" || st == "stolen" || st == "retired" {
				if err := a.Confirm(fmt.Sprintf("Mark this device %s? That cannot be undone.", st)); err != nil {
					return err
				}
			}
			resp, d, err := a.Send(ctx, http.MethodPatch, "/devices/"+id, map[string]any(body))
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Updated %s", d.Str("name"))
			return nil
		},
	}
	df.register(cmd)
	cmd.Flags().StringVar(&kind, "kind", "", "What it is: "+strings.Join(deviceKinds, ", "))
	enumFlag(cmd, "kind", deviceKinds...)
	return cmd
}

func newDevicesAssignCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "assign [device] [person]",
		Short: "Hand a device to a person",
		Example: `  adaa devices assign kari-macbook kari@firma.no
  adaa devices assign C02XK1ZXJGH5 me`,
		Args:              cobra.MaximumNArgs(2),
		ValidArgsFunction: a.complete(kindDevice),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindDevice, arg(args))
			if err != nil {
				return err
			}
			person := ""
			if len(args) > 1 {
				person = args[1]
			}
			pid, err := a.Resolve(ctx, kindPerson, person)
			if err != nil {
				return err
			}
			resp, d, err := a.Send(ctx, http.MethodPost, "/devices/"+id+"/assign", map[string]any{"person_id": pid})
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("%s is now held by %s", d.Str("name"), devHolder(d, a.personNamesBy(ctx, []obj.Obj{d}, "assigned_person_id")))
			return nil
		},
	}
	return cmd
}

func newDevicesReturnCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "return [device]",
		Short: "Take a device back from the person holding it",
		Long: `Take a device back and put it in stock, so somebody else can have it.

A device that was lost, stolen or retired is not returned; mark it with
'adaa devices edit <device> --status stolen' instead.`,
		Example:           `  adaa devices return kari-macbook`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindDevice),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindDevice, arg(args))
			if err != nil {
				return err
			}
			resp, d, err := a.Send(ctx, http.MethodPost, "/devices/"+id+"/return", nil)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("%s is back, now %s", d.Str("name"), strings.ReplaceAll(d.Str("status"), "_", " "))
			return nil
		},
	}
}

func newDevicesSignalsCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:               "signals [device]",
		Short:             "Show what has been measured on a device",
		Example:           "  adaa devices signals kari-macbook",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.complete(kindDevice),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := a.Resolve(ctx, kindDevice, arg(args))
			if err != nil {
				return err
			}
			l, err := a.ListItems(ctx, "/devices/"+id+"/signals", nil)
			if err != nil {
				return err
			}
			return a.PrintListing(l, "Nothing has been measured on this device.", []string{"Status", "Kind", "Summary", "Observed"}, func(s obj.Obj) []string {
				return []string{a.status(s.Str("status")), s.Str("kind"), s.Str("summary"), a.IO.When(s.Str("observed_at"))}
			})
		},
	}
}

// allDevices fetches every device, for matching against what is on this
// machine or on the network.
func (a *App) allDevices(ctx context.Context) ([]obj.Obj, error) {
	path, err := a.OrgPath(ctx, "/devices")
	if err != nil {
		return nil, err
	}
	l, err := a.List(ctx, path, nil, ListOpts{All: true})
	if err != nil {
		return nil, err
	}
	return l.Items, nil
}
