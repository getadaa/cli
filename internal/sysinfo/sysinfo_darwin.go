package sysinfo

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

func collectPlatform(ctx context.Context, info *Info) {
	info.Manufacturer = "Apple"
	parseSystemProfiler(run(ctx, "system_profiler", "SPHardwareDataType", "-json"), info)
	if v := run(ctx, "sw_vers", "-productVersion"); v != "" {
		info.OperatingSystem = "macOS " + v
	}
	if info.CPUModel == "" {
		info.CPUModel = run(ctx, "sysctl", "-n", "machdep.cpu.brand_string")
	}
	if n, err := strconv.ParseInt(run(ctx, "sysctl", "-n", "hw.memsize"), 10, 64); err == nil && n > 0 {
		info.MemoryMB = int(n / (1 << 20))
	}
	info.StorageGB = parseDfTotalGB(run(ctx, "df", "-k", "/"))
	// Wi-Fi reports a private address per network; networksetup knows the
	// hardware one.
	if macs := parseHardwarePorts(run(ctx, "networksetup", "-listallhardwareports")); len(macs) > 0 {
		info.MACAddresses = cleanMACs(append(info.MACAddresses, macs...))
	}
}

func parseHardwarePorts(out string) []string {
	var macs []string
	port := ""
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(l, "Hardware Port: "); ok {
			port = v
		}
		if v, ok := strings.CutPrefix(l, "Ethernet Address: "); ok && !strings.Contains(port, "Bridge") {
			macs = append(macs, v)
		}
	}
	return macs
}

func parseSystemProfiler(out string, info *Info) {
	var doc struct {
		Items []map[string]any `json:"SPHardwareDataType"`
	}
	if json.Unmarshal([]byte(out), &doc) != nil || len(doc.Items) == 0 {
		return
	}
	hw := doc.Items[0]
	str := func(k string) string { s, _ := hw[k].(string); return s }
	info.SerialNumber = str("serial_number")
	info.Model = strings.TrimSpace(str("machine_name") + " " + str("machine_model"))
	info.CPUModel = str("chip_type")
	if info.CPUModel == "" {
		info.CPUModel = str("cpu_type")
	}
	if mem := str("physical_memory"); strings.HasSuffix(mem, " GB") {
		info.MemoryMB = atoiPrefix(mem) * 1024
	}
}

// parseDfTotalGB reads the size column of `df -k /`.
func parseDfTotalGB(out string) int {
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		return 0
	}
	f := strings.Fields(lines[1])
	if len(f) < 2 {
		return 0
	}
	kb, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil {
		return 0
	}
	return int(kb / (1000 * 1000))
}
