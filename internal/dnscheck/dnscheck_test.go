package dnscheck

import (
	"context"
	"net"
	"testing"
)

type fake struct {
	ips  map[string][]string
	mx   map[string][]*net.MX
	txt  map[string][]string
	cn   map[string]string
	fail bool
}

func notFound(name string) error {
	return &net.DNSError{Name: name, IsNotFound: true, Err: "no such host"}
}

func (f fake) LookupIPAddr(_ context.Context, h string) ([]net.IPAddr, error) {
	if f.fail {
		return nil, &net.DNSError{Name: h, Err: "timeout", IsTimeout: true}
	}
	v, ok := f.ips[h]
	if !ok {
		return nil, notFound(h)
	}
	var out []net.IPAddr
	for _, s := range v {
		out = append(out, net.IPAddr{IP: net.ParseIP(s)})
	}
	return out, nil
}
func (f fake) LookupCNAME(_ context.Context, h string) (string, error) {
	if c, ok := f.cn[h]; ok {
		return c, nil
	}
	return h + ".", nil
}
func (f fake) LookupMX(_ context.Context, n string) ([]*net.MX, error) {
	if v, ok := f.mx[n]; ok {
		return v, nil
	}
	return nil, notFound(n)
}
func (f fake) LookupTXT(_ context.Context, n string) ([]string, error) {
	if v, ok := f.txt[n]; ok {
		return v, nil
	}
	return nil, notFound(n)
}
func (f fake) LookupNS(_ context.Context, n string) ([]*net.NS, error) { return nil, notFound(n) }
func (f fake) LookupSRV(_ context.Context, _, _, n string) (string, []*net.SRV, error) {
	return "", nil, notFound(n)
}

func TestCheck(t *testing.T) {
	ten := int64(10)
	good := fake{
		ips: map[string][]string{"www.firma.no": {"203.0.113.10", "2001:db8::1"}},
		mx:  map[string][]*net.MX{"firma.no": {{Host: "MX.Example.net.", Pref: 10}}},
		txt: map[string][]string{"firma.no": {"v=spf1 include:_spf.adaa.no -all"}},
		cn:  map[string]string{"autodiscover.firma.no": "mail.adaa.no."},
	}
	stale := fake{ips: map[string][]string{"www.firma.no": {"198.51.100.1"}}}
	records := []Record{
		{Type: "A", Name: "www", Value: "203.0.113.10"},
		{Type: "MX", Name: "@", Value: "mx.example.net", Priority: &ten},
		{Type: "TXT", Name: "@", Value: `"v=spf1 include:_spf.adaa.no" " -all"`},
		{Type: "CNAME", Name: "autodiscover", Value: "mail.adaa.no."},
		{Type: "AAAA", Name: "www", Value: "2001:db8::2"},
		{Type: "CAA", Name: "@", Value: `0 issue "letsencrypt.org"`},
	}
	got := Check(context.Background(), "firma.no", records, map[string]Lookup{"a": good})
	want := []Status{OK, OK, OK, OK, Different, Unchecked}
	for n, r := range got {
		if r.Status != want[n] {
			t.Errorf("%s %s: status %s, want %s (seen %v)", r.Record.Type, r.FQDN, r.Status, want[n], r.Seen)
		}
	}

	got = Check(context.Background(), "firma.no", records[:1], map[string]Lookup{"a": good, "b": stale})
	if got[0].Status != Propagating {
		t.Errorf("mixed answers: %s, want propagating", got[0].Status)
	}
	got = Check(context.Background(), "firma.no", []Record{{Type: "A", Name: "nope", Value: "1.2.3.4"}}, map[string]Lookup{"a": good})
	if got[0].Status != Missing {
		t.Errorf("absent name: %s, want missing", got[0].Status)
	}
	got = Check(context.Background(), "firma.no", records[:1], map[string]Lookup{"a": fake{fail: true}})
	if got[0].Status != Failed {
		t.Errorf("resolver failure: %s, want error", got[0].Status)
	}
}

func TestTXTJoinsQuotedStrings(t *testing.T) {
	r := Record{Type: "TXT", Value: `"v=spf1 include:a" " -all"`}
	if !matches(r, []string{"v=spf1 include:a -all"}) {
		t.Error("split quoted TXT did not match its joined answer")
	}
}

func TestFQDN(t *testing.T) {
	for in, want := range map[string]string{"@": "firma.no", "": "firma.no", "www": "www.firma.no", "www.firma.no.": "www.firma.no", "FIRMA.no": "firma.no"} {
		if got := FQDN(in, "firma.no"); got != want {
			t.Errorf("FQDN(%q) = %q, want %q", in, got, want)
		}
	}
}
