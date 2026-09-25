package cli

import (
	"strings"
	"testing"
)

const testPerson2 = "per_01JATX3M4K7Q2YV8N0RCBEZ5HT"

func personFixture(id, name, email, status string) map[string]any {
	return map[string]any{"id": id, "organization_id": testOrg, "full_name": name, "email": email,
		"employment_status": status, "job_title": "Sales",
		"created_at": "2026-09-01T10:00:00Z", "updated_at": "2026-09-01T10:00:00Z"}
}

func TestPeopleList(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/people", 200, page(
		personFixture(testPerson, "Kari Nordmann", "kari@firma.no", "active"),
		personFixture(testPerson2, "Ola Nordmann", "ola@firma.no", "planned"),
	))
	r := f.run("people", "list", "--status", "active").mustSucceed(t)
	for _, want := range []string{"Kari Nordmann", "ola@firma.no", "planned"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("missing %q:\n%s", want, r.Stdout)
		}
	}
	reqs := f.requests("GET", "/organizations/"+testOrg+"/people")
	if len(reqs) == 0 || !strings.Contains(reqs[0].Query, "employment_status=active") {
		t.Errorf("status filter not sent: %+v", reqs)
	}
	if r := f.run("people", "list", "--status", "gone"); r.Code != exitUsage {
		t.Errorf("bad status: exit %d", r.Code)
	}
}

func TestPeopleViewByEmail(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/people", 200, page(
		personFixture(testPerson2, "Ola Nordmann", "ola@firma.no", "active")))
	f.on("GET /people/"+testPerson2, 200, personFixture(testPerson2, "Ola Nordmann", "ola@firma.no", "active"))
	f.on("GET /people/"+testPerson2+"/entitlements", 200, page(
		map[string]any{"id": "ent_01JATX3M4K7Q2YV8N0RCBEZ5HS", "service_code": "office-suite", "service_name": "Microsoft 365", "quantity": 1, "fulfilled": false}))
	f.on("GET /people/"+testPerson2+"/assignments", 200, map[string]any{
		"person_id":     testPerson2,
		"devices":       []any{map[string]any{"device_id": "dev_x", "name": "MacBook Air", "kind": "computer", "status": "in_use"}},
		"cost":          map[string]any{"currency": "NOK", "monthly_minor": 49900, "wasted_monthly_minor": 0},
		"license_seats": []any{}, "subscription_seats": []any{}, "workspace_accounts": []any{}, "other_resources": []any{},
	})
	r := f.run("people", "view", "ola@firma.no").mustSucceed(t)
	for _, want := range []string{"Ola Nordmann", "Microsoft 365", "not in place yet", "MacBook Air", "499,00 kr per month"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("missing %q:\n%s", want, r.Stdout)
		}
	}
	// adaa show routes per_ ids to the same view.
	r = f.run("show", testPerson2).mustSucceed(t)
	if !strings.Contains(r.Stdout, "Ola Nordmann") {
		t.Errorf("show: %s", r.Stdout)
	}
}

func TestPeopleAddNonInteractive(t *testing.T) {
	f := newFakeAPI(t)
	path := "/organizations/" + testOrg + "/people"
	f.on("POST "+path, 201, map[string]any{
		"id": testPerson2, "full_name": "Ola Nordmann", "email": "ola@firma.no", "employment_status": "planned",
		"plan": map[string]any{"summary": "Onboarding Ola", "tasks": []any{},
			"cost_delta": map[string]any{"currency": "NOK", "monthly_before_minor": 0, "monthly_after_minor": 24900, "monthly_change_minor": 24900}},
	})
	args := []string{"people", "add", "--name", "Ola Nordmann", "--email", "ola@firma.no",
		"--starts-on", "2099-01-01", "--service", "office-suite", "--service", "laptop:2", "--role", "none"}

	if r := f.run(args...); r.Code != exitUsage {
		t.Fatalf("without --yes: exit %d, want %d; %s", r.Code, exitUsage, r.Stderr)
	}
	if n := len(f.requests("POST", path)); n != 0 {
		t.Fatalf("sent %d requests without --yes", n)
	}

	r := f.run(append(args, "--yes")...).mustSucceed(t)
	reqs := f.requests("POST", path)
	if len(reqs) != 1 {
		t.Fatalf("want 1 POST, got %d", len(reqs))
	}
	b := reqs[0].Body
	if b["full_name"] != "Ola Nordmann" || b["employment_status"] != "planned" || b["started_on"] != "2099-01-01" {
		t.Errorf("body: %+v", b)
	}
	if _, ok := b["portal_role"]; ok {
		t.Errorf("role none should omit portal_role: %+v", b)
	}
	if _, ok := b["dry_run"]; ok {
		t.Errorf("real request carried dry_run: %+v", b)
	}
	ents, _ := b["entitlements"].([]any)
	if len(ents) != 2 || ents[1].(map[string]any)["quantity"].(float64) != 2 {
		t.Errorf("entitlements: %+v", b["entitlements"])
	}
	if !strings.Contains(r.Stderr, "Onboarding Ola") {
		t.Errorf("stderr: %s", r.Stderr)
	}
}

func TestPeopleAddDryRun(t *testing.T) {
	f := newFakeAPI(t)
	path := "/organizations/" + testOrg + "/people"
	f.on("POST "+path, 200, map[string]any{"dry_run": true, "summary": map[string]any{"text": "Would onboard Ola"},
		"changes":    []any{map[string]any{"action": "create", "summary": map[string]any{"text": "Create mailbox"}, "execution": "connector", "requires_approval": false}},
		"cost_delta": map[string]any{"currency": "NOK", "monthly_before_minor": 0, "monthly_after_minor": 24900, "monthly_change_minor": 24900}})
	r := f.run("people", "add", "--name", "Ola", "--email", "ola@firma.no", "--dry-run").mustSucceed(t)
	if !strings.Contains(r.Stderr, "Create mailbox") || !strings.Contains(r.Stderr, "+249,00 kr/month") {
		t.Errorf("preview: %s", r.Stderr)
	}
	if reqs := f.requests("POST", path); len(reqs) != 1 || reqs[0].Body["dry_run"] != true {
		t.Errorf("want one dry-run request: %+v", reqs)
	}
}

func TestPeopleAddNeedsName(t *testing.T) {
	f := newFakeAPI(t)
	r := f.run("people", "add", "--email", "ola@firma.no", "--yes")
	if r.Code != exitUsage || !strings.Contains(r.Stderr, "--name") {
		t.Errorf("exit %d, stderr %s", r.Code, r.Stderr)
	}
}

func TestPeopleOffboard(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/people", 200, page(
		personFixture(testPerson, "Kari Nordmann", "kari@firma.no", "active"),
		personFixture(testPerson2, "Ola Nordmann", "ola@firma.no", "active")))
	f.on("GET /people/"+testPerson2, 200, personFixture(testPerson2, "Ola Nordmann", "ola@firma.no", "active"))
	f.on("POST /people/"+testPerson2+"/offboard", 202, map[string]any{
		"summary":    "Offboarding Ola",
		"tasks":      []any{map[string]any{"id": "tsk_01JATX3M4K7Q2YV8N0RCBEZ5HS", "summary": "Revoke Microsoft 365", "status": "awaiting_approval", "requires_approval": true}},
		"cost_delta": map[string]any{"currency": "NOK", "monthly_before_minor": 24900, "monthly_after_minor": 0, "monthly_change_minor": -24900},
	})
	r := f.run("people", "offboard", "ola@firma.no", "--last-day", "2026-10-31", "--keep-mailbox-days", "90",
		"--forward-mail-to", "kari@firma.no", "--yes").mustSucceed(t)
	reqs := f.requests("POST", "/people/"+testPerson2+"/offboard")
	if len(reqs) != 1 {
		t.Fatalf("want 1 offboard POST, got %d", len(reqs))
	}
	b := reqs[0].Body
	if b["ended_on"] != "2026-10-31" || b["keep_mailbox_days"].(float64) != 90 || b["forward_mail_to"] != testPerson {
		t.Errorf("body: %+v", b)
	}
	if !strings.Contains(r.Stderr, "adaa tasks approve tsk_") {
		t.Errorf("no approval hint: %s", r.Stderr)
	}
}

func TestPeopleEditSendsOnlyChanged(t *testing.T) {
	f := newFakeAPI(t)
	f.on("PATCH /people/"+testPerson, 200, personFixture(testPerson, "Kari Nordmann", "kari@firma.no", "active"))
	f.run("people", "edit", testPerson, "--title", "Head of Sales", "--phone", "").mustSucceed(t)
	reqs := f.requests("PATCH", "/people/"+testPerson)
	if len(reqs) != 1 || len(reqs[0].Body) != 2 || reqs[0].Body["job_title"] != "Head of Sales" || reqs[0].Body["phone"] != nil {
		t.Errorf("body: %+v", reqs)
	}
}

func TestPeopleRevokeByCode(t *testing.T) {
	f := newFakeAPI(t)
	ent := "ent_01JATX3M4K7Q2YV8N0RCBEZ5HS"
	f.on("GET /people/"+testPerson+"/entitlements", 200, page(
		map[string]any{"id": ent, "service_code": "office-suite", "service_name": "Microsoft 365"}))
	f.on("DELETE /people/"+testPerson+"/entitlements/"+ent, 204, nil)
	if r := f.run("people", "revoke", testPerson, "office-suite"); r.Code != exitUsage {
		t.Fatalf("without --yes: exit %d", r.Code)
	}
	f.run("people", "revoke", testPerson, "office-suite", "--yes").mustSucceed(t)
	if len(f.requests("DELETE", "/people/"+testPerson+"/entitlements/"+ent)) != 1 {
		t.Error("no DELETE sent")
	}
}
