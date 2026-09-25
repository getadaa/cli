package cli

import (
	"strings"
	"testing"
)

// Switching is an exchange: the session already proves who they are, so what
// comes back is a credential for somewhere else rather than a new sign-in.
func TestSwitchExchangesTheSessionForAnother(t *testing.T) {
	f := newFakeAPI(t)
	other := "org_01JATX3M4K7Q2YV8N0RCBEZ5HT"
	f.identity["memberships"] = []any{
		map[string]any{"membership_id": testMembership, "organization_id": testOrg,
			"organization_name": "Firma AS", "organization_slug": "firma", "role": "org_admin"},
		map[string]any{"membership_id": "mem_01JATX3M4K7Q2YV8N0RCBEZ5HT", "organization_id": other,
			"organization_name": "Bjerk AS", "organization_slug": "bjerk", "role": "org_member"},
	}
	f.on("POST /auth/session/switch", 201, map[string]any{
		"token": "adaa_sess_elsewhere", "expires_at": "2026-10-09T10:00:00Z",
		"identity":   map[string]any{"id": testIdentity, "full_name": "Kari Nordmann", "email": "kari@firma.no"},
		"membership": map[string]any{"id": "mem_01JATX3M4K7Q2YV8N0RCBEZ5HT", "organization_id": other, "role": "org_member"},
	})

	f.run("switch", "bjerk").mustSucceed(t)
	reqs := f.requests("POST", "/auth/session/switch")
	if len(reqs) != 1 || reqs[0].Body["organization_id"] != other {
		t.Errorf("body: %+v", reqs)
	}
}

// Somebody working for one customer has nowhere to go, and is told so rather
// than shown an empty picker.
func TestSwitchSaysSoWhenThereIsNowhereToGo(t *testing.T) {
	f := newFakeAPI(t)
	r := f.run("switch").mustSucceed(t)
	if !strings.Contains(r.Stderr, "nowhere to switch") {
		t.Errorf("stderr: %s", r.Stderr)
	}
	if len(f.requests("POST", "/auth/session/switch")) != 0 {
		t.Error("it tried to switch anyway")
	}
}

// A name that matches nothing they belong to lists what they may act on, rather
// than failing with an id nobody can use.
func TestSwitchNamesTheOrganizationsYouBelongTo(t *testing.T) {
	f := newFakeAPI(t)
	f.identity["memberships"] = []any{
		map[string]any{"organization_id": testOrg, "organization_name": "Firma AS", "organization_slug": "firma", "role": "org_admin"},
		map[string]any{"organization_id": "org_01JATX3M4K7Q2YV8N0RCBEZ5HT",
			"organization_name": "Bjerk AS", "organization_slug": "bjerk", "role": "org_member"},
	}
	r := f.run("switch", "kvist")
	if r.Code != exitUsage {
		t.Fatalf("exit %d, want %d: %s", r.Code, exitUsage, r.Stderr)
	}
	if !strings.Contains(r.Stderr, "bjerk") {
		t.Errorf("stderr should list what they may act on: %s", r.Stderr)
	}
}

// whoami is where somebody finds out they can reach more than one customer.
func TestWhoamiListsTheOtherOrganizations(t *testing.T) {
	f := newFakeAPI(t)
	f.identity["memberships"] = []any{
		map[string]any{"organization_id": testOrg, "organization_name": "Firma AS", "organization_slug": "firma", "role": "org_admin"},
		map[string]any{"organization_id": "org_01JATX3M4K7Q2YV8N0RCBEZ5HT",
			"organization_name": "Bjerk AS", "organization_slug": "bjerk", "role": "org_member"},
	}
	r := f.run("whoami").mustSucceed(t)
	for _, want := range []string{"Kari Nordmann", "org admin", "Also yours", "Bjerk AS"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("missing %q:\n%s", want, r.Stdout)
		}
	}
}
