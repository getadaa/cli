package cli

import (
	"strings"
	"testing"
)

const testConnection = "pcn_01JATY03B4C5D6E7F8G9H0J1K2"

var testProviders = []any{
	map[string]any{"slug": "bunny", "name": "Bunny", "services": []any{"dns"}, "methods": []any{"api_key"}, "connectable": true,
		"secret": map[string]any{"label": map[string]any{"text": "API key"}, "where": map[string]any{"text": "Account settings"}, "kind": "api_key", "username": false}},
	map[string]any{"slug": "microsoft_365", "name": "Microsoft 365", "services": []any{"workspace"}, "methods": []any{"oauth"}, "connectable": true,
		"account": map[string]any{"label": map[string]any{"text": "Primary domain"}, "required": true}},
	map[string]any{"slug": "adaa", "name": "adaa", "services": []any{"dns"}, "methods": []any{}, "connectable": false},
}

func TestConnectionsAddWithKey(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/providers", 200, map[string]any{"items": testProviders})
	f.on("POST /organizations/"+testOrg+"/provider-connections", 201, map[string]any{"id": testConnection, "name": "Bunny DNS", "status": "unknown"})
	f.runWithInput("sk-123\n", "connections", "add", "bunny", "--name", "Bunny DNS", "--secret-stdin").mustSucceed(t)
	b := f.requests("POST", "/organizations/"+testOrg+"/provider-connections")[0].Body
	if b["provider"] != "bunny" || b["method"] != "api_key" || b["secret"] != "sk-123" || b["name"] != "Bunny DNS" {
		t.Errorf("body: %v", b)
	}
	if _, ok := b["account"]; ok {
		t.Errorf("account sent though none was given: %v", b)
	}
}

func TestConnectionsAddRefusesUnconnectable(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/providers", 200, map[string]any{"items": testProviders})
	r := f.run("connections", "add", "adaa", "--secret-stdin")
	if r.Code == 0 || !strings.Contains(r.Stderr, "bunny, microsoft_365") {
		t.Errorf("want a usage error naming the connectable suppliers, got %d:\n%s", r.Code, r.Stderr)
	}
}

func TestConnectionsViewCompletesFromCallbackURL(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST /provider-connections/"+testConnection+"/complete", 201, map[string]any{"id": testConnection, "name": "firma.no", "status": "unknown", "workspace_id": testWorkspace})
	r := f.run("connections", "view", testConnection, "--callback-url", "http://localhost:1234/callback?code=abc&state=xyz").mustSucceed(t)
	reqs := f.requests("POST", "/provider-connections/"+testConnection+"/complete")
	if len(reqs) != 1 || reqs[0].Body["code"] != "abc" || reqs[0].Body["state"] != "xyz" {
		t.Errorf("complete: %+v", reqs)
	}
	if !strings.Contains(r.Stderr, "workspaces security "+testWorkspace) {
		t.Errorf("should point at the new workspace:\n%s", r.Stderr)
	}
}

func TestShowConnection(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /provider-connections/"+testConnection, 200, map[string]any{"id": testConnection, "name": "Bunny DNS", "provider": "bunny",
		"method": "api_key", "status": "unauthorized", "credential_id": "crd_01JATX3M4K7Q2YV8N0RCBEZ5HS",
		"failure": map[string]any{"text": "401 from Bunny"}})
	r := f.run("show", testConnection).mustSucceed(t)
	for _, want := range []string{"Bunny DNS", "401 from Bunny"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, r.Stdout)
		}
	}
	if !strings.Contains(r.Stderr, "credentials rotate") {
		t.Errorf("an unauthorized key should point at rotating it:\n%s", r.Stderr)
	}
}
