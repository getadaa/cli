package cli

import (
	"strings"
	"testing"
)

func memberFixture(membership, person, name, email, role string) map[string]any {
	m := map[string]any{"id": membership, "organization_id": testOrg, "role": role,
		"created_at": "2026-09-01T10:00:00Z", "updated_at": "2026-09-01T10:00:00Z"}
	if person != "" {
		m["person_id"] = person
	}
	return map[string]any{
		"membership": m,
		"identity":   map[string]any{"id": testIdentity, "full_name": name, "email": email},
	}
}

func TestMembersListShowsRoleAndWhoIsExternal(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/members", 200, page(
		memberFixture(testMembership, testPerson, "Kari Nordmann", "kari@firma.no", "org_admin"),
		memberFixture("mem_01JATX3M4K7Q2YV8N0RCBEZ5HT", "", "Randi Revisor", "randi@revisor.no", "org_member"),
	))
	r := f.run("members", "list").mustSucceed(t)
	for _, want := range []string{"Kari Nordmann", "admin", "randi@revisor.no", "external"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("missing %q:\n%s", want, r.Stdout)
		}
	}
}

func TestMembersGrantSendsEmailAndRole(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST /organizations/"+testOrg+"/members", 201,
		memberFixture(testMembership, testPerson, "Ola Hansen", "ola@firma.no", "org_member"))
	f.run("members", "grant", "ola@firma.no", "--role", "org_member").mustSucceed(t)

	reqs := f.requests("POST", "/organizations/"+testOrg+"/members")
	if len(reqs) != 1 || reqs[0].Body["email"] != "ola@firma.no" || reqs[0].Body["role"] != "org_member" {
		t.Errorf("body: %+v", reqs)
	}
}

// A membership is found by the address of the human holding it, because nobody
// types a ULID.
func TestMembersRoleResolvesByEmail(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/members", 200, page(
		memberFixture(testMembership, testPerson, "Ola Hansen", "ola@firma.no", "org_member"),
	))
	f.on("PATCH /members/"+testMembership, 200,
		memberFixture(testMembership, testPerson, "Ola Hansen", "ola@firma.no", "org_admin"))

	f.run("members", "role", "ola@firma.no", "--role", "org_admin").mustSucceed(t)
	reqs := f.requests("PATCH", "/members/"+testMembership)
	if len(reqs) != 1 || reqs[0].Body["role"] != "org_admin" {
		t.Errorf("body: %+v", reqs)
	}
}

func TestMembersRevokeNeedsConfirmation(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/members", 200, page(
		memberFixture(testMembership, testPerson, "Ola Hansen", "ola@firma.no", "org_member"),
	))
	f.on("DELETE /members/"+testMembership, 204, nil)

	if r := f.run("members", "revoke", "ola@firma.no"); r.Code != exitUsage {
		t.Fatalf("without --yes: exit %d", r.Code)
	}
	f.run("members", "revoke", "ola@firma.no", "--yes").mustSucceed(t)
	if len(f.requests("DELETE", "/members/"+testMembership)) != 1 {
		t.Error("no DELETE sent")
	}
}

// The last administrator cannot be removed. The API refuses it, and the CLI has
// to relay what to do rather than swallowing it.
func TestMembersRevokeRelaysTheLastAdministratorRefusal(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/members", 200, page(
		memberFixture(testMembership, testPerson, "Kari Nordmann", "kari@firma.no", "org_admin"),
	))
	f.on("DELETE /members/"+testMembership, 409, problem(409, "last-administrator",
		"This is the only administrator who can sign in, so their access cannot be taken away."))

	r := f.run("members", "revoke", "kari@firma.no", "--yes")
	if r.Code != exitRefused {
		t.Fatalf("exit %d, want %d: %s", r.Code, exitRefused, r.Stderr)
	}
	if !strings.Contains(r.Stderr, "only administrator") {
		t.Errorf("the refusal was not relayed: %s", r.Stderr)
	}
}

// A role on a person is a membership underneath, so editing it is two calls and
// the second one is the one that matters.
func TestPeopleEditRoleGoesThroughTheMembership(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /people/"+testPerson, 200,
		personFixture(testPerson, "Ola Hansen", "ola@firma.no", "active"))
	f.on("GET /organizations/"+testOrg+"/members", 200, page(
		memberFixture(testMembership, testPerson, "Ola Hansen", "ola@firma.no", "org_member"),
	))
	f.on("PATCH /members/"+testMembership, 200,
		memberFixture(testMembership, testPerson, "Ola Hansen", "ola@firma.no", "org_admin"))

	f.run("people", "edit", testPerson, "--role", "org_admin").mustSucceed(t)

	if got := len(f.requests("PATCH", "/people/"+testPerson)); got != 0 {
		t.Errorf("the person record was patched %d times; a role is not a field on it", got)
	}
	reqs := f.requests("PATCH", "/members/"+testMembership)
	if len(reqs) != 1 || reqs[0].Body["role"] != "org_admin" {
		t.Errorf("body: %+v", reqs)
	}
}

// An employee with no membership gets one granted, rather than the change being
// quietly dropped.
func TestPeopleEditRoleGrantsWhenThereIsNoMembership(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /people/"+testPerson, 200,
		personFixture(testPerson, "Lager Larsen", "lager@firma.no", "active"))
	f.on("GET /organizations/"+testOrg+"/members", 200, page())
	f.on("POST /organizations/"+testOrg+"/members", 201,
		memberFixture(testMembership, testPerson, "Lager Larsen", "lager@firma.no", "org_member"))

	f.run("people", "edit", testPerson, "--role", "org_member").mustSucceed(t)

	reqs := f.requests("POST", "/organizations/"+testOrg+"/members")
	if len(reqs) != 1 || reqs[0].Body["person_id"] != testPerson {
		t.Errorf("body: %+v", reqs)
	}
}
