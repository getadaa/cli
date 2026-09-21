// Package sysinfo describes the computer adaa is running on, so it can be
// recorded as a device without anyone typing a serial number off a sticker.
//
// Every probe is best-effort: a field that cannot be read is left empty, and
// collection never fails because of one.
package sysinfo

import (
	"context"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"
)

type Info struct {
	Hostname        string   `json:"hostname,omitempty"`
	Manufacturer    string   `json:"manufacturer,omitempty"`
	Model           string   `json:"model,omitempty"`
	SerialNumber    string   `json:"serial_number,omitempty"`
	OperatingSystem string   `json:"operating_system,omitempty"`
	CPUModel        string   `json:"cpu_model,omitempty"`
	CPUCores        int      `json:"cpu_cores,omitempty"`
	MemoryMB        int      `json:"memory_mb,omitempty"`
	StorageGB       int      `json:"storage_gb,omitempty"`
	MACAddresses    []string `json:"mac_addresses,omitempty"`
	IPAddress       string   `json:"ip_address,omitempty"`
	LastUser        string   `json:"last_user,omitempty"`
}

// Collect gathers what it can within a few seconds.
func Collect(ctx context.Context) *Info {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	info := &Info{CPUCores: runtime.NumCPU()}
	info.Hostname, _ = os.Hostname()
	info.Hostname = strings.TrimSuffix(info.Hostname, ".local")
	info.MACAddresses, info.IPAddress = interfaces()
	if u := os.Getenv("USER"); u != "" {
		info.LastUser = u
	} else {
		info.LastUser = os.Getenv("USERNAME")
	}
	collectPlatform(ctx, info)
	return info
}

func run(ctx context.Context, name string, args ...string) string {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// interfaces returns the burned-in MACs of physical interfaces, and the first
// private IPv4 address this machine has.
func interfaces() ([]string, string) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, ""
	}
	var macs []string
	ip := ""
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback != 0 || Virtual(i.Name) {
			continue
		}
		if i.Flags&net.FlagUp != 0 && ip == "" {
			addrs, _ := i.Addrs()
			for _, a := range addrs {
				if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && n.IP.IsPrivate() {
					ip = n.IP.String()
					break
				}
			}
		}
		if len(i.HardwareAddr) == 6 {
			macs = append(macs, i.HardwareAddr.String())
		}
	}
	return cleanMACs(macs), ip
}

// cleanMACs lowercases, deduplicates and drops locally administered
// addresses: those are randomised per network (private Wi-Fi addresses,
// virtual adapters) and do not identify the hardware.
func cleanMACs(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range in {
		hw, err := net.ParseMAC(strings.TrimSpace(m))
		if err != nil || len(hw) != 6 || hw[0]&0x02 != 0 {
			continue
		}
		s := hw.String()
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// Virtual reports interface names that belong to VPNs, containers and
// hypervisors rather than to the machine's own network hardware.
func Virtual(name string) bool {
	n := strings.ToLower(name)
	for _, p := range []string{"utun", "tun", "tap", "docker", "br-", "veth", "virbr", "vmnet", "vboxnet", "awdl", "llw", "bridge", "anpi", "ap1", "gif", "stf", "zt", "tailscale", "wg", "lo", "cni", "flannel", "podman", "vethernet", "vmware", "virtualbox", "hyper-v"} {
		if strings.HasPrefix(n, p) || strings.Contains(n, p+" ") {
			return true
		}
	}
	return false
}

func atoiPrefix(s string) int {
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}
