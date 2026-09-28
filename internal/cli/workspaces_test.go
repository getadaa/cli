package cli

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testWorkspace = "wsp_01JATX3M4K7Q2YV8N0RCBEZ5HS"
	testWSAccount = "wac_01JATX3M4K7Q2YV8N0RCBEZ5HS"
)

func TestWorkspacesConnectWaitsForConsent(t *testing.T) {
	orig := consentPollInterval
	consentPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { consentPollInterval = orig })

	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/providers", 200, map[string]any{"items": testProviders})
	conn := map[string]any{"id": testConnection, "provider": "microsoft_365", "method": "oauth", "name": "firma.no", "status": "pending",
		"consent_url": "https://login.microsoftonline.com/consent?x=1", "state": "s",
		"grants": []any{map[string]any{"summary": map[string]any{"text": "Read users"}, "write": false}}}
	f.on("POST /organizations/"+testOrg+"/provider-connections", 201, conn)
	var polls atomic.Int32
	f.onFunc("GET /provider-connections/"+testConnection, func(w http.ResponseWriter, r *http.Request) {
		cur := map[string]any{}
		for k, v := range conn {
			cur[k] = v
		}
		if polls.Add(1) >= 2 {
			cur["status"] = "unknown"
			cur["workspace_id"] = testWorkspace
		}
		writeJSON(w, 200, cur)
	})

	r := f.run("workspaces", "connect", "--vendor", "microsoft_365", "--domain", "firma.no", "--redirect-uri", "https://adaa.no/cb").mustSucceed(t)
	for _, want := range []string{"Read users", "login.microsoftonline.com", "Connected firma.no", testWorkspace} {
		if !strings.Contains(r.Stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, r.Stderr)
		}
	}
	reqs := f.requests("POST", "/organizations/"+testOrg+"/provider-connections")
	if len(reqs) != 1 {
		t.Fatalf("connect: %+v", reqs)
	}
	b := reqs[0].Body
	if b["provider"] != "microsoft_365" || b["method"] != "oauth" || b["account"] != "firma.no" || b["name"] != "firma.no" || b["redirect_uri"] != "https://adaa.no/cb" {
		t.Errorf("connect body: %v", b)
	}
}

func TestWorkspacesReauthorizeRenewsItsConnection(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/provider-connections", 200, page(
		map[string]any{"id": "pcn_01JATY03B4C5D6E7F8G9H0J1K3", "workspace_id": testWorkspace, "status": "revoked"},
		map[string]any{"id": testConnection, "workspace_id": testWorkspace, "status": "unauthorized"}))
	f.on("POST /provider-connections/"+testConnection+"/renew", 201, map[string]any{"id": testConnection, "status": "pending",
		"consent_url": "https://accounts.google.com/o/oauth2?x=1"})
	r := f.run("workspaces", "reauthorize", testWorkspace, "--no-wait").mustSucceed(t)
	if !strings.Contains(r.Stdout, "accounts.google.com") {
		t.Errorf("stdout should hold the consent link: %q", r.Stdout)
	}
	if n := len(f.requests("POST", "/provider-connections/"+testConnection+"/renew")); n != 1 {
		t.Errorf("renew calls: %d", n)
	}
}

func TestWorkspaceAccountByUPN(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/workspaces", 200, map[string]any{"items": []any{map[string]any{"id": testWorkspace, "primary_domain": "firma.no", "vendor": "microsoft_365", "status": "connected"}}})
	f.on("GET /workspaces/"+testWorkspace+"/accounts", 200, page(map[string]any{"id": testWSAccount, "workspace_id": testWorkspace, "user_principal_name": "kari@firma.no", "status": "active"}))
	f.on("POST /workspace-accounts/"+testWSAccount+"/reset-password", 200, map[string]any{"account_id": testWSAccount, "user_principal_name": "kari@firma.no", "password": "Tmp-1234-abcd", "must_change_at_next_sign_in": true, "reset_at": "2026-09-21T10:00:00Z"})
	r := f.run("workspaces", "accounts", "reset-password", "KARI@firma.no", "--yes").mustSucceed(t)
	if strings.TrimSpace(r.Stdout) != "Tmp-1234-abcd" {
		t.Errorf("stdout should hold only the password: %q", r.Stdout)
	}
	if b := f.requests("POST", "/workspace-accounts/"+testWSAccount+"/reset-password")[0].Body; b["sign_out_everywhere"] != true {
		t.Errorf("body: %v", b)
	}
}
