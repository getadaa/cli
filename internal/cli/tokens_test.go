package cli

import (
	"strings"
	"testing"
)

func TestTokensCreatePrintsSecretOnce(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST /tokens", 201, map[string]any{"id": "tok_01JATX3M4K7Q2YV8N0RCBEZ5HS", "membership_id": testMembership,
		"name": "HR system", "prefix": "adaa_12", "secret": "adaa_12345678secret", "created_at": "2026-09-21T10:00:00Z"})
	r := f.run("tokens", "create", "--name", "HR system", "--expires-in-days", "30").mustSucceed(t)
	if strings.TrimSpace(r.Stdout) != "adaa_12345678secret" {
		t.Errorf("stdout should be only the secret: %q", r.Stdout)
	}
	if !strings.Contains(r.Stderr, "not be shown again") {
		t.Errorf("stderr: %s", r.Stderr)
	}
	b := f.requests("POST", "/tokens")[0].Body
	if b["membership_id"] != testMembership || b["expires_in_days"].(float64) != 30 {
		t.Errorf("body: %+v", b)
	}
}

func TestTokensListAndRevoke(t *testing.T) {
	f := newFakeAPI(t)
	id := "tok_01JATX3M4K7Q2YV8N0RCBEZ5HS"
	f.on("GET /tokens", 200, page(map[string]any{"id": id, "name": "HR system", "prefix": "adaa_12", "membership_id": testMembership, "created_at": "2026-09-21T10:00:00Z"}))
	f.on("DELETE /tokens/"+id, 204, nil)
	r := f.run("tokens", "list").mustSucceed(t)
	if !strings.Contains(r.Stdout, "HR system") {
		t.Errorf("list: %s", r.Stdout)
	}
	f.run("tokens", "revoke", "HR system", "--yes").mustSucceed(t)
	if len(f.requests("DELETE", "/tokens/"+id)) != 1 {
		t.Error("no DELETE")
	}
}

func TestOrgViewAndEdit(t *testing.T) {
	f := newFakeAPI(t)
	org := map[string]any{"id": testOrg, "name": "Firma AS", "status": "active", "org_number": "987654321", "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
	f.on("GET /organizations/"+testOrg, 200, org)
	f.on("PATCH /organizations/"+testOrg, 200, org)
	r := f.run("org", "view").mustSucceed(t)
	if !strings.Contains(r.Stdout, "987654321") {
		t.Errorf("view: %s", r.Stdout)
	}
	if r := f.run("org", "edit", "--org-number", "123"); r.Code != exitUsage {
		t.Errorf("bad org number: exit %d", r.Code)
	}
	f.run("org", "edit", "--contact-email", "it@firma.no").mustSucceed(t)
	if b := f.requests("PATCH", "/organizations/"+testOrg)[0].Body; b["primary_contact_email"] != "it@firma.no" || len(b) != 1 {
		t.Errorf("body: %+v", b)
	}
}
