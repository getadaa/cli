package cli

import (
	"net/http"
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

// The password goes in the body and nowhere else, and the command follows it
// until the API says the supplier has it.
func TestMailboxPasswordFollowsUntilApplied(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/mail/mailboxes", 200, page(map[string]any{"id": testMailbox, "address": "kari@firma.no", "kind": "personal", "status": "active"}))
	f.on("PUT /mail/mailboxes/"+testMailbox+"/password", 202, map[string]any{"credential_id": "crd_01JATXV8W9X0Y1Z2A3B4C5D6E7", "pending": true})
	asked := 0
	f.onFunc("GET /mail/mailboxes/"+testMailbox+"/password", func(w http.ResponseWriter, _ *http.Request) {
		asked++
		state := "pending"
		if asked > 1 {
			state = "applied"
		}
		writeJSON(w, 200, map[string]any{"credential_id": "crd_01JATXV8W9X0Y1Z2A3B4C5D6E7", "state": state})
	})

	r := f.runWithInput("et-ganske-langt-passord\n", "mail", "mailboxes", "password", "kari@firma.no", "--secret-stdin").mustSucceed(t)
	puts := f.requests("PUT", "/mail/mailboxes/"+testMailbox+"/password")
	if len(puts) != 1 || puts[0].Body["password"] != "et-ganske-langt-passord" {
		t.Fatalf("puts: %+v", puts)
	}
	gets := f.requests("GET", "/mail/mailboxes/"+testMailbox+"/password")
	if len(gets) != 2 || !strings.Contains(gets[0].Query, "credential_id=crd_01JATXV8W9X0Y1Z2A3B4C5D6E7") || !strings.Contains(gets[0].Query, "wait=") {
		t.Fatalf("gets: %+v", gets)
	}
	if !strings.Contains(r.Stderr, "Password changed at the mail supplier") {
		t.Errorf("stderr: %s", r.Stderr)
	}
	if strings.Contains(r.Stdout+r.Stderr, "et-ganske-langt-passord") {
		t.Error("the password was printed")
	}
}

// A failed attempt at the supplier is not a success, and says the old one
// still works.
func TestMailboxPasswordFailedIsAnError(t *testing.T) {
	f := newFakeAPI(t)
	f.on("PUT /mail/mailboxes/"+testMailbox+"/password", 202, map[string]any{"credential_id": "crd_01JATXV8W9X0Y1Z2A3B4C5D6E7", "pending": true})
	f.on("GET /mail/mailboxes/"+testMailbox+"/password", 200, map[string]any{"state": "failed"})

	r := f.runWithInput("et-ganske-langt-passord\n", "mail", "mailboxes", "password", testMailbox, "--secret-stdin")
	if r.Code != exitError || !strings.Contains(r.Stderr, "old one works") {
		t.Fatalf("exit %d, stderr: %s", r.Code, r.Stderr)
	}
}

// --no-wait returns once it is saved and asks nothing more.
func TestMailboxPasswordNoWait(t *testing.T) {
	f := newFakeAPI(t)
	f.on("PUT /mail/mailboxes/"+testMailbox+"/password", 202, map[string]any{"credential_id": "crd_01JATXV8W9X0Y1Z2A3B4C5D6E7", "pending": true})

	f.runWithInput("et-ganske-langt-passord\n", "mail", "mailboxes", "password", testMailbox, "--secret-stdin", "--no-wait").mustSucceed(t)
	if n := len(f.requests("GET", "/mail/mailboxes/"+testMailbox+"/password")); n != 0 {
		t.Fatalf("followed %d times with --no-wait", n)
	}
}
