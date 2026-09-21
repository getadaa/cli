package cli

import (
	"strings"
	"testing"
)

const testMailbox = "mbx_01JATX3M4K7Q2YV8N0RCBEZ5HS"

func TestMailboxArchiveNeedsYes(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/mail/mailboxes", 200, page(map[string]any{"id": testMailbox, "address": "ola@firma.no", "kind": "personal", "status": "active"}))
	f.on("DELETE /mail/mailboxes/"+testMailbox, 202, map[string]any{"id": "tsk_01JATX3M4K7Q2YV8N0RCBEZ5HS", "status": "awaiting_approval", "summary": "Archive ola@firma.no"})

	r := f.run("mail", "mailboxes", "archive", "ola@firma.no")
	if r.Code != exitUsage || !strings.Contains(r.Stderr, "--yes") {
		t.Fatalf("exit %d, stderr: %s", r.Code, r.Stderr)
	}
	if len(f.requests("DELETE", "/mail/mailboxes/"+testMailbox)) != 0 {
		t.Fatal("archived without confirmation")
	}

	r = f.run("mail", "mailboxes", "archive", "ola@firma.no", "--retain-days", "30", "--yes").mustSucceed(t)
	reqs := f.requests("DELETE", "/mail/mailboxes/"+testMailbox)
	if len(reqs) != 1 || reqs[0].Query != "retain_days=30" {
		t.Fatalf("requests: %+v", reqs)
	}
	if !strings.Contains(r.Stderr, "tasks approve") {
		t.Errorf("should point at approval: %s", r.Stderr)
	}
}

func TestMailboxAddPersonal(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST /organizations/"+testOrg+"/mail/mailboxes", 201, map[string]any{"id": testMailbox, "address": "kari@firma.no"})
	f.run("mail", "mailboxes", "add", "kari@firma.no", "--kind", "personal", "--person", "me", "--quota-gb", "2", "--yes").mustSucceed(t)
	reqs := f.requests("POST", "/organizations/"+testOrg+"/mail/mailboxes")
	if len(reqs) != 1 {
		t.Fatalf("want one POST, got %d", len(reqs))
	}
	b := reqs[0].Body
	if b["person_id"] != testPerson || b["quota_bytes"] != float64(2<<30) || b["dry_run"] != nil {
		t.Errorf("body: %v", b)
	}
}

func TestMailboxEditMergesAliases(t *testing.T) {
	if got := mailEditSet([]string{"a@x", "b@x"}, []string{"c@x", "A@x"}, []string{"b@x"}); strings.Join(got, ",") != "a@x,c@x" {
		t.Errorf("got %v", got)
	}
}
