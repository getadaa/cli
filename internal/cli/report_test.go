package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportSendsBody(t *testing.T) {
	f := newFakeAPI(t)
	created := finding("open")
	created["summary"] = map[string]any{"text": "Printer jammed"}
	f.on("POST /organizations/"+testOrg+"/findings", 201, created)
	f.on("POST /findings/"+testFinding+"/attachments", 201, map[string]any{"id": "att_01JATX3M4K7Q2YV8N0RCBEZ5HS"})
	shot := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(shot, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := f.run("report", "Printer", "jammed", "--blocking", "--detail", "Second floor", "--attach", shot).mustSucceed(t)
	reqs := f.requests("POST", "/organizations/"+testOrg+"/findings")
	if len(reqs) != 1 {
		t.Fatalf("requests: %+v", reqs)
	}
	b := reqs[0].Body
	if b["summary"] != "Printer jammed" || b["blocking_work"] != true || b["detail"] != "Second floor" {
		t.Fatalf("body: %+v", b)
	}
	if len(f.requests("POST", "/findings/"+testFinding+"/attachments")) != 1 {
		t.Fatal("attachment was not uploaded")
	}
	if !strings.Contains(r.Stderr, testFinding) {
		t.Errorf("stderr should name the finding: %s", r.Stderr)
	}
}

func TestReportWithoutSummaryNeedsTerminal(t *testing.T) {
	f := newFakeAPI(t)
	if r := f.run("report"); r.Code != exitUsage {
		t.Fatalf("exit %d, want %d", r.Code, exitUsage)
	}
}

func TestReportMentionsDuplicate(t *testing.T) {
	f := newFakeAPI(t)
	created := finding("open")
	created["duplicate_of_finding_id"] = "fnd_01JATX3M4K7Q2YV8N0RCBEZ5HX"
	f.on("POST /organizations/"+testOrg+"/findings", 201, created)
	r := f.run("report", "Backup looks broken").mustSucceed(t)
	if !strings.Contains(r.Stderr, "fnd_01JATX3M4K7Q2YV8N0RCBEZ5HX") {
		t.Fatalf("stderr: %s", r.Stderr)
	}
}
