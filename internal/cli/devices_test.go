package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/getadaa/cli/internal/discover"
	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/sysinfo"
	"github.com/getadaa/cli/internal/ui"
)

const testDevice = "dev_01JATX3M4K7Q2YV8N0RCBEZ5HS"

func sampleDevice() map[string]any {
	return map[string]any{
		"id": testDevice, "organization_id": testOrg, "kind": "computer", "name": "Kari's MacBook",
		"status": "in_use", "ownership": "company_owned", "assigned_person_name": "Kari Nordmann",
		"manufacturer": "Apple", "model": "MacBook Pro", "serial_number": "C02XK1ZXJGH5",
		"mac_addresses": []string{"60:3e:5f:8d:c5:aa"}, "ip_address": "192.168.1.20", "hostname": "karis-mbp",
		"hardware":            map[string]any{"cpu_model": "Apple M3", "cpu_cores": 8, "memory_mb": 16384},
		"warranty_expires_on": "2020-01-01",
		"created_at":          "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z",
	}
}

func TestDevicesListFilters(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/devices", 200, page(sampleDevice()))
	r := f.run("devices", "list", "--kind", "computer", "--unassigned").mustSucceed(t)
	if !strings.Contains(r.Stdout, "Kari's MacBook") || !strings.Contains(r.Stdout, "C02XK1ZXJGH5") {
		t.Fatalf("stdout: %s", r.Stdout)
	}
	q := f.requests("GET", "/organizations/"+testOrg+"/devices")[0].Query
	for _, want := range []string{"kind=computer", "unassigned=true"} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q lacks %s", q, want)
		}
	}
}

func TestDevicesListFiltersWarrantyLocally(t *testing.T) {
	f := newFakeAPI(t)
	soon := sampleDevice()
	soon["name"], soon["warranty_expires_on"] = "Soon", time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	later := sampleDevice()
	later["name"], later["warranty_expires_on"] = "Later", time.Now().AddDate(2, 0, 0).Format("2006-01-02")
	f.on("GET /organizations/"+testOrg+"/devices", 200, page(soon, later))
	r := f.run("devices", "list", "--warranty-expiring-within", "90").mustSucceed(t)
	if !strings.Contains(r.Stdout, "Soon") || strings.Contains(r.Stdout, "Later") {
		t.Fatalf("stdout: %s", r.Stdout)
	}
}

func TestDevicesListRejectsUnknownKind(t *testing.T) {
	f := newFakeAPI(t)
	if r := f.run("devices", "list", "--kind", "toaster"); r.Code != exitUsage {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
}

func TestDevicesViewBySerial(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/devices", 200, page(sampleDevice()))
	f.on("GET /devices/"+testDevice, 200, sampleDevice())
	r := f.run("devices", "view", "c02xk1zxjgh5").mustSucceed(t)
	for _, want := range []string{"Kari's MacBook", "Apple M3, 8 cores", "16.0 GiB", "expired 2020-01-01"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("view lacks %q:\n%s", want, r.Stdout)
		}
	}
}

func TestShowDispatchesToDevicesView(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /devices/"+testDevice, 200, sampleDevice())
	r := f.run("show", testDevice).mustSucceed(t)
	if !strings.Contains(r.Stdout, "Kari's MacBook") {
		t.Fatalf("stdout: %s", r.Stdout)
	}
}

func TestWarrantyText(t *testing.T) {
	s := ui.Test(nil, nil, nil).S()
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	if got := warrantyText(s, "2026-10-21", now); !strings.Contains(got, "30 days left") {
		t.Errorf("got %q", got)
	}
	if got := warrantyText(s, "2028-01-01", now); got != "until 2028-01-01" {
		t.Errorf("got %q", got)
	}
}

func TestDevParseMinor(t *testing.T) {
	for in, want := range map[string]int64{"12999": 1299900, "12 999,50": 1299950, "149.9": 14990, "0.05": 5} {
		if got, err := devParseMinor(in); err != nil || got != want {
			t.Errorf("devParseMinor(%q) = %d, %v", in, got, err)
		}
	}
	if _, err := devParseMinor("1.234"); err == nil {
		t.Error("three decimals accepted")
	}
}

func stubSysinfo(t *testing.T, info *sysinfo.Info) {
	t.Helper()
	old := collectSysinfo
	collectSysinfo = func(context.Context) *sysinfo.Info { return info }
	t.Cleanup(func() { collectSysinfo = old })
}

func thisMac() *sysinfo.Info {
	return &sysinfo.Info{Hostname: "makkie", Manufacturer: "Apple", Model: "MacBook Pro Mac15,10", SerialNumber: "J7YH64JXH2",
		OperatingSystem: "macOS 26.5.2", CPUModel: "Apple M3 Max", CPUCores: 14, MemoryMB: 36864, StorageGB: 994,
		MACAddresses: []string{"60:3e:5f:8d:c5:bb"}, IPAddress: "192.168.1.30", LastUser: "kari"}
}

func TestDevicesRegisterCreates(t *testing.T) {
	stubSysinfo(t, thisMac())
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/devices", 200, page())
	f.on("POST /organizations/"+testOrg+"/devices", 201, map[string]any{"id": testDevice, "name": "makkie"})
	f.run("devices", "register", "--assign", "me", "--yes").mustSucceed(t)
	reqs := f.requests("POST", "/organizations/"+testOrg+"/devices")
	if len(reqs) != 1 {
		t.Fatalf("want one create, got %d", len(reqs))
	}
	b := obj.Obj(reqs[0].Body)
	if b.Str("serial_number") != "J7YH64JXH2" || b.Str("kind") != "computer" || b.Str("assigned_person_id") != testPerson ||
		b.Str("hardware.memory_mb") != "36864" || b.Str("computer.operating_system") != "macOS 26.5.2" || b.Str("name") != "makkie" {
		t.Fatalf("body: %v", reqs[0].Body)
	}
}

func TestDevicesRegisterUpdatesKnownSerial(t *testing.T) {
	stubSysinfo(t, thisMac())
	f := newFakeAPI(t)
	existing := sampleDevice()
	existing["serial_number"] = "j7yh64jxh2"
	f.on("GET /organizations/"+testOrg+"/devices", 200, page(existing))
	f.on("PATCH /devices/"+testDevice, 200, existing)
	f.run("devices", "register", "--yes").mustSucceed(t)
	if n := len(f.requests("PATCH", "/devices/"+testDevice)); n != 1 {
		t.Fatalf("want one update, got %d", n)
	}
	if n := len(f.requests("POST", "/organizations/"+testOrg+"/devices")); n != 0 {
		t.Fatalf("created a duplicate")
	}
}

func TestDevicesRegisterNeedsYesWithoutTerminal(t *testing.T) {
	stubSysinfo(t, thisMac())
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/devices", 200, page())
	if r := f.run("devices", "register"); r.Code != exitUsage || !strings.Contains(r.Stderr, "--yes") {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
}

func TestDevicesRegisterPrintSendsNothing(t *testing.T) {
	stubSysinfo(t, thisMac())
	f := newFakeAPI(t)
	r := f.run("devices", "register", "--print", "--json").mustSucceed(t)
	var info sysinfo.Info
	if err := json.Unmarshal([]byte(r.Stdout), &info); err != nil || info.SerialNumber != "J7YH64JXH2" {
		t.Fatalf("%v: %s", err, r.Stdout)
	}
}

func stubScan(t *testing.T, hosts []discover.Host) {
	t.Helper()
	oldScan, oldNets := scanNetwork, localNetworks
	scanNetwork = func(context.Context, discover.Options) ([]discover.Host, string, error) { return hosts, "test", nil }
	t.Cleanup(func() { scanNetwork, localNetworks = oldScan, oldNets })
}

func TestMatchDiscovered(t *testing.T) {
	devices := []obj.Obj{
		{"id": "dev_a", "name": "Router", "mac_addresses": []any{"0:1B:2C:3D:4E:5F"}},
		{"id": "dev_b", "name": "Printer", "ip_address": "192.168.1.20"},
		{"id": "dev_c", "name": "NAS", "hostname": "NAS.lan"},
	}
	got := matchDiscovered([]discover.Host{
		{IP: "192.168.1.1", MAC: "00:1b:2c:3d:4e:5f"},
		{IP: "192.168.1.20"},
		{IP: "192.168.1.40", Hostname: "nas.lan"},
		{IP: "192.168.1.50", MAC: "aa:bb:cc:dd:ee:ff"},
	}, devices)
	want := []string{"dev_a", "dev_b", "dev_c", ""}
	for n, h := range got {
		if h.DeviceID != want[n] {
			t.Errorf("%s matched %q, want %q", h.IP, h.DeviceID, want[n])
		}
	}
}

func TestDevicesDiscoverAddsNewHosts(t *testing.T) {
	stubScan(t, []discover.Host{
		{IP: "192.168.1.1", MAC: "00:1b:2c:3d:4e:5f", Vendor: "Ubiquiti", Kind: "network", Gateway: true},
		{IP: "192.168.1.20", MAC: "3c:2a:f4:00:00:01", Vendor: "Brother", Kind: "printer"},
		{IP: "192.168.1.30", Kind: "computer", Self: true},
	})
	f := newFakeAPI(t)
	known := map[string]any{"id": "dev_a", "name": "Router", "mac_addresses": []string{"00:1b:2c:3d:4e:5f"}}
	f.on("GET /organizations/"+testOrg+"/devices", 200, page(known))
	f.on("POST /organizations/"+testOrg+"/devices", 201, map[string]any{"id": "dev_new", "name": "Brother at 192.168.1.20"})

	r := f.run("devices", "discover", "--cidr", "192.168.1.0/24", "--json").mustSucceed(t)
	if n := len(f.requests("POST", "/organizations/"+testOrg+"/devices")); n != 0 {
		t.Fatalf("recorded without --add")
	}
	var out []map[string]any
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil || len(out) != 3 || out[0]["device_id"] != "dev_a" {
		t.Fatalf("%v: %s", err, r.Stdout)
	}

	f.run("devices", "discover", "--cidr", "192.168.1.0/24", "--add", "--yes").mustSucceed(t)
	reqs := f.requests("POST", "/organizations/"+testOrg+"/devices")
	if len(reqs) != 1 {
		t.Fatalf("want only the printer recorded (router known, self skipped), got %d", len(reqs))
	}
	b := obj.Obj(reqs[0].Body)
	if b.Str("kind") != "printer" || b.Str("manufacturer") != "Brother" || b.Str("mac_addresses") != "3c:2a:f4:00:00:01" || b.Str("managed") != "no" {
		t.Fatalf("body: %v", reqs[0].Body)
	}
}

func TestDevicesDiscoverRejectsPublicCIDR(t *testing.T) {
	f := newFakeAPI(t)
	if r := f.run("devices", "discover", "--cidr", "8.8.8.0/24"); r.Code != exitUsage {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
}
