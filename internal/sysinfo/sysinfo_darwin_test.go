package sysinfo

import "testing"

func TestParseSystemProfiler(t *testing.T) {
	var info Info
	parseSystemProfiler(`{"SPHardwareDataType":[{"chip_type":"Apple M3 Max","machine_model":"Mac15,10","machine_name":"MacBook Pro","physical_memory":"36 GB","serial_number":"J7YH64JXH2"}]}`, &info)
	if info.SerialNumber != "J7YH64JXH2" || info.Model != "MacBook Pro Mac15,10" || info.MemoryMB != 36864 || info.CPUModel != "Apple M3 Max" {
		t.Fatalf("%+v", info)
	}
}

func TestParseHardwarePortsSkipsBridges(t *testing.T) {
	out := "Hardware Port: Thunderbolt Bridge\nDevice: bridge0\nEthernet Address: 36:a7:86:6d:90:40\n\nHardware Port: Wi-Fi\nDevice: en0\nEthernet Address: 60:3e:5f:8d:c5:aa\n"
	got := parseHardwarePorts(out)
	if len(got) != 1 || got[0] != "60:3e:5f:8d:c5:aa" {
		t.Fatalf("%v", got)
	}
}
