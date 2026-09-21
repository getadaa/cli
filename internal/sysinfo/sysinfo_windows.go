package sysinfo

import (
	"context"
	"encoding/json"
)

func collectPlatform(ctx context.Context, info *Info) {
	const script = `$cs=Get-CimInstance Win32_ComputerSystem; $b=Get-CimInstance Win32_BIOS; $o=Get-CimInstance Win32_OperatingSystem; $p=Get-CimInstance Win32_Processor | Select-Object -First 1; $d=Get-CimInstance Win32_LogicalDisk -Filter "DeviceID='C:'";
@{manufacturer=$cs.Manufacturer; model=$cs.Model; serial=$b.SerialNumber; os=($o.Caption + ' ' + $o.Version); cpu=$p.Name; mem=[int64]$cs.TotalPhysicalMemory; disk=[int64]$d.Size} | ConvertTo-Json -Compress`
	parsePowerShell(run(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script), info)
}

func parsePowerShell(out string, info *Info) {
	var v struct {
		Manufacturer string `json:"manufacturer"`
		Model        string `json:"model"`
		Serial       string `json:"serial"`
		OS           string `json:"os"`
		CPU          string `json:"cpu"`
		Mem          int64  `json:"mem"`
		Disk         int64  `json:"disk"`
	}
	if json.Unmarshal([]byte(out), &v) != nil {
		return
	}
	info.Manufacturer, info.Model, info.SerialNumber = v.Manufacturer, v.Model, v.Serial
	info.OperatingSystem, info.CPUModel = v.OS, v.CPU
	info.MemoryMB = int(v.Mem / (1 << 20))
	info.StorageGB = int(v.Disk / 1e9)
}
