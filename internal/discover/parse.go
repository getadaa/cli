package discover

import (
	"bufio"
	"context"
	"encoding/xml"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ParseNmapXML reads the hosts that are up from `nmap -oX` output.
func ParseNmapXML(b []byte) ([]Host, error) {
	var run struct {
		Hosts []struct {
			Status struct {
				State string `xml:"state,attr"`
			} `xml:"status"`
			Addresses []struct {
				Addr     string `xml:"addr,attr"`
				AddrType string `xml:"addrtype,attr"`
				Vendor   string `xml:"vendor,attr"`
			} `xml:"address"`
			Hostnames []struct {
				Name string `xml:"name,attr"`
			} `xml:"hostnames>hostname"`
			Ports []struct {
				PortID int `xml:"portid,attr"`
				State  struct {
					State string `xml:"state,attr"`
				} `xml:"state"`
			} `xml:"ports>port"`
		} `xml:"host"`
	}
	if err := xml.Unmarshal(b, &run); err != nil {
		return nil, err
	}
	var out []Host
	for _, h := range run.Hosts {
		if h.Status.State != "" && h.Status.State != "up" {
			continue
		}
		var host Host
		for _, a := range h.Addresses {
			switch a.AddrType {
			case "ipv4":
				host.IP = a.Addr
			case "mac":
				host.MAC = NormalizeMAC(a.Addr)
				host.Vendor = a.Vendor
			}
		}
		if host.IP == "" {
			continue
		}
		if len(h.Hostnames) > 0 {
			host.Hostname = strings.TrimSuffix(h.Hostnames[0].Name, ".local")
		}
		for _, p := range h.Ports {
			if p.State.State == "open" {
				host.OpenPorts = append(host.OpenPorts, p.PortID)
			}
		}
		out = append(out, host)
	}
	return out, nil
}

// ReadNeighbours returns the OS neighbour (ARP) table as IP → MAC.
func ReadNeighbours(ctx context.Context) map[string]string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out := func(name string, args ...string) string {
		b, _ := exec.CommandContext(ctx, name, args...).Output()
		return string(b)
	}
	switch runtime.GOOS {
	case "linux":
		if b, err := os.ReadFile("/proc/net/arp"); err == nil {
			if m := ParseProcARP(string(b)); len(m) > 0 {
				return m
			}
		}
		return ParseIPNeigh(out("ip", "neigh"))
	case "windows":
		return ParseWindowsARP(out("arp", "-a"))
	default:
		return ParseBSDARP(out("arp", "-an"))
	}
}

// ParseBSDARP reads macOS/BSD `arp -an`:
//
//	? (192.168.1.1) at 0:1b:2c:3d:4e:5f on en0 ifscope [ethernet]
func ParseBSDARP(s string) map[string]string {
	m := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[2] != "at" {
			continue
		}
		ip := strings.Trim(f[1], "()")
		if mac := NormalizeMAC(f[3]); mac != "" {
			m[ip] = mac
		}
	}
	return m
}

// ParseProcARP reads Linux /proc/net/arp. Flags 0x0 are incomplete entries.
func ParseProcARP(s string) map[string]string {
	m := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[0] == "IP" || f[2] == "0x0" {
			continue
		}
		if mac := NormalizeMAC(f[3]); mac != "" {
			m[f[0]] = mac
		}
	}
	return m
}

// ParseIPNeigh reads `ip neigh`:
//
//	192.168.1.1 dev eth0 lladdr 00:1b:2c:3d:4e:5f REACHABLE
func ParseIPNeigh(s string) map[string]string {
	m := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		for n := 0; n+1 < len(f); n++ {
			if f[n] == "lladdr" {
				if st := f[len(f)-1]; st == "FAILED" || st == "INCOMPLETE" {
					break
				}
				if mac := NormalizeMAC(f[n+1]); mac != "" {
					m[f[0]] = mac
				}
			}
		}
	}
	return m
}

// ParseWindowsARP reads `arp -a`:
//
//	192.168.1.1           00-1b-2c-3d-4e-5f     dynamic
func ParseWindowsARP(s string) map[string]string {
	m := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 3 || (f[2] != "dynamic" && f[2] != "static") {
			continue
		}
		if mac := NormalizeMAC(f[1]); mac != "" {
			m[f[0]] = mac
		}
	}
	return m
}

// DefaultGateway returns the IPv4 default gateway, or "".
func DefaultGateway(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	switch runtime.GOOS {
	case "linux":
		b, err := os.ReadFile("/proc/net/route")
		if err != nil {
			return ""
		}
		return parseProcRoute(string(b))
	case "windows":
		return ""
	default:
		b, _ := exec.CommandContext(ctx, "route", "-n", "get", "default").Output()
		return parseRouteGet(string(b))
	}
}

func parseRouteGet(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "gateway:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// parseProcRoute finds the default route in /proc/net/route, whose gateway is
// little-endian hex.
func parseProcRoute(s string) string {
	for _, l := range strings.Split(s, "\n") {
		f := strings.Fields(l)
		if len(f) < 3 || f[1] != "00000000" || len(f[2]) != 8 {
			continue
		}
		var b [4]byte
		for n := range 4 {
			var v byte
			for _, r := range f[2][n*2 : n*2+2] {
				v <<= 4
				switch {
				case r >= '0' && r <= '9':
					v |= byte(r - '0')
				case r >= 'A' && r <= 'F':
					v |= byte(r-'A') + 10
				case r >= 'a' && r <= 'f':
					v |= byte(r-'a') + 10
				}
			}
			b[3-n] = v
		}
		if b == [4]byte{} {
			continue
		}
		return strings.Join([]string{itoa(b[0]), itoa(b[1]), itoa(b[2]), itoa(b[3])}, ".")
	}
	return ""
}

func itoa(b byte) string {
	if b == 0 {
		return "0"
	}
	var s []byte
	for b > 0 {
		s = append([]byte{'0' + b%10}, s...)
		b /= 10
	}
	return string(s)
}
