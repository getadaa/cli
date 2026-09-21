package discover

import (
	"net/netip"
	"reflect"
	"testing"
)

const nmapXML = `<?xml version="1.0"?>
<nmaprun scanner="nmap">
<host><status state="up" reason="arp-response"/>
<address addr="192.168.1.1" addrtype="ipv4"/>
<address addr="00:1B:2C:3D:4E:5F" addrtype="mac" vendor="Ubiquiti Networks"/>
<hostnames><hostname name="gw.lan" type="PTR"/></hostnames>
</host>
<host><status state="up"/>
<address addr="192.168.1.20" addrtype="ipv4"/>
<address addr="3C:2A:F4:00:00:01" addrtype="mac" vendor="Brother Industries"/>
</host>
<host><status state="down"/><address addr="192.168.1.30" addrtype="ipv4"/></host>
</nmaprun>`

func TestParseNmapXML(t *testing.T) {
	hosts, err := ParseNmapXML([]byte(nmapXML))
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 2 {
		t.Fatalf("want 2 hosts up, got %+v", hosts)
	}
	if h := hosts[0]; h.IP != "192.168.1.1" || h.MAC != "00:1b:2c:3d:4e:5f" || h.Vendor != "Ubiquiti Networks" || h.Hostname != "gw.lan" {
		t.Fatalf("%+v", h)
	}
	if GuessKind(hosts[1]) != "printer" || GuessKind(hosts[0]) != "network" {
		t.Fatalf("kinds: %s %s", GuessKind(hosts[1]), GuessKind(hosts[0]))
	}
}

func TestParseNeighbourTables(t *testing.T) {
	bsd := `? (192.168.1.1) at 0:1b:2c:3d:4e:5f on en0 ifscope [ethernet]
? (192.168.1.9) at (incomplete) on en0 ifscope [ethernet]
? (192.168.1.255) at ff:ff:ff:ff:ff:ff on en0 ifscope [ethernet]`
	proc := `IP address       HW type     Flags       HW address            Mask     Device
192.168.1.1      0x1         0x2         00:1b:2c:3d:4e:5f     *        eth0
192.168.1.9      0x1         0x0         00:00:00:00:00:00     *        eth0`
	ipn := `192.168.1.1 dev eth0 lladdr 00:1b:2c:3d:4e:5f REACHABLE
192.168.1.9 dev eth0 FAILED
fe80::1 dev eth0 lladdr 00:1b:2c:3d:4e:60 router STALE`
	win := `Interface: 192.168.1.10 --- 0x5
  Internet Address      Physical Address      Type
  192.168.1.1           00-1b-2c-3d-4e-5f     dynamic
  192.168.1.255         ff-ff-ff-ff-ff-ff     static`
	want := map[string]string{"192.168.1.1": "00:1b:2c:3d:4e:5f"}
	for name, got := range map[string]map[string]string{
		"bsd": ParseBSDARP(bsd), "proc": ParseProcARP(proc), "windows": ParseWindowsARP(win),
	} {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v", name, got)
		}
	}
	if got := ParseIPNeigh(ipn); got["192.168.1.1"] != "00:1b:2c:3d:4e:5f" || got["192.168.1.9"] != "" {
		t.Errorf("ip neigh: %v", got)
	}
}

func TestGuessKind(t *testing.T) {
	cases := []struct {
		h    Host
		want string
	}{
		{Host{OpenPorts: []int{9100}}, "printer"},
		{Host{OpenPorts: []int{62078}}, "phone"},
		{Host{OpenPorts: []int{3389}}, "computer"},
		{Host{Gateway: true}, "network"},
		{Host{Self: true, OpenPorts: []int{22}}, "computer"},
		{Host{OpenPorts: []int{22, 443}}, "server"},
		{Host{Vendor: "Synology Incorporated"}, "server"},
		{Host{}, "other"},
	}
	for _, c := range cases {
		if got := GuessKind(c.h); got != c.want {
			t.Errorf("GuessKind(%+v) = %s, want %s", c.h, got, c.want)
		}
	}
}

func TestMergeNeighboursAddsSilentHostsInScope(t *testing.T) {
	nets := []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}
	hosts := mergeNeighbours([]Host{{IP: "192.168.1.20", OpenPorts: []int{80}}},
		map[string]string{"192.168.1.20": "aa:bb:cc:dd:ee:01", "192.168.1.5": "aa:bb:cc:dd:ee:02", "10.0.0.1": "aa:bb:cc:dd:ee:03"}, nets)
	if len(hosts) != 2 || hosts[0].IP != "192.168.1.5" || hosts[1].MAC != "aa:bb:cc:dd:ee:01" {
		t.Fatalf("%+v", hosts)
	}
}

func TestParseCIDRs(t *testing.T) {
	if _, err := ParseCIDRs([]string{"8.8.8.0/24"}); err == nil {
		t.Error("public network accepted")
	}
	if _, err := ParseCIDRs([]string{"10.0.0.0/8"}); err == nil {
		t.Error("huge network accepted")
	}
	p, err := ParseCIDRs([]string{"192.168.1.7/24", "10.1.2.3"})
	if err != nil || p[0].String() != "192.168.1.0/24" || p[1].String() != "10.1.2.3/32" {
		t.Fatalf("%v %v", p, err)
	}
}

func TestHostsIn(t *testing.T) {
	if n := len(hostsIn(netip.MustParsePrefix("192.168.1.0/24"))); n != 254 {
		t.Fatalf("got %d", n)
	}
}

func TestParseRoutes(t *testing.T) {
	if gw := parseProcRoute("Iface\tDestination\tGateway\neth0\t00000000\t0101A8C0\t0003\n"); gw != "192.168.1.1" {
		t.Errorf("proc route: %q", gw)
	}
	if gw := parseRouteGet("   route to: default\n    gateway: 172.20.10.1\n"); gw != "172.20.10.1" {
		t.Errorf("route get: %q", gw)
	}
}
