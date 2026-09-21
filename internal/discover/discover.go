// Package discover finds hosts on the networks this computer is attached to.
//
// It prefers nmap when installed, and otherwise does the same job with plain
// TCP connection attempts: a refused connection proves a host is there as well
// as an accepted one does, and every attempt makes the OS resolve the target's
// MAC address, which the neighbour table then gives back.
package discover

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/getadaa/cli/internal/sysinfo"
)

// Host is one address that answered.
type Host struct {
	IP        string `json:"ip"`
	MAC       string `json:"mac,omitempty"`
	Vendor    string `json:"vendor,omitempty"`
	Hostname  string `json:"hostname,omitempty"`
	OpenPorts []int  `json:"open_ports,omitempty"`
	Gateway   bool   `json:"gateway,omitempty"`
	Self      bool   `json:"self,omitempty"`
	Kind      string `json:"kind"`
}

// Probe ports are chosen because each says something about what a host is.
var Ports = []int{22, 80, 443, 445, 139, 631, 9100, 62078, 8080, 3389, 5900}

// MaxDefaultPrefix caps an automatically chosen network at a /22 (1022 hosts),
// so joining a large corporate or guest network does not start a long scan.
const MaxDefaultPrefix = 22

type Options struct {
	Networks []netip.Prefix
	// UseNmap uses nmap when it is on PATH.
	UseNmap bool
	// Progress is called as hosts are probed by the built-in scanner.
	Progress func(done, total int)
}

// LocalNetworks returns the private IPv4 networks of up, physical interfaces.
func LocalNetworks() ([]netip.Prefix, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []netip.Prefix
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 || sysinfo.Virtual(i.Name) {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil || !n.IP.IsPrivate() {
				continue
			}
			ones, _ := n.Mask.Size()
			addr, _ := netip.AddrFromSlice(n.IP.To4())
			p := netip.PrefixFrom(addr, max(ones, MaxDefaultPrefix)).Masked()
			if !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("this computer is not on a private IPv4 network; pass --cidr")
	}
	return out, nil
}

// ParseCIDRs parses --cidr values, refusing anything that is not private or is
// too large to scan in reasonable time.
func ParseCIDRs(values []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, v := range values {
		p, err := netip.ParsePrefix(strings.TrimSpace(v))
		if err != nil {
			a, aerr := netip.ParseAddr(strings.TrimSpace(v))
			if aerr != nil {
				return nil, fmt.Errorf("%q is not a network like 192.168.1.0/24", v)
			}
			p = netip.PrefixFrom(a, 32)
		}
		if !p.Addr().Is4() || !p.Addr().IsPrivate() {
			return nil, fmt.Errorf("%s is not a private IPv4 network; only local networks can be scanned", v)
		}
		if p.Bits() < 16 {
			return nil, fmt.Errorf("%s is too large; scan a /16 or smaller", v)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

// Scan finds live hosts. The method used ("nmap" or "built-in") is returned
// for the person to see.
func Scan(ctx context.Context, opts Options) ([]Host, string, error) {
	if len(opts.Networks) == 0 {
		return nil, "", errors.New("no networks to scan")
	}
	var hosts []Host
	method := "built-in"
	if path, err := exec.LookPath("nmap"); opts.UseNmap && err == nil {
		method = "nmap"
		args := []string{"-sn", "-oX", "-"}
		for _, p := range opts.Networks {
			args = append(args, p.String())
		}
		out, err := exec.CommandContext(ctx, path, args...).Output()
		if err != nil {
			return nil, method, fmt.Errorf("nmap failed: %w (try --no-nmap)", err)
		}
		if hosts, err = ParseNmapXML(out); err != nil {
			return nil, method, err
		}
	} else {
		hosts = probeAll(ctx, opts.Networks, opts.Progress)
	}
	if ctx.Err() != nil {
		return nil, method, ctx.Err()
	}
	hosts = mergeNeighbours(hosts, ReadNeighbours(ctx), opts.Networks)
	enrich(ctx, hosts)
	return hosts, method, nil
}

// probeAll connects to Ports on every address, with bounded concurrency.
func probeAll(ctx context.Context, nets []netip.Prefix, progress func(int, int)) []Host {
	var addrs []netip.Addr
	for _, p := range nets {
		addrs = append(addrs, hostsIn(p)...)
	}
	var (
		mu    sync.Mutex
		found []Host
		done  atomic.Int64
		wg    sync.WaitGroup
		sem   = make(chan struct{}, 128)
	)
	for _, a := range addrs {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(a netip.Addr) {
			defer wg.Done()
			defer func() { <-sem }()
			alive, open := probeHost(ctx, a)
			if alive {
				mu.Lock()
				found = append(found, Host{IP: a.String(), OpenPorts: open})
				mu.Unlock()
			}
			if progress != nil {
				progress(int(done.Add(1)), len(addrs))
			}
		}(a)
	}
	wg.Wait()
	return found
}

func probeHost(ctx context.Context, a netip.Addr) (alive bool, open []int) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	d := net.Dialer{Timeout: 700 * time.Millisecond}
	for _, port := range Ports {
		wg.Add(1)
		go func(port int) {
			defer wg.Done()
			c, err := d.DialContext(ctx, "tcp", netip.AddrPortFrom(a, uint16(port)).String())
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				c.Close()
				alive = true
				open = append(open, port)
			} else if errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(err.Error(), "refused") {
				alive = true
			}
		}(port)
	}
	wg.Wait()
	sort.Ints(open)
	return alive, open
}

// hostsIn lists the usable addresses in a network, without network and
// broadcast addresses.
func hostsIn(p netip.Prefix) []netip.Addr {
	p = p.Masked()
	if p.Bits() >= 31 {
		return []netip.Addr{p.Addr()}
	}
	var out []netip.Addr
	for a := p.Addr().Next(); p.Contains(a); a = a.Next() {
		if !p.Contains(a.Next()) {
			break
		}
		out = append(out, a)
	}
	return out
}

// mergeNeighbours adds MACs from the neighbour table, and adds hosts that only
// the neighbour table knows about: a device that answered ARP but has every
// probed port filtered is still there.
func mergeNeighbours(hosts []Host, neigh map[string]string, nets []netip.Prefix) []Host {
	index := map[string]int{}
	for n, h := range hosts {
		index[h.IP] = n
	}
	for ip, mac := range neigh {
		a, err := netip.ParseAddr(ip)
		if err != nil || !inAny(a, nets) {
			continue
		}
		if n, ok := index[ip]; ok {
			if hosts[n].MAC == "" {
				hosts[n].MAC = mac
			}
			continue
		}
		index[ip] = len(hosts)
		hosts = append(hosts, Host{IP: ip, MAC: mac})
	}
	sort.Slice(hosts, func(i, j int) bool {
		a, _ := netip.ParseAddr(hosts[i].IP)
		b, _ := netip.ParseAddr(hosts[j].IP)
		return a.Less(b)
	})
	return hosts
}

func inAny(a netip.Addr, nets []netip.Prefix) bool {
	for _, p := range nets {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// enrich fills in names, whether a host is the gateway or this computer, and
// a guess at what kind of device it is.
func enrich(ctx context.Context, hosts []Host) {
	gw := DefaultGateway(ctx)
	self := localAddrs()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 32)
	for n := range hosts {
		h := &hosts[n]
		h.Gateway = h.IP == gw
		if mac, ok := self[h.IP]; ok {
			h.Self = true
			if h.MAC == "" {
				h.MAC = mac
			}
		}
		if h.Hostname != "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			lctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
			defer cancel()
			if names, err := net.DefaultResolver.LookupAddr(lctx, h.IP); err == nil && len(names) > 0 {
				h.Hostname = strings.TrimSuffix(strings.TrimSuffix(names[0], "."), ".local")
			}
		}()
	}
	wg.Wait()
	for n := range hosts {
		hosts[n].Kind = GuessKind(hosts[n])
	}
}

func localAddrs() map[string]string {
	out := map[string]string{}
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				out[n.IP.String()] = strings.ToLower(i.HardwareAddr.String())
			}
		}
	}
	return out
}

var vendorKinds = []struct {
	kind  string
	words []string
}{
	{"printer", []string{"brother", "canon", "epson", "xerox", "lexmark", "kyocera", "ricoh", "konica", "sharp", "oki", "hewlett", "hp inc"}},
	{"network", []string{"ubiquiti", "cisco", "netgear", "tp-link", "tplink", "mikrotik", "aruba", "juniper", "fortinet", "zyxel", "d-link", "meraki", "sonicwall", "draytek", "eero", "unifi"}},
	{"server", []string{"synology", "qnap", "supermicro", "vmware"}},
	{"phone", []string{"yealink", "polycom", "snom", "grandstream", "gigaset"}},
	{"computer", []string{"dell", "lenovo", "intel corporate", "microsoft", "asustek", "micro-star", "framework"}},
}

// GuessKind maps what is known about a host to a DeviceKind. It is a guess the
// person confirms, so it errs towards "other" rather than a wrong answer.
func GuessKind(h Host) string {
	has := func(p int) bool { return slices.Contains(h.OpenPorts, p) }
	switch {
	case h.Self:
		return "computer"
	case h.Gateway:
		return "network"
	case has(9100) || has(631):
		return "printer"
	case has(62078):
		return "phone"
	}
	v := strings.ToLower(h.Vendor)
	for _, vk := range vendorKinds {
		for _, w := range vk.words {
			if v != "" && strings.Contains(v, w) {
				return vk.kind
			}
		}
	}
	switch {
	case has(3389) || (has(445) && !has(22)) || has(5900):
		return "computer"
	case has(22) && (has(80) || has(443)):
		return "server"
	}
	return "other"
}

// NormalizeMAC turns "a:b:c:d:e:f", "0A-0B-…" and friends into "0a:0b:…",
// the form stored on devices.
func NormalizeMAC(s string) string {
	s = strings.TrimSpace(strings.ToLower(strings.ReplaceAll(s, "-", ":")))
	parts := strings.Split(s, ":")
	if len(parts) != 6 {
		return ""
	}
	for n, p := range parts {
		if len(p) == 0 || len(p) > 2 {
			return ""
		}
		if len(p) == 1 {
			parts[n] = "0" + p
		}
		for _, r := range parts[n] {
			if !strings.ContainsRune("0123456789abcdef", r) {
				return ""
			}
		}
	}
	mac := strings.Join(parts, ":")
	if mac == "ff:ff:ff:ff:ff:ff" || mac == "00:00:00:00:00:00" {
		return ""
	}
	return mac
}
