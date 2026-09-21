package sysinfo

import (
	"bufio"
	"context"
	"os"
	"strings"
	"syscall"
)

func collectPlatform(ctx context.Context, info *Info) {
	dmi := func(f string) string {
		b, err := os.ReadFile("/sys/class/dmi/id/" + f)
		if err != nil {
			return ""
		}
		s := strings.TrimSpace(string(b))
		if junkDMI(s) {
			return ""
		}
		return s
	}
	info.Manufacturer = dmi("sys_vendor")
	info.Model = strings.TrimSpace(dmi("product_name") + " " + dmi("product_version"))
	// Reading the serial usually needs root; that is fine, it stays empty.
	info.SerialNumber = dmi("product_serial")
	info.OperatingSystem = osRelease()
	info.CPUModel, info.MemoryMB = procInfo()
	var st syscall.Statfs_t
	if syscall.Statfs("/", &st) == nil {
		info.StorageGB = int(uint64(st.Blocks) * uint64(st.Bsize) / 1e9)
	}
}

func junkDMI(s string) bool {
	l := strings.ToLower(s)
	return l == "" || strings.Contains(l, "to be filled") || strings.Contains(l, "default string") || l == "system product name" || l == "none" || l == "not specified"
}

func osRelease() string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return "Linux"
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return "Linux"
}

func procInfo() (cpu string, memMB int) {
	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if k, v, ok := strings.Cut(l, ":"); ok && strings.TrimSpace(k) == "model name" {
				cpu = strings.TrimSpace(v)
				break
			}
		}
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(l, "MemTotal:"); ok {
				memMB = atoiPrefix(strings.TrimSpace(v)) / 1024
			}
		}
	}
	return cpu, memMB
}
