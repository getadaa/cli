package cli

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fastDevicePolling(t *testing.T) {
	orig := devicePollFloor
	devicePollFloor = 5 * time.Millisecond
	t.Cleanup(func() { devicePollFloor = orig })
}

func deviceStart(f *fakeAPI) {
	f.on("POST /auth/device", 201, map[string]any{
		"device_code": "adaa_dev_secret", "user_code": "BCDF-GHJK",
		"verification_uri": "https://adaa.no/enhet", "verification_uri_complete": "https://adaa.no/enhet?kode=BCDF-GHJK",
		"expires_at": time.Now().Add(time.Minute).UTC().Format(time.RFC3339), "interval_seconds": 0,
	})
}

func TestLoginWaitsForBrowserApproval(t *testing.T) {
	fastDevicePolling(t)
	f := newFakeAPI(t)
	deviceStart(f)
	var polls atomic.Int32
	f.onFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		switch polls.Add(1) {
		case 1:
			writeJSON(w, 400, problem(400, "authorization-pending", "Not approved yet"))
		case 2:
			writeJSON(w, 429, problem(429, "slow-down", "Slow down"))
		default:
			writeJSON(w, 201, map[string]any{"token": "adaa_sess_new", "expires_at": "2026-10-12T10:00:00Z"})
		}
	})
	r := f.run("login").mustSucceed(t)
	for _, want := range []string{"https://adaa.no/enhet?kode=BCDF-GHJK", "BCDF-GHJK", "Logged in as Kari Nordmann"} {
		if !strings.Contains(r.Stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, r.Stderr)
		}
	}
	start := f.requests("POST", "/auth/device")[0].Body
	if name, _ := start["client_name"].(string); !strings.HasPrefix(name, "adaa CLI") {
		t.Errorf("client_name: %v", start)
	}
	if b := f.requests("POST", "/auth/device/token")[0].Body; b["device_code"] != "adaa_dev_secret" {
		t.Errorf("token body: %v", b)
	}
}

func TestLoginRefusedInTheBrowser(t *testing.T) {
	fastDevicePolling(t)
	f := newFakeAPI(t)
	deviceStart(f)
	f.on("POST /auth/device/token", 403, problem(403, "device-authorization-denied", "Refused"))
	r := f.run("login")
	if r.Code == 0 || !strings.Contains(r.Stderr, "refused") {
		t.Fatalf("want a refusal, got %d:\n%s", r.Code, r.Stderr)
	}
}

// A server from before device sign-in answers 404; the emailed link still works.
func TestLoginFallsBackToEmailedLink(t *testing.T) {
	f := newFakeAPI(t)
	r := f.run("login")
	if r.Code != exitUsage || !strings.Contains(r.Stderr, "--email") {
		t.Fatalf("without a terminal the fallback should ask for --email, got %d:\n%s", r.Code, r.Stderr)
	}
	f.on("POST /auth/magic-link", 202, nil)
	f.run("login", "--email", "kari@firma.no").mustSucceed(t)
	if n := len(f.requests("POST", "/auth/magic-link")); n != 1 {
		t.Errorf("magic-link calls: %d", n)
	}
}
