// Package diag takes a plain-text snapshot of this computer, for attaching to
// a problem report. It only reads; nothing here changes the machine.
package diag

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Collect returns a human-readable report. apiHost is resolved to show
// whether name lookups work, which is the first thing a technician asks.
func Collect(ctx context.Context, apiHost string) string {
	var b strings.Builder
	line := func(label, value string) {
		if strings.TrimSpace(value) == "" {
			value = "(unknown)"
		}
		fmt.Fprintf(&b, "%-12s %s\n", label+":", value)
	}
	now := time.Now()
	hostname, _ := os.Hostname()

	b.WriteString("adaa diagnostics\n\n")
	line("Time", now.Format(time.RFC3339)+" ("+now.Location().String()+")")
	line("Hostname", hostname)
	line("System", runtime.GOOS+"/"+runtime.GOARCH+" "+osVersion(ctx))
	line("Uptime", uptime(ctx, now))
	line("User", os.Getenv("USER")+os.Getenv("USERNAME"))

	b.WriteString("\nDisk\n")
	b.WriteString(indent(diskFree(ctx)))

	b.WriteString("\nNetwork interfaces\n")
	b.WriteString(indent(interfaces()))

	b.WriteString("\nDefault route\n")
	b.WriteString(indent(defaultRoute(ctx)))

	b.WriteString("\nName lookup\n")
	b.WriteString(indent(lookup(ctx, apiHost)))
	return b.String()
}

func run(ctx context.Context, name string, args ...string) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func osVersion(ctx context.Context) string {
	switch runtime.GOOS {
	case "darwin":
		return "macOS " + run(ctx, "sw_vers", "-productVersion")
	case "linux":
		if b, err := os.ReadFile("/etc/os-release"); err == nil {
			for _, l := range strings.Split(string(b), "\n") {
				if v, ok := strings.CutPrefix(l, "PRETTY_NAME="); ok {
					return strings.Trim(v, `"`)
				}
			}
		}
	case "windows":
		return run(ctx, "cmd", "/c", "ver")
	}
	return ""
}

func uptime(ctx context.Context, now time.Time) string {
	switch runtime.GOOS {
	case "linux":
		if b, err := os.ReadFile("/proc/uptime"); err == nil {
			var secs float64
			if _, err := fmt.Sscanf(string(b), "%f", &secs); err == nil {
				return humanDuration(time.Duration(secs) * time.Second)
			}
		}
	case "darwin":
		// kern.boottime reads like "{ sec = 1726900000, usec = 0 } Sat Sep 21 ..."
		out := run(ctx, "sysctl", "-n", "kern.boottime")
		var sec int64
		if i := strings.Index(out, "sec = "); i >= 0 {
			if _, err := fmt.Sscanf(out[i+len("sec = "):], "%d", &sec); err == nil {
				return humanDuration(now.Sub(time.Unix(sec, 0)))
			}
		}
	}
	return ""
}

func humanDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if days > 0 {
		return fmt.Sprintf("%d days, %d hours", days, hours)
	}
	return fmt.Sprintf("%d hours, %d minutes", hours, int(d.Minutes())%60)
}

func diskFree(ctx context.Context) string {
	if runtime.GOOS == "windows" {
		return run(ctx, "powershell", "-NoProfile", "-Command", "Get-PSDrive -PSProvider FileSystem | Format-Table -AutoSize | Out-String")
	}
	return run(ctx, "df", "-h", "/")
}

func interfaces() string {
	ifs, err := net.Interfaces()
	if err != nil {
		return err.Error()
	}
	var b strings.Builder
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		var list []string
		for _, a := range addrs {
			// Link-local addresses are on every interface and say nothing
			// about whether the machine is actually connected.
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			list = append(list, a.String())
		}
		if len(list) == 0 {
			continue
		}
		mac := ifc.HardwareAddr.String()
		if mac != "" {
			mac = " [" + mac + "]"
		}
		fmt.Fprintf(&b, "%s%s: %s\n", ifc.Name, mac, strings.Join(list, ", "))
	}
	return b.String()
}

func defaultRoute(ctx context.Context) string {
	switch runtime.GOOS {
	case "darwin":
		out := run(ctx, "route", "-n", "get", "default")
		var keep []string
		for _, l := range strings.Split(out, "\n") {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "gateway:") || strings.HasPrefix(l, "interface:") {
				keep = append(keep, l)
			}
		}
		return strings.Join(keep, "\n")
	case "linux":
		return run(ctx, "ip", "route", "show", "default")
	case "windows":
		return run(ctx, "route", "print", "0.0.0.0")
	}
	return ""
}

func lookup(ctx context.Context, host string) string {
	if host == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	start := time.Now()
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	took := time.Since(start).Round(time.Millisecond)
	if err != nil {
		return fmt.Sprintf("%s: failed after %s: %v", host, took, err)
	}
	return fmt.Sprintf("%s: %s (%s)", host, strings.Join(addrs, ", "), took)
}

func indent(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return "  (unknown)\n"
	}
	return "  " + strings.ReplaceAll(s, "\n", "\n  ") + "\n"
}
