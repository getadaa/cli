package sysinfo

import (
	"reflect"
	"testing"
)

func TestCleanMACsDropsRandomisedAddresses(t *testing.T) {
	got := cleanMACs([]string{"60:3E:5F:8D:C5:AA", "d2:4c:1b:d3:b9:04", "60:3e:5f:8d:c5:aa", "junk"})
	if want := []string{"60:3e:5f:8d:c5:aa"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestVirtual(t *testing.T) {
	for name, want := range map[string]bool{"en0": false, "eth0": false, "utun3": true, "docker0": true, "bridge0": true, "wlan0": false, "veth12ab": true} {
		if Virtual(name) != want {
			t.Errorf("Virtual(%q) = %v", name, !want)
		}
	}
}
