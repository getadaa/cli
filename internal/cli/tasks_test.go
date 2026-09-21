package cli

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

const coreTaskID = "tsk_01JATX3M4K7Q2YV8N0RCBEZ5HS"

func task(status string) map[string]any {
	return map[string]any{
		"id": coreTaskID, "organization_id": testOrg, "kind": "revoke_resource", "status": status, "execution": "connector",
		"summary": map[string]any{"text": "Revoke the Office seat"}, "destructive": true, "requires_approval": true,
		"steps": []any{
			map[string]any{"index": 0, "summary": "Checked the seat is unused", "status": "done"},
			map[string]any{"index": 1, "summary": "Releasing the seat", "status": "in_progress", "progress_percent": 40},
		},
		"created_at": "2026-09-20T10:00:00Z", "updated_at": "2026-09-20T10:00:00Z",
	}
}

func TestTasksApproveNeedsYes(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /tasks/"+coreTaskID, 200, task("awaiting_approval"))
	f.on("POST /tasks/"+coreTaskID+"/approve", 202, task("queued"))
	if r := f.run("tasks", "approve", coreTaskID); r.Code != exitUsage {
		t.Fatalf("exit %d without --yes, want %d: %s", r.Code, exitUsage, r.Stderr)
	}
	if n := len(f.requests("POST", "/tasks/"+coreTaskID+"/approve")); n != 0 {
		t.Fatalf("approved without confirmation (%d calls)", n)
	}
	r := f.run("tasks", "approve", coreTaskID, "--yes").mustSucceed(t)
	if len(f.requests("POST", "/tasks/"+coreTaskID+"/approve")) != 1 {
		t.Fatal("approve was not sent")
	}
	if !strings.Contains(r.Stderr, "adaa tasks wait") {
		t.Errorf("stderr should suggest waiting: %s", r.Stderr)
	}
}

func TestTasksWaitFollowsToDone(t *testing.T) {
	f := newFakeAPI(t)
	var polls atomic.Int32
	f.onFunc("GET /tasks/"+coreTaskID, func(w http.ResponseWriter, r *http.Request) {
		if polls.Add(1) == 1 {
			writeJSON(w, 200, task("in_progress"))
			return
		}
		writeJSON(w, 200, task("done"))
	})
	r := f.run("tasks", "wait", coreTaskID).mustSucceed(t)
	if polls.Load() < 2 || !strings.Contains(r.Stderr, "Revoke the Office seat") {
		t.Fatalf("polls=%d stderr=%s", polls.Load(), r.Stderr)
	}
}

func TestTasksWaitFailedExitsNonZero(t *testing.T) {
	f := newFakeAPI(t)
	failed := task("failed")
	failed["error"] = "The vendor refused"
	f.on("GET /tasks/"+coreTaskID, 200, failed)
	r := f.run("tasks", "wait", coreTaskID)
	if r.Code == 0 || !strings.Contains(r.Stderr, "The vendor refused") {
		t.Fatalf("exit %d stderr %s", r.Code, r.Stderr)
	}
}

func TestTasksViewShowsSteps(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /tasks/"+coreTaskID, 200, task("in_progress"))
	r := f.run("tasks", "view", coreTaskID).mustSucceed(t)
	for _, want := range []string{"Checked the seat is unused", "Releasing the seat 40%", "Destructive"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("view lacks %q:\n%s", want, r.Stdout)
		}
	}
}

func TestTasksListHidesDone(t *testing.T) {
	f := newFakeAPI(t)
	done := task("done")
	done["summary"] = map[string]any{"text": "Finished thing"}
	f.on("GET /organizations/"+testOrg+"/tasks", 200, page(task("queued"), done))
	r := f.run("tasks", "list").mustSucceed(t)
	if strings.Contains(r.Stdout, "Finished thing") || !strings.Contains(r.Stdout, "Revoke the Office seat") {
		t.Fatalf("list:\n%s", r.Stdout)
	}
}
