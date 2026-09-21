package cli

import (
	"strings"
	"testing"
)

const (
	testServer = "srv_01JATX3M4K7Q2YV8N0RCBEZ5HS"
	testTask   = "tsk_01JATX3M4K7Q2YV8N0RCBEZ5HS"
)

func testServerObj() map[string]any {
	return map[string]any{
		"id": testServer, "organization_id": testOrg, "hostname": "files01", "status": "running",
		"platform_kind": "linux", "os_version": "Ubuntu 24.04", "purpose": "File shares",
		"cpu_cores": 2, "memory_mb": 4096, "storage_gb": 200, "storage_used_gb": 190, "access_count": 3,
		"maintenance": map[string]any{"up_to_date": false, "updates_pending": 4, "reboot_required": true},
		"created_at":  "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z",
	}
}

func TestServersList(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/servers", 200, page(testServerObj()))
	r := f.run("servers", "list").mustSucceed(t)
	for _, want := range []string{"files01", "running", "2 vCPU", "4 pending, reboot"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("lacks %q:\n%s", want, r.Stdout)
		}
	}
}

func TestServersViewByHostname(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/servers", 200, page(testServerObj()))
	f.on("GET /servers/"+testServer, 200, testServerObj())
	r := f.run("servers", "view", "files01").mustSucceed(t)
	for _, want := range []string{"190 of 200 GB (95%)", "Ubuntu 24.04", "3 people"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("lacks %q:\n%s", want, r.Stdout)
		}
	}
}

func TestServersOrder(t *testing.T) {
	f := newFakeAPI(t)
	path := "/organizations/" + testOrg + "/servers"
	f.on("POST "+path, 202, map[string]any{"summary": "Ordering files02", "tasks": []any{}, "cost_delta": map[string]any{"currency": "NOK", "monthly_before_minor": 0, "monthly_after_minor": 50000, "monthly_change_minor": 50000}})
	args := []string{"servers", "order", "--hostname", "files02", "--platform", "linux", "--cores", "2", "--memory", "4", "--storage", "100"}

	if r := f.run(args...); r.Code != exitUsage {
		t.Fatalf("without --yes: exit %d, want %d: %s", r.Code, exitUsage, r.Stderr)
	}
	if n := len(f.requests("POST", path)); n != 0 {
		t.Fatalf("sent %d requests without confirmation", n)
	}

	f.run(append(args, "--yes")...).mustSucceed(t)
	reqs := f.requests("POST", path)
	if len(reqs) != 1 {
		t.Fatalf("want 1 request, got %d", len(reqs))
	}
	b := reqs[0].Body
	if b["hostname"] != "files02" || b["platform_kind"] != "linux" || b["memory_mb"] != float64(4096) || b["dry_run"] != nil {
		t.Errorf("body: %v", b)
	}
}

func TestServersRestartWaits(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST /servers/"+testServer+"/restart", 202, map[string]any{"id": testTask, "status": "queued", "summary": "Restart files01"})
	f.on("GET /tasks/"+testTask, 200, map[string]any{"id": testTask, "status": "done", "summary": "Restarted files01"})
	r := f.run("servers", "restart", testServer, "--yes", "--wait").mustSucceed(t)
	if !strings.Contains(r.Stderr, "Restarted files01") {
		t.Errorf("stderr: %s", r.Stderr)
	}
	if len(f.requests("GET", "/tasks/"+testTask)) == 0 {
		t.Error("did not poll the task")
	}
}

func TestServersRestartNeedsConfirmation(t *testing.T) {
	f := newFakeAPI(t)
	if r := f.run("servers", "restart", testServer); r.Code != exitUsage {
		t.Fatalf("exit %d, want %d", r.Code, exitUsage)
	}
}

func TestServersAccessGrant(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST /servers/"+testServer+"/access", 202, map[string]any{"server_id": testServer, "person_id": testPerson, "person_name": "Kari Nordmann", "level": "administrator", "status": "granting"})
	f.run("servers", "access", "grant", testServer, testPerson, "--level", "administrator").mustSucceed(t)
	b := f.requests("POST", "/servers/"+testServer+"/access")[0].Body
	if b["person_id"] != testPerson || b["level"] != "administrator" {
		t.Errorf("body: %v", b)
	}
}
