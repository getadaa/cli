package sysinfo

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestCollectLive(t *testing.T) {
	if os.Getenv("ADAA_LIVE_SYSINFO") == "" {
		t.Skip("set ADAA_LIVE_SYSINFO=1 to probe this machine")
	}
	b, _ := json.MarshalIndent(Collect(context.Background()), "", "  ")
	t.Log(string(b))
}
