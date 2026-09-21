package cli

import (
	"strings"
	"testing"
)

const testBackup = "bkp_01JATX3M4K7Q2YV8N0RCBEZ5HS"

func TestBackupsRestoreRequiresConfirmation(t *testing.T) {
	f := newFakeAPI(t)
	path := "/backups/" + testBackup + "/restore"
	f.on("POST "+path, 202, map[string]any{"id": testTask, "status": "awaiting_approval", "summary": "Restore files"})
	args := []string{"backups", "restore", testBackup, "--at", "2026-09-20T22:00:00Z", "--path", "/srv/finance"}

	if r := f.run(args...); r.Code != exitUsage {
		t.Fatalf("exit %d, want %d: %s", r.Code, exitUsage, r.Stderr)
	}
	if len(f.requests("POST", path)) != 0 {
		t.Fatal("restored without confirmation")
	}

	r := f.run(append(args, "--yes")...).mustSucceed(t)
	b := f.requests("POST", path)[0].Body
	if b["point_in_time"] != "2026-09-20T22:00:00Z" || b["dry_run"] != nil {
		t.Errorf("body: %v", b)
	}
	if !strings.Contains(r.Stderr, "adaa tasks approve") {
		t.Errorf("no approval hint: %s", r.Stderr)
	}
}

func TestBackupsRestoreNeedsTime(t *testing.T) {
	f := newFakeAPI(t)
	if r := f.run("backups", "restore", testBackup, "--yes"); r.Code != exitUsage {
		t.Fatalf("exit %d, want %d", r.Code, exitUsage)
	}
}

func TestBackupsListFlagsUntested(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/backups", 200, page(map[string]any{
		"id": testBackup, "name": "File server", "status": "unknown", "destination": "jottacloud", "append_only": true,
		"schedule_description": "Nightly at 02:00", "last_success_at": "2026-09-20T02:00:00Z",
	}))
	r := f.run("backups", "list").mustSucceed(t)
	if !strings.Contains(r.Stdout, "never tested") {
		t.Errorf("stdout: %s", r.Stdout)
	}
}
