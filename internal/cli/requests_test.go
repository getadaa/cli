package cli

import (
	"strings"
	"testing"
)

const testRequest = "req_01JATX3M4K7Q2YV8N0RCBEZ5HS"

func TestRequestsViewRendersThread(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /requests/"+testRequest, 200, map[string]any{
		"id": testRequest, "organization_id": testOrg, "person_id": testPerson, "kind": "question", "status": "open",
		"title": "Guest Wi-Fi?", "message_count": 2, "created_at": "2026-09-20T10:00:00Z", "updated_at": "2026-09-20T10:00:00Z",
	})
	f.on("GET /requests/"+testRequest+"/messages", 200, page(
		map[string]any{"id": "m1", "request_id": testRequest, "author_person_id": testPerson, "author_name": "Kari Nordmann", "body": "Can we get one?", "created_at": "2026-09-20T10:00:00Z"},
		map[string]any{"id": "m2", "request_id": testRequest, "author_person_id": "per_01JATX3M4K7Q2YV8N0RCBEZ5HZ", "author_name": "adaa support", "body": "Yes, Tuesday.", "created_at": "2026-09-20T11:00:00Z"},
	))
	f.on("GET /requests/"+testRequest+"/attachments", 200, page())
	r := f.run("requests", "view", testRequest).mustSucceed(t)
	for _, want := range []string{"Guest Wi-Fi?", "Kari Nordmann (you)", "Can we get one?", "adaa support", "Yes, Tuesday."} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("view lacks %q:\n%s", want, r.Stdout)
		}
	}
	if !strings.Contains(f.run("requests", "view", testRequest, "--json").mustSucceed(t).Stdout, `"messages"`) {
		t.Error("--json should include messages")
	}
}

func TestRequestsNewAndReply(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST /organizations/"+testOrg+"/requests", 201, map[string]any{"id": testRequest, "title": "Monitors"})
	f.on("POST /requests/"+testRequest+"/messages", 201, map[string]any{"id": "m3"})
	f.run("requests", "new", "--kind", "order", "--title", "Monitors", "--body", "Two of them").mustSucceed(t)
	b := f.requests("POST", "/organizations/"+testOrg+"/requests")[0].Body
	if b["kind"] != "order" || b["title"] != "Monitors" || b["body"] != "Two of them" {
		t.Fatalf("body: %+v", b)
	}
	f.runWithInput("From stdin\n", "requests", "reply", testRequest, "--body-file", "-").mustSucceed(t)
	if got := f.requests("POST", "/requests/"+testRequest+"/messages")[0].Body["body"]; got != "From stdin\n" {
		t.Fatalf("reply body %q", got)
	}
	if r := f.run("requests", "new", "--title", "x"); r.Code != exitUsage {
		t.Fatalf("missing body: exit %d", r.Code)
	}
}
