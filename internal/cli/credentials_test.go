package cli

import (
	"strings"
	"testing"
)

const testCredential = "crd_01JATX3M4K7Q2YV8N0RCBEZ5HS"

func TestCredentialsRevealPipedPrintsOnlySecret(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST /credentials/"+testCredential+"/reveal", 200, map[string]any{
		"id": testCredential, "username": "admin", "secret": "hunter2", "revealed_at": "2026-09-21T10:00:00Z"})
	r := f.run("credentials", "reveal", testCredential, "--reason", "VPN down").mustSucceed(t)
	if r.Stdout != "hunter2\n" {
		t.Fatalf("stdout %q", r.Stdout)
	}
	if got := f.requests("POST", "/credentials/"+testCredential+"/reveal")[0].Body["reason"]; got != "VPN down" {
		t.Fatalf("reason %v", got)
	}
}

func TestCredentialsRevealNeedsReason(t *testing.T) {
	f := newFakeAPI(t)
	if r := f.run("credentials", "reveal", testCredential); r.Code != exitUsage || !strings.Contains(r.Stderr, "--reason") {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
}

func TestCredentialsRevealRefusedForAgents(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST /credentials/"+testCredential+"/reveal", 403, problem(403, "agents-may-not-reveal", "Agenter kan ikke se hemmeligheter"))
	r := f.run("credentials", "reveal", testCredential, "--reason", "x")
	if r.Code != exitForbidden || !strings.Contains(r.Stderr, "only revealed to people") {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
}

func TestCredentialsAddReadsSecretFromStdin(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST /organizations/"+testOrg+"/credentials", 201, map[string]any{"id": testCredential, "name": "Router"})
	f.runWithInput("s3cret\n", "credentials", "add", "--name", "Router", "--kind", "password", "--secret-stdin").mustSucceed(t)
	b := f.requests("POST", "/organizations/"+testOrg+"/credentials")[0].Body
	if b["secret"] != "s3cret" || b["target_type"] != "organization" || b["kind"] != "password" {
		t.Fatalf("body %v", b)
	}
}

func TestCredentialsAddWithoutSecretFailsNamingFlag(t *testing.T) {
	f := newFakeAPI(t)
	if r := f.run("credentials", "add", "--name", "Router", "--kind", "password"); r.Code != exitUsage || !strings.Contains(r.Stderr, "--secret-stdin") {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
}

func TestCredentialsDeleteNeedsConfirmation(t *testing.T) {
	f := newFakeAPI(t)
	f.on("DELETE /credentials/"+testCredential, 204, nil)
	if r := f.run("credentials", "delete", testCredential); r.Code != exitUsage {
		t.Fatalf("exit %d without --yes", r.Code)
	}
	if n := len(f.requests("DELETE", "/credentials/"+testCredential)); n != 0 {
		t.Fatalf("deleted without confirmation")
	}
	f.run("credentials", "delete", testCredential, "--yes").mustSucceed(t)
	if n := len(f.requests("DELETE", "/credentials/"+testCredential)); n != 1 {
		t.Fatalf("%d deletes", n)
	}
}
