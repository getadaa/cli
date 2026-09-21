package cli

import (
	"net/url"
	"testing"
	"time"
)

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	cases := map[string]string{
		"24h":                  "2026-09-20T12:00:00Z",
		"7d":                   "2026-09-14T12:00:00Z",
		"2w":                   "2026-09-07T12:00:00Z",
		"30m":                  "2026-09-21T11:30:00Z",
		"2026-09-01":           "2026-09-01T00:00:00Z",
		"2026-09-01T08:00:00Z": "2026-09-01T08:00:00Z",
	}
	for in, want := range cases {
		got, err := parseSince(in, now)
		if err != nil || got != want {
			t.Errorf("parseSince(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "yesterday", "7x"} {
		if _, err := parseSince(bad, now); err == nil {
			t.Errorf("parseSince(%q) should fail", bad)
		}
	}
}

func TestActivityQuery(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /activity", 200, page(map[string]any{
		"id": "evt_01JATX3M4K7Q2YV8N0RCBEZ5HS", "at": "2026-09-20T10:00:00Z", "category": "change",
		"summary": map[string]any{"text": "Kari changed a mailbox"}, "outcome": "succeeded", "action": "update",
		"entity_type": "mailbox", "entity_id": "mbx_01JATX3M4K7Q2YV8N0RCBEZ5HS",
	}))
	f.run("activity", "--entity", "per_01JATX3M4K7Q2YV8N0RCBEZ5HS", "--since", "7d", "--actor", "agent").mustSucceed(t)
	q, _ := url.ParseQuery(f.requests("GET", "/activity")[0].Query)
	if q.Get("entity_type") != "person" || q.Get("actor_kind") != "agent" || q.Get("since") == "" || q.Get("organization_id") != testOrg {
		t.Fatalf("query: %v", q)
	}
	if r := f.run("activity", "--since", "soon"); r.Code != exitUsage {
		t.Fatalf("bad --since: exit %d", r.Code)
	}
}
