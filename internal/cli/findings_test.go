package cli

import (
	"strings"
	"testing"
)

const testFinding = "fnd_01JATX3M4K7Q2YV8N0RCBEZ5HS"

func finding(status string) map[string]any {
	return map[string]any{
		"id": testFinding, "organization_id": testOrg, "source": "detected", "kind": "unused_license_seat",
		"severity": "warning", "status": status,
		"summary":                   map[string]any{"key": "finding.unused", "text": "A Microsoft 365 seat is paid for but unused."},
		"suggested_action":          map[string]any{"key": "x", "text": "Release the seat."},
		"desired":                   map[string]any{"key": "d", "text": "9 seats"},
		"observed":                  map[string]any{"key": "o", "text": "10 seats"},
		"cost_impact_monthly_minor": -24900,
		"first_seen_at":             "2026-09-01T10:00:00Z", "last_seen_at": "2026-09-20T10:00:00Z",
	}
}

func TestFindingsListHidesClosed(t *testing.T) {
	f := newFakeAPI(t)
	closed := finding("resolved")
	closed["id"] = "fnd_01JATX3M4K7Q2YV8N0RCBEZ5HT"
	closed["summary"] = map[string]any{"text": "Old news"}
	f.on("GET /organizations/"+testOrg+"/findings", 200, page(finding("open"), closed))
	r := f.run("findings", "list").mustSucceed(t)
	if !strings.Contains(r.Stdout, "Microsoft 365 seat") || strings.Contains(r.Stdout, "Old news") {
		t.Fatalf("unexpected list:\n%s", r.Stdout)
	}
	r = f.run("findings", "list", "--closed", "--json").mustSucceed(t)
	if !strings.Contains(r.Stdout, "Old news") {
		t.Fatalf("--closed should include resolved findings:\n%s", r.Stdout)
	}
	if f.run("findings", "list", "--severity", "bad").Code != exitUsage {
		t.Fatal("an unknown severity should be a usage error")
	}
}

func TestFindingsView(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /findings/"+testFinding, 200, finding("open"))
	r := f.run("show", testFinding).mustSucceed(t)
	for _, want := range []string{"Release the seat.", "9 seats", "10 seats", "−249,00 kr/month"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("view lacks %q:\n%s", want, r.Stdout)
		}
	}
}

func TestFindingsResolveNeedsYes(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST /findings/"+testFinding+"/resolve", 202, map[string]any{
		"summary": "Releasing one seat", "cost_delta": map[string]any{"currency": "NOK", "monthly_before_minor": 100, "monthly_after_minor": 0, "monthly_change_minor": -100},
		"tasks": []any{map[string]any{"id": "tsk_01JATX3M4K7Q2YV8N0RCBEZ5HS", "summary": "Release seat", "status": "queued"}},
	})
	if r := f.run("findings", "resolve", testFinding); r.Code != exitUsage {
		t.Fatalf("exit %d without --yes, want %d", r.Code, exitUsage)
	}
	r := f.run("findings", "resolve", testFinding, "--yes", "--direction", "adopt").mustSucceed(t)
	if !strings.Contains(r.Stderr, "Releasing one seat") {
		t.Errorf("stderr: %s", r.Stderr)
	}
	reqs := f.requests("POST", "/findings/"+testFinding+"/resolve")
	if len(reqs) != 1 || reqs[0].Body["direction"] != "adopt" || reqs[0].Body["dry_run"] != nil {
		t.Fatalf("requests: %+v", reqs)
	}
}

func TestFindingsIgnoreSendsOutcome(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST /findings/"+testFinding+"/ignore", 200, finding("closed"))
	f.run("findings", "ignore", testFinding, "--outcome", "accepted_risk", "--reason", "Kiosk").mustSucceed(t)
	reqs := f.requests("POST", "/findings/"+testFinding+"/ignore")
	if len(reqs) != 1 || reqs[0].Body["outcome"] != "accepted_risk" || reqs[0].Body["reason"] != "Kiosk" {
		t.Fatalf("requests: %+v", reqs)
	}
	if r := f.run("findings", "ignore", testFinding, "--outcome", "wont_fix"); r.Code != exitUsage {
		t.Fatalf("missing --reason without a terminal: exit %d", r.Code)
	}
}
