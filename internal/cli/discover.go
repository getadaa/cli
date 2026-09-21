package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/getadaa/cli/internal/discover"
	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/sysinfo"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

// Swappable in tests, which must neither read this machine nor scan a network.
var (
	collectSysinfo = sysinfo.Collect
	scanNetwork    = discover.Scan
	localNetworks  = discover.LocalNetworks
)

func newDevicesRegisterCmd(a *App) *cobra.Command {
	var name, assign string
	var printOnly bool
	cmd := &cobra.Command{
		Use:   "register",
		Short: "Record the computer you are on",
		Long: `Record this computer as a device, reading its model, serial number, operating
system, CPU, memory, disk and network addresses for you.

If adaa already knows a device with the same serial number (or MAC address),
that device is updated instead of a duplicate being created. Run it on each
laptop when someone starts, and the inventory is done.`,
		Example: `  adaa devices register
  adaa devices register --assign me
  adaa devices register --print --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			info, _ := ui.Spin(a.IO, "Reading this computer's details…", func() (*sysinfo.Info, error) {
				return collectSysinfo(ctx), nil
			})
			if printOnly {
				if a.JSON {
					b, _ := json.Marshal(info)
					return a.PrintJSON(b)
				}
				a.renderSysinfo(info)
				return nil
			}

			devices, err := a.allDevices(ctx)
			if err != nil {
				return err
			}
			existing := matchThisComputer(devices, info)
			body := sysinfoBody(info)

			if existing != nil {
				if !a.JSON {
					a.renderSysinfo(info)
					fmt.Fprintln(a.IO.Err)
				}
				if name != "" {
					body["name"] = name
				}
				if err := a.Confirm(fmt.Sprintf("adaa already knows this computer as %q. Update it with these details?", existing.Str("name"))); err != nil {
					return err
				}
				id := existing.Str("id")
				resp, d, err := a.Send(ctx, http.MethodPatch, "/devices/"+id, body)
				if err != nil {
					return err
				}
				if assign != "" {
					pid, err := a.Resolve(ctx, kindPerson, assign)
					if err != nil {
						return err
					}
					if pid != existing.Str("assigned_person_id") {
						if resp, d, err = a.Send(ctx, http.MethodPost, "/devices/"+id+"/assign", map[string]any{"person_id": pid}); err != nil {
							return err
						}
					}
				}
				if a.JSON {
					return a.PrintJSON(resp.Body)
				}
				a.IO.Successf("Updated %s %s", d.Str("name"), a.IO.E().Dim(id))
				return nil
			}

			if name == "" {
				name = defaultComputerName(a, info)
				if a.IO.Interactive() && !a.Yes {
					if err := a.IO.Input("Name for this computer", "--name", &name, nil); err != nil {
						return err
					}
				}
			}
			body["name"] = name
			body["kind"] = "computer"
			body["status"] = "in_use"

			pid, err := a.registerHolder(ctx, assign)
			if err != nil {
				return err
			}
			if pid != "" {
				body["assigned_person_id"] = pid
			}
			if !a.JSON {
				a.renderSysinfo(info)
				fmt.Fprintln(a.IO.Err)
			}
			if err := a.Confirm(fmt.Sprintf("Record this computer as %q?", name)); err != nil {
				return err
			}
			path, err := a.OrgPath(ctx, "/devices")
			if err != nil {
				return err
			}
			resp, d, err := a.Send(ctx, http.MethodPost, path, body)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Recorded %s %s", d.Str("name"), a.IO.E().Dim(d.Str("id")))
			a.IO.Hint("adaa devices view %s", d.Str("id"))
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Name for this computer (default: its hostname)")
	cmd.Flags().StringVar(&assign, "assign", "", "Who uses it (email, name, id or 'me'); asked when omitted")
	cmd.Flags().BoolVar(&printOnly, "print", false, "Only show what was read from this computer; send nothing")
	_ = cmd.RegisterFlagCompletionFunc("assign", a.complete(kindPerson))
	return cmd
}

// registerHolder decides who the computer is assigned to: the flag, or a
// question with "me" first, since it is usually your own machine.
func (a *App) registerHolder(ctx context.Context, assign string) (string, error) {
	if assign != "" {
		return a.Resolve(ctx, kindPerson, assign)
	}
	if !a.IO.Interactive() || a.Yes {
		return "", nil
	}
	me, err := a.Identity(ctx)
	if err != nil {
		return "", err
	}
	choice, err := a.IO.Select("Who uses this computer?", "--assign", []ui.Option{
		{Label: "Me (" + me.Str("person.full_name") + ")", Value: "me"},
		{Label: "Someone else", Value: "other"},
		{Label: "Nobody in particular (shared or spare)", Value: "none"},
	})
	if err != nil {
		return "", err
	}
	switch choice {
	case "me":
		return me.Str("person.id"), nil
	case "other":
		return a.Resolve(ctx, kindPerson, "")
	}
	return "", nil
}

func defaultComputerName(a *App, info *sysinfo.Info) string {
	if info.Hostname != "" {
		return info.Hostname
	}
	if a.Cfg.Name != "" && info.Model != "" {
		return a.Cfg.Name + "'s " + info.Model
	}
	return "This computer"
}

// matchThisComputer finds the device record for this machine: by serial
// number, which is unique per organization, then by any shared MAC address.
func matchThisComputer(devices []obj.Obj, info *sysinfo.Info) obj.Obj {
	if info.SerialNumber != "" {
		for _, d := range devices {
			if strings.EqualFold(d.Str("serial_number"), info.SerialNumber) {
				return d
			}
		}
	}
	for _, d := range devices {
		for _, m := range d.Strings("mac_addresses") {
			for _, mine := range info.MACAddresses {
				if discover.NormalizeMAC(m) == mine {
					return d
				}
			}
		}
	}
	return nil
}

func sysinfoBody(info *sysinfo.Info) map[string]any {
	body := map[string]any{}
	set := func(k, v string) {
		if v != "" {
			body[k] = v
		}
	}
	set("hostname", info.Hostname)
	set("manufacturer", info.Manufacturer)
	set("model", info.Model)
	set("serial_number", info.SerialNumber)
	set("ip_address", info.IPAddress)
	if len(info.MACAddresses) > 0 {
		body["mac_addresses"] = info.MACAddresses
	}
	hw := map[string]any{}
	if info.CPUModel != "" {
		hw["cpu_model"] = info.CPUModel
	}
	if info.CPUCores > 0 {
		hw["cpu_cores"] = info.CPUCores
	}
	if info.MemoryMB >= 128 {
		hw["memory_mb"] = info.MemoryMB
	}
	if info.StorageGB > 0 {
		hw["storage_gb"] = info.StorageGB
	}
	if len(hw) > 0 {
		body["hardware"] = hw
	}
	comp := map[string]any{}
	if info.OperatingSystem != "" {
		comp["operating_system"] = info.OperatingSystem
	}
	if info.LastUser != "" {
		comp["last_user"] = info.LastUser
	}
	if len(comp) > 0 {
		body["computer"] = comp
	}
	return body
}

func (a *App) renderSysinfo(info *sysinfo.Info) {
	d := a.IO.NewDetail("This computer", "")
	d.Field("Hostname", info.Hostname)
	d.Field("Model", join(" ", info.Manufacturer, info.Model))
	d.Field("Serial", orNone(info.SerialNumber, a.IO.S().Yellow("not readable (on Linux this needs root)")))
	d.Field("OS", info.OperatingSystem)
	if info.CPUCores > 0 {
		d.Field("CPU", join(", ", info.CPUModel, fmt.Sprintf("%d cores", info.CPUCores)))
	}
	if info.MemoryMB > 0 {
		d.Field("Memory", ui.Bytes(int64(info.MemoryMB)<<20))
	}
	if info.StorageGB > 0 {
		d.Field("Disk", fmt.Sprintf("%d GB", info.StorageGB))
	}
	d.Field("IP", info.IPAddress)
	d.Field("MAC", strings.Join(info.MACAddresses, ", "))
	d.Field("User", info.LastUser)
	d.Render()
}

// discoveredHost is a scanned host with what adaa knows about it.
type discoveredHost struct {
	discover.Host
	DeviceID   string `json:"device_id,omitempty"`
	DeviceName string `json:"device_name,omitempty"`
}

func newDevicesDiscoverCmd(a *App) *cobra.Command {
	var cidrs []string
	var noNmap, add bool
	cmd := &cobra.Command{
		Use:     "discover",
		Aliases: []string{"scan"},
		Short:   "Find devices on your local network that adaa does not know about",
		Long: `Scan the networks this computer is connected to, compare what answers with
the devices adaa knows, and record the new ones.

It uses nmap when it is installed, and otherwise probes a handful of common
ports and reads this computer's ARP table. Only private networks this computer
is on are scanned (at most a /22 unless you pass --cidr), and nothing leaves
this computer except the devices you choose to record.

Only scan networks you are allowed to scan. On an office network that is
usually fine; on a hotel, café or client network it is not.`,
		Example: `  adaa devices discover
  adaa devices discover --cidr 192.168.10.0/24
  adaa devices discover --json
  adaa devices discover --add --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			nets, err := discoverNetworks(cidrs)
			if err != nil {
				return err
			}
			hosts, err := a.scanWithProgress(ctx, nets, !noNmap)
			if err != nil {
				return err
			}
			devices, err := a.allDevices(ctx)
			if err != nil {
				return err
			}
			found := matchDiscovered(hosts, devices)

			var fresh []*discoveredHost
			for n := range found {
				if found[n].DeviceID == "" && !found[n].Self {
					fresh = append(fresh, &found[n])
				}
			}

			if !a.JSON {
				a.renderDiscovered(found)
			}
			switch {
			case len(fresh) == 0:
				if !a.JSON {
					a.IO.Successf("Everything that answered is already known to adaa.")
				}
			case add:
				if err := a.Confirm(fmt.Sprintf("Record %d new device(s) in adaa?", len(fresh))); err != nil {
					return err
				}
				if err := a.recordDiscovered(ctx, fresh, false); err != nil {
					return err
				}
			case a.IO.Interactive() && !a.JSON:
				chosen, err := a.chooseDiscovered(fresh)
				if err != nil {
					return err
				}
				if err := a.recordDiscovered(ctx, chosen, true); err != nil {
					return err
				}
			default:
				if !a.JSON {
					a.IO.Hint("adaa devices discover --add --yes   (records all %d new devices)", len(fresh))
				}
			}
			for _, h := range found {
				if h.Self && h.DeviceID == "" && !a.JSON {
					a.IO.Hint("adaa devices register   (records this computer, with its serial number)")
					break
				}
			}
			if a.JSON {
				if found == nil {
					found = []discoveredHost{}
				}
				b, err := json.Marshal(found)
				if err != nil {
					return err
				}
				return a.PrintJSON(b)
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&cidrs, "cidr", nil, "Network to scan instead of this computer's own, e.g. 192.168.1.0/24 (repeatable)")
	cmd.Flags().BoolVar(&noNmap, "no-nmap", false, "Use the built-in scanner even when nmap is installed")
	cmd.Flags().BoolVar(&add, "add", false, "Record every new device with its guessed name and kind")
	return cmd
}

func discoverNetworks(cidrs []string) ([]netip.Prefix, error) {
	if len(cidrs) > 0 {
		nets, err := discover.ParseCIDRs(cidrs)
		if err != nil {
			return nil, usagef("--cidr: %v", err)
		}
		return nets, nil
	}
	return localNetworks()
}

func (a *App) scanWithProgress(ctx context.Context, nets []netip.Prefix, useNmap bool) ([]discover.Host, error) {
	var names []string
	for _, p := range nets {
		names = append(names, p.String())
	}
	title := "Scanning " + strings.Join(names, ", ")
	sp := a.IO.StartSpinner(title + "…")
	hosts, method, err := scanNetwork(ctx, discover.Options{
		Networks: nets,
		UseNmap:  useNmap,
		Progress: func(done, total int) {
			sp.Update(fmt.Sprintf("%s… %d/%d addresses", title, done, total))
		},
	})
	sp.Stop()
	if err != nil {
		return nil, err
	}
	if !a.JSON {
		a.IO.Infof("%s", a.IO.E().Dim(fmt.Sprintf("Scanned %s with %s: %d host(s) answered.", strings.Join(names, ", "), method, len(hosts))))
	}
	return hosts, nil
}

// matchDiscovered pairs scanned hosts with known devices, by MAC address
// first (the only thing that survives a DHCP renewal), then IP, then hostname.
func matchDiscovered(hosts []discover.Host, devices []obj.Obj) []discoveredHost {
	byMAC := map[string]obj.Obj{}
	byIP := map[string]obj.Obj{}
	byName := map[string]obj.Obj{}
	for _, d := range devices {
		for _, m := range d.Strings("mac_addresses") {
			if nm := discover.NormalizeMAC(m); nm != "" {
				byMAC[nm] = d
			}
		}
		if ip := d.Str("ip_address"); ip != "" {
			byIP[ip] = d
		}
		if hn := strings.ToLower(d.Str("hostname")); hn != "" {
			byName[hn] = d
		}
	}
	out := make([]discoveredHost, 0, len(hosts))
	for _, h := range hosts {
		dh := discoveredHost{Host: h}
		var d obj.Obj
		if h.MAC != "" {
			d = byMAC[h.MAC]
		}
		if d == nil {
			d = byIP[h.IP]
		}
		if d == nil && h.Hostname != "" {
			d = byName[strings.ToLower(h.Hostname)]
		}
		if d != nil {
			dh.DeviceID, dh.DeviceName = d.Str("id"), d.Str("name")
		}
		out = append(out, dh)
	}
	return out
}

func (a *App) renderDiscovered(found []discoveredHost) {
	if len(found) == 0 {
		a.IO.Infof("Nothing answered.")
		return
	}
	s := a.IO.S()
	t := a.IO.NewTable("IP", "MAC", "Vendor", "Name", "Kind", "In adaa").Flex(3)
	for _, h := range found {
		known := s.Yellow("new")
		switch {
		case h.DeviceID != "":
			known = s.Green("✓ " + h.DeviceName)
		case h.Self:
			known = s.Dim("this computer")
		}
		name := h.Hostname
		if h.Gateway {
			name = join(" ", name, s.Dim("(gateway)"))
		}
		t.Row(h.IP, h.MAC, h.Vendor, name, h.Kind, known)
	}
	t.Render()
}

func (a *App) chooseDiscovered(fresh []*discoveredHost) ([]*discoveredHost, error) {
	opts := make([]ui.Option, len(fresh))
	for n, h := range fresh {
		opts[n] = ui.Option{Label: join(" · ", h.IP, h.Hostname, h.Vendor, "looks like a "+h.Kind), Value: h.IP}
	}
	picked, err := a.IO.MultiSelect("Which of these should adaa know about? (space to select)", "--add", opts, nil)
	if err != nil {
		return nil, err
	}
	var out []*discoveredHost
	for _, ip := range picked {
		for _, h := range fresh {
			if h.IP == ip {
				out = append(out, h)
			}
		}
	}
	return out, nil
}

func discoveredName(h *discoveredHost) string {
	switch {
	case h.Hostname != "":
		return h.Hostname
	case h.Vendor != "":
		return h.Vendor + " at " + h.IP
	}
	return "Device at " + h.IP
}

// recordDiscovered creates a device for each host. With ask, each gets a
// chance to have its name and kind corrected first.
func (a *App) recordDiscovered(ctx context.Context, hosts []*discoveredHost, ask bool) error {
	path, err := a.OrgPath(ctx, "/devices")
	if err != nil {
		return err
	}
	kindOpts := make([]huh.Option[string], len(deviceKinds))
	for n, k := range deviceKinds {
		kindOpts[n] = huh.NewOption(k, k)
	}
	for _, h := range hosts {
		name, kind := discoveredName(h), h.Kind
		if ask {
			err := a.IO.Run(huh.NewGroup(
				huh.NewNote().Title(join(" · ", h.IP, h.MAC, h.Vendor)),
				huh.NewInput().Title("Name").Value(&name),
				huh.NewSelect[string]().Title("Kind").Options(kindOpts...).Value(&kind),
			))
			if err != nil {
				return err
			}
		}
		body := map[string]any{"kind": kind, "name": name, "status": "in_use", "managed": false, "ip_address": h.IP}
		if h.MAC != "" {
			body["mac_addresses"] = []string{h.MAC}
		}
		if h.Hostname != "" {
			body["hostname"] = h.Hostname
		}
		if h.Vendor != "" {
			body["manufacturer"] = h.Vendor
		}
		_, d, err := a.Send(ctx, http.MethodPost, path, body)
		if err != nil {
			return fmt.Errorf("recording %s: %w", h.IP, err)
		}
		h.DeviceID, h.DeviceName = d.Str("id"), d.Str("name")
		if !a.JSON {
			a.IO.Successf("Recorded %s %s", d.Str("name"), a.IO.E().Dim(d.Str("id")))
		}
	}
	return nil
}
