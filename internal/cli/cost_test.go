package cli

import (
	"strings"
	"testing"
)

func TestCostRendersTotals(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/cost-summary", 200, map[string]any{
		"organization_id": testOrg, "currency": "NOK",
		"recurring_monthly_minor": 825000, "recurring_yearly_minor": 9900000, "one_time_minor": 0,
		"lines_awaiting_price": 1, "wasted_monthly_minor": 24900, "generated_at": "2026-09-21T10:00:00Z",
		"lines": []any{
			map[string]any{"subscription_id": "sub_01JATX3M4K7Q2YV8N0RCBEZ5HS", "service_code": "m365", "service_name": "Microsoft 365",
				"billing_period": "monthly", "quantity": 6, "observed_count": 5, "unit_price_minor": 24900,
				"monthly_equivalent_minor": 149400, "currency": "NOK", "priced": true},
			map[string]any{"subscription_id": "sub_01JATX3M4K7Q2YV8N0RCBEZ5HT", "service_code": "support", "service_name": "Support retainer",
				"billing_period": "monthly", "quantity": 1, "monthly_equivalent_minor": 675600, "currency": "NOK", "priced": true},
		},
	})
	r := f.run("cost").mustSucceed(t)
	for _, want := range []string{"8 250,00 kr", "99 000,00 kr", "estimate", "249,00 kr per month", "Support retainer", "(5 exist)"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("lacks %q:\n%s", want, r.Stdout)
		}
	}
	if strings.Index(r.Stdout, "Support retainer") > strings.Index(r.Stdout, "Microsoft 365") {
		t.Error("lines are not sorted by monthly cost")
	}
}

func TestSubscriptionsAssign(t *testing.T) {
	f := newFakeAPI(t)
	sub := "sub_01JATX3M4K7Q2YV8N0RCBEZ5HS"
	f.on("GET /organizations/"+testOrg+"/people", 200, page(map[string]any{"id": testPerson, "full_name": "Kari Nordmann", "email": "kari@firma.no"}))
	f.on("POST /subscriptions/"+sub+"/assignments", 201, map[string]any{"subscription_id": sub, "person_id": testPerson, "person_name": "Kari Nordmann", "assigned_at": "2026-09-21T10:00:00Z"})
	r := f.run("subscriptions", "assign", sub, "kari@firma.no", "--note", "New hire").mustSucceed(t)
	b := f.requests("POST", "/subscriptions/"+sub+"/assignments")[0].Body
	if b["person_id"] != testPerson || b["note"] != "New hire" {
		t.Errorf("body: %v", b)
	}
	if !strings.Contains(r.Stderr, "Kari Nordmann") {
		t.Errorf("stderr: %s", r.Stderr)
	}
}

func TestTimeDefaultsToThisMonth(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/time-summary", 200, map[string]any{
		"organization_id": testOrg, "from": "2026-09-01", "to": "2026-09-21", "minutes_logged": 200, "minutes_billable": 180,
		"retainer_minutes_included": 240, "minutes_remaining": 60, "generated_at": "2026-09-21T10:00:00Z",
	})
	r := f.run("time").mustSucceed(t)
	q := f.requests("GET", "/organizations/"+testOrg+"/time-summary")[0].Query
	if !strings.Contains(q, "from=") || !strings.Contains(q, "-01") {
		t.Errorf("query: %s", q)
	}
	if !strings.Contains(r.Stdout, "1h 00m left") {
		t.Errorf("stdout: %s", r.Stdout)
	}
}
