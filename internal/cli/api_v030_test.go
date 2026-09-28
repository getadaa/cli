package cli

import (
	"net/http"
	"strings"
	"testing"
)

func TestDomainsEditPublishesThroughAConnection(t *testing.T) {
	f := newFakeAPI(t)
	f.on("PATCH /domains/"+testDomain, 200, map[string]any{"id": testDomain, "name": "firma.no", "management": "provider"})
	f.run("domains", "edit", testDomain, "--dns-connection", testConnection).mustSucceed(t)
	b := f.requests("PATCH", "/domains/"+testDomain)[0].Body
	if b["management"] != "provider" || b["dns_connection_id"] != testConnection {
		t.Errorf("body: %v", b)
	}
	if r := f.run("domains", "edit", testDomain, "--management", "provider"); r.Code != exitUsage {
		t.Errorf("provider without a connection should be a usage error, got %d", r.Code)
	}
	if r := f.run("domains", "list", "--management", "provider"); r.Code == exitUsage {
		t.Errorf("provider is a valid list filter:\n%s", r.Stderr)
	}
}

// The API filters findings on one status, so open ones can sit behind a page of
// closed ones.
func TestFindingsListPagesPastClosed(t *testing.T) {
	f := newFakeAPI(t)
	closed := finding("closed")
	closed["id"] = "fnd_01JATX3M4K7Q2YV8N0RCBEZ5HT"
	f.onFunc("GET /organizations/"+testOrg+"/findings", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			writeJSON(w, 200, map[string]any{"items": []any{closed}, "next_cursor": "p2"})
			return
		}
		writeJSON(w, 200, page(finding("open")))
	})
	r := f.run("findings", "list", "--limit", "1").mustSucceed(t)
	if !strings.Contains(r.Stdout, "Microsoft 365 seat") {
		t.Fatalf("the open finding on page two is missing:\n%s", r.Stdout)
	}
}

func TestPeopleEditRoleFindsAMembershipOnALaterPage(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /people/"+testPerson, 200, personFixture(testPerson, "Kari Nordmann", "kari@firma.no", "active"))
	f.on("PATCH /people/"+testPerson, 200, personFixture(testPerson, "Kari Nordmann", "kari@firma.no", "active"))
	other := map[string]any{"identity": map[string]any{"email": "ola@firma.no"},
		"membership": map[string]any{"id": "mem_01JATX3M4K7Q2YV8N0RCBEZ5HT", "person_id": "per_other", "role": "org_member"}}
	mine := map[string]any{"identity": map[string]any{"email": "kari@firma.no"},
		"membership": map[string]any{"id": testMembership, "person_id": testPerson, "role": "org_member"}}
	f.onFunc("GET /organizations/"+testOrg+"/members", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			writeJSON(w, 200, map[string]any{"items": []any{other}, "next_cursor": "p2"})
			return
		}
		writeJSON(w, 200, page(mine))
	})
	f.on("PATCH /members/"+testMembership, 200, mine)
	f.run("people", "edit", testPerson, "--role", "org_admin").mustSucceed(t)
	if n := len(f.requests("POST", "/organizations/"+testOrg+"/members")); n != 0 {
		t.Errorf("granted a second membership instead of changing the first")
	}
	if n := len(f.requests("PATCH", "/members/"+testMembership)); n != 1 {
		t.Errorf("membership PATCHes: %d", n)
	}
}

func TestRequestsEditSeverityAndResource(t *testing.T) {
	f := newFakeAPI(t)
	const req = "req_01JATX3M4K7Q2YV8N0RCBEZ5HS"
	const res = "res_01JATX3M4K7Q2YV8N0RCBEZ5HS"
	f.on("PATCH /requests/"+req, 200, map[string]any{"id": req, "title": "New laptop", "severity": "critical"})
	f.run("requests", "edit", req, "--severity", "critical").mustSucceed(t)
	if b := f.requests("PATCH", "/requests/"+req)[0].Body; b["severity"] != "critical" || len(b) != 1 {
		t.Errorf("body: %v", b)
	}
	f.on("POST /organizations/"+testOrg+"/requests", 201, map[string]any{"id": req, "title": "Fix it"})
	f.run("requests", "new", "--title", "Fix it", "--body", "Broken", "--kind", "change", "--resource", res).mustSucceed(t)
	if b := f.requests("POST", "/organizations/"+testOrg+"/requests")[0].Body; b["resource_id"] != res {
		t.Errorf("body: %v", b)
	}
}

func TestOrgGrantAndRevokeCompanyServices(t *testing.T) {
	f := newFakeAPI(t)
	path := "/organizations/" + testOrg + "/entitlements"
	const ent = "ent_01JATX3M4K7Q2YV8N0RCBEZ5HS"
	f.on("POST "+path, 201, map[string]any{"id": ent, "service_code": "office-network", "service_name": "Office network"})
	f.run("org", "grant", "office-network", "--quantity", "2", "--yes").mustSucceed(t)
	if b := f.requests("POST", path)[0].Body; b["service_code"] != "office-network" || b["quantity"] != float64(2) {
		t.Errorf("body: %v", b)
	}
	f.on("GET "+path, 200, page(map[string]any{"id": ent, "service_code": "office-network", "fulfilled": true, "quantity": 2}))
	f.on("DELETE "+path+"/"+ent, 204, nil)
	f.run("org", "revoke", "office-network", "--yes").mustSucceed(t)
	if n := len(f.requests("DELETE", path+"/"+ent)); n != 1 {
		t.Errorf("deletes: %d", n)
	}
}

func TestLicensesEditSendsOnlyWhatChanged(t *testing.T) {
	f := newFakeAPI(t)
	const lic = "lic_01JATX3M4K7Q2YV8N0RCBEZ5HS"
	f.on("PATCH /licenses/"+lic, 200, map[string]any{"id": lic, "plan_name": "Creative Cloud"})
	f.run("licenses", "edit", lic, "--price", "949,00", "--auto-renew=false").mustSucceed(t)
	b := f.requests("PATCH", "/licenses/"+lic)[0].Body
	if b["unit_price_minor"] != float64(94900) || b["auto_renew"] != false || len(b) != 2 {
		t.Errorf("body: %v", b)
	}
}

func TestActivityInfersMembershipFromItsID(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /activity", 200, page())
	f.run("activity", "--entity", testMembership).mustSucceed(t)
	if q := f.requests("GET", "/activity")[0].Query; !strings.Contains(q, "entity_type=membership") {
		t.Errorf("query: %s", q)
	}
}
