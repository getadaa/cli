package cli

import (
	"strings"
	"testing"
)

const testLicense = "lic_01JATX3M4K7Q2YV8N0RCBEZ5HS"

func sampleLicense() map[string]any {
	return map[string]any{"id": testLicense, "vendor": "microsoft_365", "plan_name": "Microsoft 365 Business Standard",
		"seats_purchased": 10, "seats_assigned": 8, "seats_available": 2, "seats_inactive": 1, "currency": "NOK",
		"monthly_cost_minor": 149000, "wasted_monthly_minor": 44700, "term": "annual"}
}

func TestLicensesSeatsSendsCount(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/licenses", 200, page(sampleLicense()))
	f.on("PATCH /licenses/"+testLicense, 200, sampleLicense())
	f.run("licenses", "seats", "Microsoft 365 Business Standard", "12", "--yes").mustSucceed(t)
	reqs := f.requests("PATCH", "/licenses/"+testLicense)
	if len(reqs) != 1 || reqs[0].Body["seats_purchased"] != float64(12) || reqs[0].Body["dry_run"] != nil {
		t.Fatalf("requests %+v", reqs)
	}
}

func TestLicensesSeatsDryRun(t *testing.T) {
	f := newFakeAPI(t)
	f.on("PATCH /licenses/"+testLicense, 200, map[string]any{"dry_run": true, "summary": map[string]any{"text": "Add 2 seats"},
		"changes":    []any{},
		"cost_delta": map[string]any{"currency": "NOK", "monthly_before_minor": 149000, "monthly_after_minor": 178800, "monthly_change_minor": 29800}})
	r := f.run("licenses", "seats", testLicense, "12", "--dry-run").mustSucceed(t)
	if !strings.Contains(r.Stderr, "Add 2 seats") || !strings.Contains(r.Stderr, "+298,00 kr/month") {
		t.Fatalf("stderr: %s", r.Stderr)
	}
	if f.requests("PATCH", "/licenses/"+testLicense)[0].Body["dry_run"] != true {
		t.Fatal("not a dry run")
	}
}

func TestLicensesSeatsCommitmentActive(t *testing.T) {
	f := newFakeAPI(t)
	f.on("PATCH /licenses/"+testLicense, 409, problem(409, "commitment-active", "Bindingstiden er ikke over"))
	r := f.run("licenses", "seats", testLicense, "5", "--yes")
	if r.Code != exitRefused || !strings.Contains(r.Stderr, "Bindingstiden") {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
}

func TestLicensesListShowsWaste(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/licenses", 200, page(sampleLicense()))
	r := f.run("licenses", "list").mustSucceed(t)
	if !strings.Contains(r.Stdout, "8/10") || !strings.Contains(r.Stderr, "447,00 kr a month") {
		t.Fatalf("stdout: %s\nstderr: %s", r.Stdout, r.Stderr)
	}
}

func TestLicensesRelease(t *testing.T) {
	f := newFakeAPI(t)
	f.on("DELETE /licenses/"+testLicense+"/assignments/"+testPerson, 204, nil)
	r := f.run("licenses", "release", testLicense, testPerson, "--yes").mustSucceed(t)
	if !strings.Contains(r.Stderr, "Released the seat") {
		t.Fatalf("stderr: %s", r.Stderr)
	}
}
