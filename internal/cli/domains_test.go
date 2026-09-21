package cli

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/getadaa/cli/internal/dnscheck"
)

const (
	testDomain = "dom_01JATX3M4K7Q2YV8N0RCBEZ5HS"
	testDNSRec = "dns_01JATX3M4K7Q2YV8N0RCBEZ5HS"
)

func domainFixture() map[string]any {
	return map[string]any{
		"id": testDomain, "organization_id": testOrg, "name": "firma.no", "management": "external",
		"verification_status": "verified", "records_total": 4, "records_published": 3, "records_missing": 1,
		"nameservers": []string{"ns1.domeneshop.no"}, "registrar": "Domeneshop", "transfer_lock": true,
		"created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z",
	}
}

func TestDomainsListAndViewByName(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/domains", 200, page(domainFixture()))
	f.on("GET /domains/"+testDomain, 200, domainFixture())

	r := f.run("domains", "list").mustSucceed(t)
	if !strings.Contains(r.Stdout, "firma.no") || !strings.Contains(r.Stdout, "1 missing") {
		t.Errorf("list output: %s", r.Stdout)
	}
	r = f.run("domains", "view", "firma.no").mustSucceed(t)
	for _, want := range []string{"Domeneshop", "ns1.domeneshop.no", "3/4 published"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("view lacks %q:\n%s", want, r.Stdout)
		}
	}
	r = f.run("show", testDomain).mustSucceed(t)
	if !strings.Contains(r.Stdout, "Domeneshop") {
		t.Errorf("show did not reach domains view: %s", r.Stdout)
	}
}

func TestDNSAddSendsRecord(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/domains", 200, page(domainFixture()))
	f.on("POST /domains/"+testDomain+"/dns", 201, map[string]any{"id": testDNSRec, "type": "MX", "name": "@", "value": "mx.example.net", "status": "pending"})
	f.run("domains", "dns", "add", "firma.no", "--type", "mx", "--name", "@", "--value", "mx.example.net", "--priority", "10").mustSucceed(t)
	reqs := f.requests("POST", "/domains/"+testDomain+"/dns")
	if len(reqs) != 1 {
		t.Fatalf("want one POST, got %d", len(reqs))
	}
	b := reqs[0].Body
	if b["type"] != "MX" || b["value"] != "mx.example.net" || b["priority"] != float64(10) || b["ttl"] != nil {
		t.Errorf("body: %v", b)
	}
}

func TestDNSRemoveNeedsID(t *testing.T) {
	f := newFakeAPI(t)
	if r := f.run("domains", "dns", "remove", "www"); r.Code != exitUsage {
		t.Errorf("exit %d, want usage", r.Code)
	}
}

type fakeLookup struct{ ips map[string][]string }

func (f fakeLookup) LookupIPAddr(_ context.Context, h string) ([]net.IPAddr, error) {
	v, ok := f.ips[h]
	if !ok {
		return nil, &net.DNSError{Name: h, IsNotFound: true, Err: "no such host"}
	}
	var out []net.IPAddr
	for _, s := range v {
		out = append(out, net.IPAddr{IP: net.ParseIP(s)})
	}
	return out, nil
}
func (fakeLookup) LookupCNAME(_ context.Context, h string) (string, error) { return h, nil }
func (fakeLookup) LookupMX(_ context.Context, n string) ([]*net.MX, error) {
	return nil, &net.DNSError{Name: n, IsNotFound: true}
}
func (fakeLookup) LookupTXT(_ context.Context, n string) ([]string, error) {
	return nil, &net.DNSError{Name: n, IsNotFound: true}
}
func (fakeLookup) LookupNS(_ context.Context, n string) ([]*net.NS, error) {
	return nil, &net.DNSError{Name: n, IsNotFound: true}
}
func (fakeLookup) LookupSRV(_ context.Context, _, _, n string) (string, []*net.SRV, error) {
	return "", nil, &net.DNSError{Name: n, IsNotFound: true}
}

func TestDomainsCheck(t *testing.T) {
	orig := domainLookups
	t.Cleanup(func() { domainLookups = orig })
	domainLookups = func([]string) map[string]dnscheck.Lookup {
		return map[string]dnscheck.Lookup{"fake": fakeLookup{ips: map[string][]string{"www.firma.no": {"203.0.113.10"}}}}
	}
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/domains", 200, page(domainFixture()))
	records := []any{
		map[string]any{"id": testDNSRec, "type": "A", "name": "www", "value": "203.0.113.10", "enabled": true, "status": "published", "source": "manual"},
	}
	f.on("GET /domains/"+testDomain+"/dns", 200, map[string]any{"domain_id": testDomain, "domain_name": "firma.no", "records": records})
	r := f.run("domains", "check", "firma.no").mustSucceed(t)
	if !strings.Contains(r.Stderr, "All 1 records") {
		t.Errorf("stderr: %s", r.Stderr)
	}

	f2 := newFakeAPI(t)
	f2.on("GET /organizations/"+testOrg+"/domains", 200, page(domainFixture()))
	records = append(records, map[string]any{"id": "dns_2", "type": "A", "name": "shop", "value": "203.0.113.20", "enabled": true})
	f2.on("GET /domains/"+testDomain+"/dns", 200, map[string]any{"domain_id": testDomain, "domain_name": "firma.no", "records": records})
	r = f2.run("domains", "check", "firma.no")
	if r.Code == 0 || !strings.Contains(r.Stdout, "missing") {
		t.Errorf("exit %d, stdout: %s", r.Code, r.Stdout)
	}
}

func TestSPFUpdateMergesManualMechanisms(t *testing.T) {
	cur := map[string]any{"mechanisms": []any{
		map[string]any{"id": "m1", "type": "include", "value": "_spf.adaa.no", "source_kind": "automatic", "enabled": true},
		map[string]any{"id": "m2", "type": "include", "value": "_spf.crm.io", "source_kind": "manual", "enabled": true},
		map[string]any{"id": "m3", "type": "include", "value": "_spf.old.io", "source_kind": "automatic", "enabled": false},
	}}
	body, err := spfUpdate(cur, "", []string{"ip4:203.0.113.4"}, nil, []string{"m1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	manual := body["manual_mechanisms"].([]map[string]any)
	if len(manual) != 2 || manual[0]["value"] != "_spf.crm.io" || manual[1]["value"] != "203.0.113.4" {
		t.Errorf("manual: %v", manual)
	}
	disabled := body["disabled_mechanism_ids"].([]string)
	if len(disabled) != 2 {
		t.Errorf("disabled should keep m3 and add m1: %v", disabled)
	}
}
