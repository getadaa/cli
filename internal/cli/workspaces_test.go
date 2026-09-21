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
	testWSAuth    = "wau_01JATX3M4K7Q2YV8N0RCBEZ5HS"
	testWSAccount = "wac_01JATX3M4K7Q2YV8N0RCBEZ5HS"
)

func TestWorkspacesConnectWaitsForConsent(t *testing.T) {
	orig := wsPollInterval
	wsPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { wsPollInterval = orig })

	f := newFakeAPI(t)
	auth := map[string]any{"id": testWSAuth, "vendor": "microsoft_365", "status": "pending",
		"consent_url": "https://login.microsoftonline.com/consent?x=1", "state": "s",
		"grants": []any{map[string]any{"summary": map[string]any{"text": "Read users"}, "write": false}}}
	f.on("POST /organizations/"+testOrg+"/workspaces/connect", 201, auth)
	var polls atomic.Int32
	f.onFunc("GET /workspace-authorizations/"+testWSAuth, func(w http.ResponseWriter, r *http.Request) {
		cur := map[string]any{}
		for k, v := range auth {
			cur[k] = v
		}
		if polls.Add(1) >= 2 {
			cur["status"] = "granted"
			cur["workspace_id"] = testWorkspace
		}
		writeJSON(w, 200, cur)
	})

	r := f.run("workspaces", "connect", "--vendor", "microsoft_365", "--domain", "firma.no", "--redirect-uri", "https://adaa.no/cb").mustSucceed(t)
	for _, want := range []string{"Read users", "login.microsoftonline.com", "Connected " + testWorkspace} {
		if !strings.Contains(r.Stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, r.Stderr)
		}
	}
	reqs := f.requests("POST", "/organizations/"+testOrg+"/workspaces/connect")
	if len(reqs) != 1 || reqs[0].Body["redirect_uri"] != "https://adaa.no/cb" || reqs[0].Body["primary_domain"] != "firma.no" {
		t.Errorf("connect body: %+v", reqs)
	}
}

func TestWorkspacesAuthorizationCompletesFromCallbackURL(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST /workspace-authorizations/"+testWSAuth+"/complete", 201, map[string]any{"id": testWorkspace, "primary_domain": "firma.no"})
	f.run("workspaces", "authorization", testWSAuth, "--callback-url", "http://localhost:1234/callback?code=abc&state=xyz").mustSucceed(t)
	reqs := f.requests("POST", "/workspace-authorizations/"+testWSAuth+"/complete")
	if len(reqs) != 1 || reqs[0].Body["code"] != "abc" || reqs[0].Body["state"] != "xyz" {
		t.Errorf("complete: %+v", reqs)
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
